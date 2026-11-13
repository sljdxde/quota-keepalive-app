// Command embedassets 生成 QKA 内嵌运行时资源。
//
// Windows 发布构建把 Bun、adapters/ 与 provider-plugins/ 压成
// internal/runtimebundle/assets 下的 bun.zst / adapters.tar.zst /
// provider-plugins.tar.zst，再由 -tags embeddedruntime 的构建嵌入 exe，
// 由 internal/runtimebundle 在首次运行时解压到配置目录。
//
// 用法：
//
//	go run ./build/embedassets --arch amd64 \
//	    --bun dist/bun/bun.exe --adapters adapters --plugins provider-plugins
//
// 生成前会清空输出目录（保留 .gitignore/.gitkeep 占位），避免上一版残留的包
// 被一起嵌入；每个产物先写临时文件再 rename，中途失败不会留下半个包。
// tar 包内的修改时间与属主被归一化，同样的输入得到同样的资源，便于发布复核。
package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"

	"qka/internal/runtimebundle"
)

// temporaryFile 是 zstd 输出端；抽成接口是为了让测试覆盖关闭失败的分支。
type temporaryFile interface {
	Name() string
	io.Writer
	io.Closer
}

// 测试接缝：构建工具的真实磁盘失败（磁盘满、权限不足）无法稳定制造。
var (
	createTemp = func(dir, pattern string) (temporaryFile, error) {
		return os.CreateTemp(dir, pattern)
	}
	renamePath = os.Rename
	removePath = os.Remove
	writeFile  = os.WriteFile
	walkDir    = filepath.WalkDir
	readDir    = os.ReadDir
	removeAll  = os.RemoveAll
	runCommand = run
)

func main() {
	if err := runCommand(os.Args[1:]); err != nil {
		panic(err)
	}
}

// genOptions 是一次资源生成的全部输入。
type genOptions struct {
	Arch     string
	Bun      string
	Adapters string
	Plugins  string
	Out      string
}

func run(args []string) error {
	flags := flag.NewFlagSet("embedassets", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	arch := flags.String("arch", "", "目标架构（amd64/arm64），写入 bundle.json")
	bun := flags.String("bun", "", "Bun 可执行文件路径")
	adapters := flags.String("adapters", "", "adapters 目录")
	plugins := flags.String("plugins", "", "provider-plugins 目录")
	out := flags.String("out", filepath.Join("internal", "runtimebundle", "assets"), "资源输出目录")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return generate(genOptions{
		Arch:     *arch,
		Bun:      *bun,
		Adapters: *adapters,
		Plugins:  *plugins,
		Out:      *out,
	})
}

// generate 校验输入、清空输出目录并写出全部资源。
func generate(o genOptions) error {
	if !validArch(o.Arch) {
		return fmt.Errorf("--arch 必须是 amd64 或 arm64，实际 %q", o.Arch)
	}
	if o.Bun == "" {
		return errors.New("缺少 --bun")
	}
	info, err := os.Stat(o.Bun)
	if err != nil {
		return fmt.Errorf("--bun 路径不可用: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("--bun 必须是文件: %s", o.Bun)
	}
	for _, dir := range []struct{ flag, path string }{
		{"--adapters", o.Adapters},
		{"--plugins", o.Plugins},
	} {
		entry, err := os.Stat(dir.path)
		if err != nil {
			return fmt.Errorf("%s 路径不可用: %w", dir.flag, err)
		}
		if !entry.IsDir() {
			return fmt.Errorf("%s 必须是目录: %s", dir.flag, dir.path)
		}
	}
	if o.Out == "" {
		return errors.New("缺少 --out")
	}
	if err := resetOut(o.Out); err != nil {
		return err
	}
	if err := compressFile(filepath.Join(o.Out, runtimebundle.AssetBun), o.Bun); err != nil {
		return err
	}
	if err := compressTar(filepath.Join(o.Out, runtimebundle.AssetAdapters),
		o.Adapters, runtimebundle.AdaptersDirName); err != nil {
		return err
	}
	if err := compressTar(filepath.Join(o.Out, runtimebundle.AssetPlugins),
		o.Plugins, runtimebundle.PluginsDirName); err != nil {
		return err
	}
	// 只含一个字符串字段，MarshalIndent 不会失败。
	meta, _ := json.MarshalIndent(struct {
		Arch string `json:"arch"`
	}{Arch: o.Arch}, "", "  ")
	if err := writeFile(filepath.Join(o.Out, runtimebundle.AssetMetadata), append(meta, '\n'), 0o644); err != nil {
		return fmt.Errorf("写 bundle.json: %w", err)
	}
	return nil
}

// validArch 只接受发布矩阵覆盖的两种架构。
func validArch(arch string) bool {
	return arch == "amd64" || arch == "arm64"
}

// resetOut 清空输出目录，但保留目录占位文件。
func resetOut(out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("创建输出目录 %s: %w", out, err)
	}
	entries, err := readDir(out)
	if err != nil {
		return fmt.Errorf("读取输出目录 %s: %w", out, err)
	}
	for _, entry := range entries {
		switch entry.Name() {
		case ".gitignore", ".gitkeep":
			// 目录占位文件不是生成物，保留。
			continue
		}
		if err := removeAll(filepath.Join(out, entry.Name())); err != nil {
			return fmt.Errorf("清空 %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// compressFile 把 src 文件 zstd 压缩到 dest。
func compressFile(dest, src string) error {
	file, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", src, err)
	}
	defer file.Close()
	return writeZstd(dest, func(w io.Writer) error {
		if _, err := io.Copy(w, file); err != nil {
			return fmt.Errorf("压缩 %s: %w", src, err)
		}
		return nil
	})
}

// compressTar 把 srcDir 打成 tar 并用 zstd 压缩到 dest。包内顶层目录名为
// baseName，与解压端的目录布局一致。tar 在内存里组装后再压缩，避免把
// 流式写入的错误路径留给解压端。
func compressTar(dest, srcDir, baseName string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := addTree(tw, srcDir, baseName); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("收尾 %s: %w", dest, err)
	}
	return writeZstd(dest, func(w io.Writer) error {
		_, err := w.Write(buf.Bytes())
		return err
	})
}

// addTree 递归写入 srcDir 下的全部条目，顶层名替换为 baseName。
func addTree(tw *tar.Writer, srcDir, baseName string) error {
	return walkDir(srcDir, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// current 来自 srcDir 下的遍历，Rel 不会失败。
		rel, _ := filepath.Rel(srcDir, current)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("不支持符号链接: %s", current)
		}
		name := baseName
		if rel != "." {
			name = path.Join(baseName, filepath.ToSlash(rel))
		}
		// 已排除符号链接，FileInfoHeader 不会失败。
		hdr, _ := tar.FileInfoHeader(info, "")
		hdr.Name = name
		// 归一化元数据，保证同一份输入产出同样的资源。
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		hdr.ModTime = time.Unix(0, 0).UTC()
		hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}
		if info.IsDir() {
			hdr.Name = name + "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
		return nil
	})
}

// writeZstd 用 zstd 编码 fill 写出的内容；先写临时文件再 rename，避免留下
// 半个资源被后续构建嵌入。
func writeZstd(dest string, fill func(io.Writer) error) error {
	dir := filepath.Dir(dest)
	tmp, err := createTemp(dir, ".embedassets-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = removePath(name)
		}
	}()
	// 最高档压缩换发行物体积：构建多花约 5-6s，首启解压解码多约 0.03s。
	// 选项合法，NewWriter 不返回错误。
	enc, _ := zstd.NewWriter(tmp, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err := fill(enc); err != nil {
		_ = enc.Close()
		_ = tmp.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("收尾 %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭 %s: %w", dest, err)
	}
	if err := renamePath(name, dest); err != nil {
		return fmt.Errorf("发布 %s: %w", dest, err)
	}
	committed = true
	return nil
}
