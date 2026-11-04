// Package runtimebundle 让发布构建把 Bun 运行时、适配器与供应商插件内嵌进
// 单个 exe：首次运行时解压到配置目录，之后直接复用解压结果。
//
// 资源只在 -tags embeddedruntime 的构建里嵌入（见 assets_embed.go）。普通开发
// 构建走 assets_none.go，Available 返回 false，不产生任何解压行为，也不需要
// 生成资源目录。
//
// 解压布局（version 由发布构建的 qka/internal/cli.Version 注入）：
//
//	<configDir>/runtime/<version>/bun(.exe)
//	<configDir>/runtime/<version>/adapters/
//	<configDir>/runtime/<version>/provider-plugins/
//	<configDir>/runtime/<version>/.ready   ← 就绪标记，内容是格式号与版本
//
// 发布是原子的：先解压到同级临时目录，写完标记后整体 rename 到目标路径，
// 因此任何时刻目标目录要么不存在、要么完整可用。版本变化时目标路径不同，
// 自然重新解压，不会复用上一版的文件。
//
// 每次解压完成（或启动命中缓存）后会清理 runtime/ 下的历史版本目录，最多
// 保留「当前 + 上一个版本」两份（回滚需要）；清理是尽力而为的附属动作，
// 删不掉（如 Windows 上旧 bun.exe 被残留子进程占用）就下次再试，绝不影响
// 主流程（见 cleanup.go）。
package runtimebundle

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"qka/internal/applog"
)

// 内嵌资源文件名，由 build/embedassets 生成。
const (
	// AssetBun 是 zstd 压缩的 Bun 可执行文件。
	AssetBun = "bun.zst"
	// AssetAdapters 是 adapters 目录的 tar.zst 打包。
	AssetAdapters = "adapters.tar.zst"
	// AssetPlugins 是 provider-plugins 目录的 tar.zst 打包。
	AssetPlugins = "provider-plugins.tar.zst"
	// AssetMetadata 记录资源对应的目标架构，供构建期排查。
	AssetMetadata = "bundle.json"
)

// 解压产物在 <configDir>/runtime/<version>/ 下的固定名字。
const (
	// AdaptersDirName 是解压后的适配器父目录名。
	AdaptersDirName = "adapters"
	// PluginsDirName 是解压后的供应商插件父目录名。
	PluginsDirName = "provider-plugins"

	runtimeDirName = "runtime"
	markerName     = ".ready"
	markerFormat   = "qka-runtime-bundle/1"

	// stagingPrefix 是解压临时目录的前缀（见 publish）。清理历史版本时跳过
	// 该前缀的目录：它可能正被并发进程写入，删了会破坏对方的原子发布。
	stagingPrefix = ".staging-"
)

// ErrUnavailable 表示本次构建没有内嵌运行时：未加 embeddedruntime 标签，
// 或加了标签但资源目录为空。
var ErrUnavailable = errors.New("runtimebundle: 本次构建未内嵌运行时")

// Paths 是解压后的内嵌运行时路径。
type Paths struct {
	// Bun 是解压出的 Bun 可执行文件路径。
	Bun string
	// AdaptersRoot 是解压出的适配器父目录。
	AdaptersRoot string
	// PluginsRoot 是解压出的供应商插件父目录。
	PluginsRoot string
}

// ensureMu 串行化进程内的解压，避免同一进程并发解压同一份 115MB 资源。
// 跨进程由「临时目录 + 原子 rename」保护：后到者 rename 失败后会发现目标
// 已就绪，直接复用先到者的结果。
var ensureMu sync.Mutex

// 测试接缝：真实失败路径（磁盘满、权限不足、被别的进程抢先发布）无法稳定
// 制造，替换这些变量来覆盖错误分支。
var (
	mkdirTemp  = os.MkdirTemp
	removePath = os.RemoveAll
	renamePath = os.Rename
	writeFile  = os.WriteFile
	chmodPath  = os.Chmod
	createFile = func(name string, perm fs.FileMode) (sink, error) {
		return os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	}
)

// sink 是解压目标文件的写入端；抽成接口是为了覆盖关闭失败的分支。
type sink interface {
	io.Writer
	io.Closer
}

// Available 报告本次构建是否内嵌了运行时资源。
func Available() bool {
	return embeddedAssets() != nil
}

// Ensure 保证 <configDir>/runtime/<version>/ 下有可用的内嵌运行时，并返回
// 其路径。首次调用解压，之后命中 .ready 标记直接返回；version 变化解压到
// 新目录。并发调用安全。
//
// 解压失败返回错误且不留半成品目录；调用方应当回退到旧的文件布局，
// 不要把错误升级成崩溃。
func Ensure(configDir, version string) (Paths, error) {
	return ensureWith(configDir, version, embeddedAssets())
}

// ensureWith 是 Ensure 的实现，assets 为 nil 时即「本次构建未内嵌」。
func ensureWith(configDir, version string, assets fs.FS) (Paths, error) {
	if assets == nil {
		// 开发构建每次调用都会走到这里，用 debug 级别避免刷日志。
		applog.Debugf("runtimebundle", "本次构建未内嵌运行时，跳过解压")
		return Paths{}, ErrUnavailable
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return Paths{}, errors.New("runtimebundle: 配置目录为空")
	}
	version = strings.TrimSpace(version)
	if !validVersion(version) {
		return Paths{}, fmt.Errorf("runtimebundle: 版本 %q 不能用作目录名", version)
	}

	target := filepath.Join(configDir, runtimeDirName, version)
	if isReady(target, version) {
		applog.Debugf("runtimebundle", "命中已解压运行时 version=%s path=%s", version, target)
		// 启动命中缓存时也顺带清理历史版本目录（升级后旧副本不再增长）；
		// 清理自身绝不报错，失败只记日志、下次启动再试。
		cleanupOldVersions(filepath.Dir(target), version)
		return pathsFor(target), nil
	}

	ensureMu.Lock()
	defer ensureMu.Unlock()
	paths, err := ensureLocked(target, version, assets)
	if err == nil {
		// 解压完成后清理旧版本目录：保留当前 + 上一个版本，其余删除。
		cleanupOldVersions(filepath.Dir(target), version)
	}
	return paths, err
}

// ensureLocked 在持有 ensureMu 时再检查一次目标，避免同一版本被重复解压。
func ensureLocked(target, version string, assets fs.FS) (Paths, error) {
	// 等锁期间可能已被其他调用（或另一个进程）解压完成。
	if isReady(target, version) {
		applog.Debugf("runtimebundle", "等锁期间运行时已就绪，直接复用 version=%s path=%s", version, target)
		return pathsFor(target), nil
	}
	// 目标存在但不完整（上次解压中途被杀、或历史版本留下的半成品）会挡住
	// rename，先清掉再重建。跨进程同时清理只会让后到者重建一份完好副本。
	if _, err := os.Stat(target); err == nil {
		if err := removePath(target); err != nil {
			applog.Errorf("runtimebundle", "清理残留运行时目录失败 path=%s: %v", target, err)
			return Paths{}, fmt.Errorf("runtimebundle: 清理残留目录 %s: %w", target, err)
		}
	}
	// 首次解压 100MB 级资源要几秒，开始与耗时都记一行，方便排查「启动卡住」。
	started := time.Now()
	applog.Infof("runtimebundle", "开始解压内嵌运行时 version=%s target=%s", version, target)
	if err := publish(target, version, assets); err != nil {
		applog.Errorf("runtimebundle", "解压内嵌运行时失败 version=%s: %v", version, err)
		return Paths{}, err
	}
	applog.Infof("runtimebundle", "内嵌运行时解压完成 version=%s 耗时=%s",
		version, time.Since(started).Round(time.Millisecond))
	return pathsFor(target), nil
}

// validVersion 拒绝任何可能逃出 runtime/ 目录的版本字符串。
func validVersion(version string) bool {
	if version == "" || version == "." || version == ".." {
		return false
	}
	if strings.ContainsAny(version, `/\:`) {
		return false
	}
	return filepath.Base(version) == version
}

// publish 解压到同级临时目录，再原子 rename 到最终路径。
func publish(target, version string, assets fs.FS) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("runtimebundle: 创建 %s: %w", parent, err)
	}
	staging, err := mkdirTemp(parent, stagingPrefix+version+"-*")
	if err != nil {
		return fmt.Errorf("runtimebundle: 创建临时目录: %w", err)
	}
	committed := false
	defer func() {
		// 无论成功失败，临时目录都不能留下。
		if !committed {
			_ = removePath(staging)
		}
	}()

	if err := unpack(staging, assets); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(staging, markerName),
		[]byte(markerFormat+"\n"+version+"\n"), 0o600); err != nil {
		return fmt.Errorf("runtimebundle: 写就绪标记: %w", err)
	}
	if err := renamePath(staging, target); err != nil {
		// 另一进程可能刚好发布了同一版本：目标就绪即视为成功。
		if isReady(target, version) {
			return nil
		}
		return fmt.Errorf("runtimebundle: 发布 %s: %w", target, err)
	}
	committed = true
	return nil
}

// unpack 解压全部资源到 dest。
func unpack(dest string, assets fs.FS) error {
	if err := extractBun(dest, assets); err != nil {
		return err
	}
	for _, dir := range []struct{ asset, name string }{
		{AssetAdapters, AdaptersDirName},
		{AssetPlugins, PluginsDirName},
	} {
		if err := extractTarZstd(dest, dir.asset, dir.name, assets); err != nil {
			return err
		}
	}
	return nil
}

// extractBun 解压 zstd 压缩的 Bun 可执行文件，并保留可执行权限。
func extractBun(dest string, assets fs.FS) error {
	src, err := assets.Open(AssetBun)
	if err != nil {
		return fmt.Errorf("runtimebundle: 打开 %s: %w", AssetBun, err)
	}
	defer src.Close()

	// 默认配置下 NewReader 不会失败：资源损坏会在读取时以解压错误暴露。
	dec, _ := zstd.NewReader(src)
	defer dec.Close()

	bun := filepath.Join(dest, bunExecutableName)
	out, err := createFile(bun, 0o755)
	if err != nil {
		return fmt.Errorf("runtimebundle: 创建 %s: %w", bun, err)
	}
	if _, err := io.Copy(out, dec); err != nil {
		_ = out.Close()
		return fmt.Errorf("runtimebundle: 解压 %s: %w", bun, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("runtimebundle: 关闭 %s: %w", bun, err)
	}
	// Windows 上 chmod 基本无副作用；Unix 上保证解压出来的 bun 可执行。
	if err := chmodPath(bun, 0o755); err != nil {
		return fmt.Errorf("runtimebundle: 设置 %s 权限: %w", bun, err)
	}
	return nil
}

// extractTarZstd 解压一个 tar.zst 目录包；只接受以 dirName/ 开头的条目。
func extractTarZstd(dest, asset, dirName string, assets fs.FS) error {
	src, err := assets.Open(asset)
	if err != nil {
		return fmt.Errorf("runtimebundle: 打开 %s: %w", asset, err)
	}
	defer src.Close()

	// 同上：NewReader 不返回错误，坏包在读取条目时暴露。
	dec, _ := zstd.NewReader(src)
	defer dec.Close()

	tr := tar.NewReader(dec)
	seen := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("runtimebundle: 读取 %s: %w", asset, err)
		}
		name := filepath.ToSlash(filepath.Clean(filepath.FromSlash(hdr.Name)))
		if name != dirName && !strings.HasPrefix(name, dirName+"/") {
			return fmt.Errorf("runtimebundle: %s 含意外条目 %q", asset, hdr.Name)
		}
		seen = true
		// 条目名已确认以 dirName/ 开头且是相对路径，Join 不会逃出解压目录。
		if err := writeEntry(filepath.Join(dest, name), hdr, tr); err != nil {
			return fmt.Errorf("runtimebundle: %s 条目 %q: %w", asset, hdr.Name, err)
		}
	}
	if !seen {
		return fmt.Errorf("runtimebundle: %s 里没有 %s/ 目录", asset, dirName)
	}
	return nil
}

// writeEntry 重建一个 tar 条目，保留目录与可执行位。
func writeEntry(target string, hdr *tar.Header, r io.Reader) error {
	perm := fs.FileMode(hdr.Mode).Perm()
	if hdr.Typeflag == tar.TypeDir {
		if perm == 0 {
			perm = 0o755
		}
		if err := os.MkdirAll(target, perm); err != nil {
			return fmt.Errorf("runtimebundle: 创建目录 %s: %w", target, err)
		}
		if err := chmodPath(target, perm); err != nil {
			return fmt.Errorf("runtimebundle: 设置目录 %s 权限: %w", target, err)
		}
		return nil
	}
	if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
		return fmt.Errorf("runtimebundle: 不支持的条目类型 %q", hdr.Typeflag)
	}
	if perm == 0 {
		perm = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("runtimebundle: 创建目录 %s: %w", filepath.Dir(target), err)
	}
	out, err := createFile(target, perm)
	if err != nil {
		return fmt.Errorf("runtimebundle: 创建 %s: %w", target, err)
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return fmt.Errorf("runtimebundle: 写入 %s: %w", target, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("runtimebundle: 关闭 %s: %w", target, err)
	}
	if err := chmodPath(target, perm); err != nil {
		return fmt.Errorf("runtimebundle: 设置 %s 权限: %w", target, err)
	}
	return nil
}

// isReady 校验目标目录已完整解压：标记格式/版本匹配且关键产物都在。
func isReady(target, version string) bool {
	data, err := os.ReadFile(filepath.Join(target, markerName))
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || lines[0] != markerFormat || lines[1] != version {
		return false
	}
	if !fileExists(filepath.Join(target, bunExecutableName)) {
		return false
	}
	return isDir(filepath.Join(target, AdaptersDirName)) && isDir(filepath.Join(target, PluginsDirName))
}

// pathsFor 按固定布局给出解压产物路径。
func pathsFor(target string) Paths {
	return Paths{
		Bun:          filepath.Join(target, bunExecutableName),
		AdaptersRoot: filepath.Join(target, AdaptersDirName),
		PluginsRoot:  filepath.Join(target, PluginsDirName),
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
