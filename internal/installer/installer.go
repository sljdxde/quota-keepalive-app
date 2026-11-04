// Package installer 实现 QKA 单文件 exe 的安装向导后端：安装状态判定与安装动作。
//
// QKA 的 Windows 发行物是一个自包含的 exe（Bun/adapters/provider-plugins 由
// internal/runtimebundle 内嵌），没有 NSIS/zip，也没有独立的 setup.exe。所以
// 「第一次双击」就是安装：本包负责把当前 exe 复制到用户选定的安装目录、解压
// 内嵌运行时、创建开始菜单快捷方式与 App Paths，最后把结果写进
// <configDir>/install.json。之后再次运行（包括从安装目录启动）看到这条记录就
// 直接进托盘，不再弹安装向导。记录丢失/损坏时不直接断定「没装过」，而是先做
// 一次低成本探测（detect.go：默认目录/App Paths/开始菜单快捷方式），探测到
// 残留安装就按升级/修复处理，避免装出第二份。
//
// 状态记录最后写：任何一步失败都只留下「可重试」的中间状态，不会出现
// 「标记已安装但程序没复制好」的半成品。
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"qka/internal/applog"
	"qka/internal/atomicfile"
	"qka/internal/procquit"
	"qka/internal/runtimebundle"
	"qka/internal/selfinstall"
)

const (
	// StateName 是配置目录下记录安装状态的文件夹名。
	stateName = "install.json"

	// SchemaVersion 是 install.json 的格式号；不认识的格式按「未安装」处理，
	// 由下一次安装重新建立。
	SchemaVersion = 1

	// ExeName 是安装目录里的可执行文件名。App Paths 与开始菜单捷径都按它
	// 查找，所以用固定的 QKA.exe，而不是发行物下载后的文件名。
	ExeName = "QKA.exe"

	// defaultDirName 是默认安装目录的最后一级。
	defaultDirName = "Quota Keepalive"
)

// Step 是安装进度的步骤；页面按顺序点亮这五个步骤。
type Step string

const (
	// StepCheck 正在检查安装位置。
	StepCheck Step = "check"
	// StepCopy 正在复制程序。
	StepCopy Step = "copy"
	// StepRuntime 正在解压内嵌运行时。
	StepRuntime Step = "runtime"
	// StepShortcut 正在创建开始菜单快捷方式。
	StepShortcut Step = "shortcut"
	// StepFinish 安装完成。
	StepFinish Step = "finish"
	// StepFailed 安装失败，detail 是中文错误信息。
	StepFailed Step = "failed"
)

// Progress 接收安装进度。detail 是给用户看的中文状态文字；失败时 step 为
// StepFailed。回调在安装 goroutine 上同步执行，实现方不要阻塞。
type Progress func(step Step, detail string)

// Options 是一次安装的输入。
type Options struct {
	// ConfigDir 是配置目录：install.json 与解压出来的运行时都放在这里。
	ConfigDir string
	// Version 是应用版本（qka/internal/cli.Version），决定运行时解压目录。
	Version string
	// InstallDir 是用户选择的安装目录。
	InstallDir string
	// SelfPath 是当前正在运行的 exe 路径，安装时复制到 InstallDir。
	SelfPath string
	// Progress 可以观察安装进度；nil 表示不关心。
	Progress Progress
}

// Result 是安装成功后的结果。
type Result struct {
	// ConfigDir 是写入 install.json 的配置目录。
	ConfigDir string
	// InstallDir 是清理后的安装目录绝对路径。
	InstallDir string
	// InstalledExe 是安装目录里可执行文件的完整路径。
	InstalledExe string
	// LaunchArgs 是启动已安装程序时的参数；当前为空，即无参数启动直接进托盘。
	LaunchArgs []string
}

// Record 是 <configDir>/install.json 的内容。
type Record struct {
	// Schema 是格式号，当前为 SchemaVersion。
	Schema int `json:"schema"`
	// Version 是安装时的应用版本。
	Version string `json:"version"`
	// InstallDir 是安装目录。
	InstallDir string `json:"install_dir"`
	// ExePath 是安装目录里可执行文件的完整路径。
	ExePath string `json:"exe_path"`
	// InstalledAt 是安装时间，UTC RFC3339。
	InstalledAt string `json:"installed_at"`
}

// 运行环境接缝：测试替换以覆盖平台分支与各类 I/O 失败。
var (
	// lookupEnv 读取环境变量（Windows 上用它取 %LOCALAPPDATA% 推导默认目录）。
	lookupEnv = os.Getenv
	// now 是安装时间接缝，测试固定时间以断言 RFC3339 格式。
	now = time.Now

	// ensureRuntime 解压内嵌运行时；测试注入假实现以覆盖失败路径。
	ensureRuntime = runtimebundle.Ensure
	// ensureSelfInstall 创建开始菜单快捷方式与 App Paths；测试注入假实现。
	ensureSelfInstall = selfinstall.Ensure
	// writeStateFile 原子写 install.json；测试注入失败以验证不写标记。
	writeStateFile = func(path string, data []byte) error {
		return atomicfile.WriteFile(path, data, 0o600)
	}

	// openFile / createTempFile 是复制程序的 I/O 接缝：复制是安装里最容易
	// 失败的步骤（磁盘满、目标被占用），打开与建临时文件的失败分支要能测；
	// 其余失败分支由「目标不可写/源不是文件」等真实情形覆盖。
	// requestQuit 让安装目录里正在运行的旧版本退出（命名事件 + 按路径结束
	// 进程的兜底），返回被结束的进程数。测试注入假实现覆盖「旧版还在跑」。
	requestQuit = func(exePath string) (int, error) {
		asked := 0
		if procquit.Signal() {
			asked = 1
		}
		// 先给它一点时间正常退出：旧版本要关数据库、结束适配器子进程，
		// 直接强杀有丢数据/丢设置的风险。等到文件可写就说明它已经走了。
		if asked > 0 {
			deadline := now().Add(gracefulQuitWait)
			for now().Before(deadline) {
				if checkReplaceable(exePath) == nil {
					return asked, nil
				}
				time.Sleep(replaceProbeInterval)
			}
		}
		killed, err := procquit.TerminateByImage(exePath)
		return asked + len(killed), err
	}

	openFile       = os.Open
	createTempFile = func(dir, pattern string) (copySink, error) {
		return os.CreateTemp(dir, pattern)
	}
)

// copySink 是复制目标临时文件的写入端；抽成接口是为了覆盖写入、同步、关闭
// 失败的分支（*os.File 是它的实现）。
type copySink interface {
	Name() string
	Chmod(fs.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// DefaultDir 自动检测默认安装目录，按以下优先级：
//
//  1. 已有安装记录、且记录里的目录仍然存在 → 直接沿用（升级/重装都装回原处）；
//  2. %LOCALAPPDATA%\Programs\Quota Keepalive 已经存在 → 沿用它；
//  3. %LOCALAPPDATA%\Programs 下的 Quota Keepalive（先构造出来，安装时创建）；
//  4. 拿不到 %LOCALAPPDATA%（极端环境）→ 退回当前 exe 所在目录。
//
// 页面加载时用这个结果预填输入框，用户不动就能装；浏览对话框的初始位置也基于它。
func DefaultDir(configDir, selfPath string) string {
	record, hasRecord := Load(configDir)
	return resolveDefaultDir(
		record,
		hasRecord,
		dirExists,
		strings.TrimSpace(lookupEnv("LOCALAPPDATA")),
		strings.TrimSpace(selfPath),
	)
}

// resolveDefaultDir 是 DefaultDir 的纯逻辑：exists 是目录存在判定（测试注入），
// localAppData 是 %LOCALAPPDATA% 的值，selfPath 是当前 exe 的路径。抽成纯函数
// 是为了在 mac 上覆盖全部回退层级。
func resolveDefaultDir(record Record, hasRecord bool, exists func(string) bool, localAppData, selfPath string) string {
	if hasRecord && strings.TrimSpace(record.InstallDir) != "" {
		dir := filepath.Clean(record.InstallDir)
		if exists(dir) {
			return dir
		}
	}
	if localAppData != "" {
		standard := filepath.Join(localAppData, "Programs", defaultDirName)
		if exists(standard) {
			return standard
		}
		// 目录还没建不影响：安装向导会连父级一起创建。
		return standard
	}
	if selfPath != "" {
		return filepath.Dir(filepath.Clean(selfPath))
	}
	return ""
}

// dirExistsFile 报告 path 是否存在且是一个普通文件。
func dirExistsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// dirExists 报告 path 是否存在且是一个目录。
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Mode 是一次启动应进入的安装模式。
type Mode int

const (
	// ModeNone 表示不需要安装向导：当前 exe 就是已安装的那份，直接进托盘。
	ModeNone Mode = iota
	// ModeInstall 表示这台机器还没有安装记录，需要走首次安装向导。
	ModeInstall
	// ModeUpgrade 表示已有安装记录，但当前运行的 exe 不是安装目录里的那个：
	// 用户双击了下载目录里的新版 exe，应走升级向导替换已安装的程序。
	ModeUpgrade
)

// String 返回稳定的模式名，用于日志与前端展示。
func (m Mode) String() string {
	switch m {
	case ModeInstall:
		return "install"
	case ModeUpgrade:
		return "upgrade"
	default:
		return "none"
	}
}

// runtimeStepError 给「解压运行时」失败补诊断。最常见的原因是构建时漏了
// embeddedruntime 标签（GUI 版但没嵌 Bun/adapters/plugins）：那种 exe 无论
// 重试多少次都装不上，错误原文却只有一句技术描述，用户无从下手。
func runtimeStepError(err error) error {
	if errors.Is(err, runtimebundle.ErrUnavailable) {
		return fmt.Errorf("解压运行时失败：%w；这个 exe 没有内嵌运行时，"+
			"请用 make build-windows 或 scripts/build-windows-exe.sh 重新构建", err)
	}
	return fmt.Errorf("解压运行时失败：%w", err)
}

// Plan 判定这次启动应进入哪种模式：install.json 缺失或损坏时不能直接断定
// 「没装过」（issue #8：用户手删配置目录/文件写坏/跨机器拷贝）——再做一次
// 低成本探测（默认目录/App Paths/开始菜单快捷方式，见 detect.go），探测到
// 残留安装就按升级/修复处理，把安装目录锁定在探测到的位置，避免装出第二份；
// 全部未命中才是首次安装。记录可读时：记录里的 exe 正是当前进程 → 不需要
// 向导（托盘）；否则（用户双击了下载目录里的另一个副本）→ 升级。
//
// selfPath 为空时不按「已安装」处理：无法证明当前就是安装目录里的那份，宁可让
// 用户确认一次升级，也不要把新下载的 exe 当成第二个独立应用跑起来。
func Plan(configDir, selfPath string) Mode {
	return PlanWith(configDir, selfPath, DetectExistingInstall)
}

// PlanWith 是 Plan 的可注入版本：detect 负责「记录缺失时探测既有安装」，
// 传 nil 表示不做探测（等同「探测未命中」）。GUI 向导把检测接缝透传进来，
// 单测因此不必依赖真机是否恰好装过 QKA。
func PlanWith(configDir, selfPath string, detect func() string) Mode {
	record, ok := Load(configDir)
	if !ok {
		detected := ""
		if detect != nil {
			detected = detect()
		}
		if detected == "" {
			return ModeInstall
		}
		// 探测到的就是正在运行的这份：说明用户双击的是已安装副本、只是安装
		// 记录丢了（手删配置目录/文件写坏）。此时不需要向导——运行时早已解压
		// 好，直接进托盘即可，否则每次启动都要弹一次「升级」。
		if strings.TrimSpace(selfPath) != "" && samePath(selfPath, detected) {
			return ModeNone
		}
		return ModeUpgrade
	}
	if strings.TrimSpace(selfPath) == "" {
		return ModeUpgrade
	}
	if samePath(selfPath, record.ExePath) {
		return ModeNone
	}
	return ModeUpgrade
}

// NeedsInstall 报告配置目录下是否需要先跑安装向导：install.json 不存在、无法
// 解析、格式号不认识或必填字段缺失时都返回 true。
func NeedsInstall(configDir string) bool {
	_, ok := Load(configDir)
	return !ok
}

// Load 读取安装记录。文件缺失、内容损坏、格式号不认识或必填字段为空时返回
// ok=false，而不是让调用方去区分各种损坏形态——损坏的记录等同于没装过。
func Load(configDir string) (Record, bool) {
	if strings.TrimSpace(configDir) == "" {
		return Record{}, false
	}
	data, err := os.ReadFile(filepath.Join(configDir, stateName))
	if err != nil {
		return Record{}, false
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, false
	}
	if record.Schema != SchemaVersion {
		return Record{}, false
	}
	if strings.TrimSpace(record.InstallDir) == "" || strings.TrimSpace(record.ExePath) == "" {
		return Record{}, false
	}
	return record, true
}

// Install 执行一次安装。步骤固定为：检查安装位置 → 复制程序 → 解压运行时 →
// 创建快捷方式 → 写安装记录。install.json 最后写，所以任何一步失败都只留下
// 可重试的中间状态（已复制的程序保留，但不会被当成「已安装」）。
func Install(ctx context.Context, opts Options) (Result, error) {
	report := func(step Step, detail string) {
		if opts.Progress != nil {
			opts.Progress(step, detail)
		}
	}
	fail := func(err error) (Result, error) {
		report(StepFailed, err.Error())
		return Result{}, err
	}

	report(StepCheck, "正在检查安装位置…")
	if err := ctx.Err(); err != nil {
		return fail(fmt.Errorf("安装已取消：%w", err))
	}
	if strings.TrimSpace(opts.SelfPath) == "" {
		return fail(errors.New("无法定位当前程序路径，不能开始安装"))
	}
	installDir, err := prepareInstallDir(opts.InstallDir)
	if err != nil {
		return fail(err)
	}

	report(StepCopy, "正在复制程序…")
	installedExe := filepath.Join(installDir, ExeName)
	// 安装目录里可能已经有一份**正在运行**的旧程序（例如用户删掉了
	// install.json 后重装）：Windows 不允许覆盖运行中的 exe，先请它退出。
	if !samePath(opts.SelfPath, installedExe) && dirExistsFile(installedExe) {
		report(StepCopy, "正在退出该目录里正在运行的 QKA…")
		if _, err := requestQuit(installedExe); err != nil {
			applog.Warnf("installer", "请旧版本退出失败：%v", err)
		}
		if err := waitReplaceable(installedExe); err != nil {
			return fail(err)
		}
	}
	if err := copyExecutable(opts.SelfPath, installedExe); err != nil {
		return fail(fmt.Errorf("复制程序失败：%w", err))
	}

	report(StepRuntime, "正在解压运行时…")
	if _, err := ensureRuntime(opts.ConfigDir, opts.Version); err != nil {
		return fail(runtimeStepError(err))
	}

	report(StepShortcut, "正在创建开始菜单快捷方式…")
	if err := ensureSelfInstall(opts.ConfigDir, installedExe, opts.Version); err != nil {
		return fail(fmt.Errorf("创建快捷方式失败：%w", err))
	}

	record := Record{
		Schema:      SchemaVersion,
		Version:     opts.Version,
		InstallDir:  installDir,
		ExePath:     installedExe,
		InstalledAt: now().UTC().Format(time.RFC3339),
	}
	if err := writeRecord(opts.ConfigDir, record); err != nil {
		return fail(fmt.Errorf("写入安装记录失败：%w", err))
	}

	report(StepFinish, "安装完成")
	return Result{
		ConfigDir:    opts.ConfigDir,
		InstallDir:   installDir,
		InstalledExe: installedExe,
	}, nil
}

// Upgrade 把已安装的程序替换成当前 exe——用户在下载目录双击新版 exe 的场景。
// 安装目录固定为 install.json 里记录的目录，不提供修改；记录丢失/损坏但探测
// 到残留安装（issue #8）时锁定在探测到的目录，就地修复。步骤与 Install 相同，
// 但「复制」换成「替换」：先确认旧版没在运行，再用临时文件+rename 换掉
// <InstallDir>\QKA.exe，最后更新 install.json 的版本。
//
// 失败时保留旧记录，下次双击仍会进入升级向导，可以重试；旧版程序只会被整体
// rename 覆盖，不会出现半个文件。
func Upgrade(ctx context.Context, opts Options) (Result, error) {
	report := func(step Step, detail string) {
		if opts.Progress != nil {
			opts.Progress(step, detail)
		}
	}
	fail := func(err error) (Result, error) {
		report(StepFailed, err.Error())
		return Result{}, err
	}

	report(StepCheck, "正在检查已安装版本…")
	if err := ctx.Err(); err != nil {
		return fail(fmt.Errorf("升级已取消：%w", err))
	}
	record, ok := Load(opts.ConfigDir)
	if !ok {
		// install.json 丢了不代表程序没了（用户手删配置目录/文件写坏）：按
		// Plan 的同一套探测找残留安装，找到就锁定探测到的目录就地修复，
		// 等同一次升级；探测不到才是真的没装过。
		detected := detectExistingInstall()
		if detected == "" {
			return fail(errors.New("没有找到已安装版本的记录，无法升级；请重新运行安装向导"))
		}
		record = Record{InstallDir: filepath.Dir(detected), ExePath: detected}
	}
	if strings.TrimSpace(opts.SelfPath) == "" {
		return fail(errors.New("无法定位当前程序路径，不能开始升级"))
	}
	installDir, err := prepareInstallDir(record.InstallDir)
	if err != nil {
		return fail(err)
	}
	installedExe := filepath.Join(installDir, ExeName)

	report(StepCopy, "正在替换程序…")
	// 当前 exe 就是已安装的那份（记录被手改或丢失后重跑）：原地刷新，不替换自己。
	if !samePath(opts.SelfPath, installedExe) {
		// 旧版可能还开着（用户升级前不会先手动退出）。Windows 不允许覆盖运行中
		// 的 exe，所以这里主动请它退出：先发命名事件让新版自己收尾退出，再对
		// 没实现该通道的旧构建按可执行文件路径结束进程，最后才检查可替换性。
		report(StepCopy, "正在退出正在运行的旧版本…")
		if _, err := requestQuit(installedExe); err != nil {
			applog.Warnf("installer", "请旧版本退出失败：%v", err)
		}
		if err := waitReplaceable(installedExe); err != nil {
			return fail(err)
		}
		if err := copyExecutable(opts.SelfPath, installedExe); err != nil {
			return fail(fmt.Errorf("替换程序失败：%w", err))
		}
	}

	report(StepRuntime, "正在解压运行时…")
	if _, err := ensureRuntime(opts.ConfigDir, opts.Version); err != nil {
		return fail(runtimeStepError(err))
	}

	report(StepShortcut, "正在刷新开始菜单快捷方式…")
	if err := ensureSelfInstall(opts.ConfigDir, installedExe, opts.Version); err != nil {
		return fail(fmt.Errorf("刷新快捷方式失败：%w", err))
	}

	next := Record{
		Schema:      SchemaVersion,
		Version:     opts.Version,
		InstallDir:  installDir,
		ExePath:     installedExe,
		InstalledAt: now().UTC().Format(time.RFC3339),
	}
	// 版本号最后写：替换/解压/快捷方式任何一步失败，旧记录都还在，双击仍会
	// 进入升级向导而不是被当成「已是最新」。
	if err := writeRecord(opts.ConfigDir, next); err != nil {
		return fail(fmt.Errorf("更新安装记录失败：%w", err))
	}

	report(StepFinish, "升级完成")
	return Result{
		ConfigDir:    opts.ConfigDir,
		InstallDir:   installDir,
		InstalledExe: installedExe,
	}, nil
}

// replaceProbeInterval / replaceProbeTimeout 是等待旧版本释放文件的重试节奏：
// 进程被结束或收到退出请求后，文件句柄由系统回收需要一点时间，直接写会偶发
// 「拒绝访问」。
var (
	replaceProbeInterval = 100 * time.Millisecond
	replaceProbeTimeout  = 5 * time.Second
	// gracefulQuitWait 是「请旧版本退出」之后等它自己收尾的上限：超过才强杀。
	gracefulQuitWait = 2 * time.Second
)

// waitReplaceable 等待已安装的程序变成可替换（旧版本进程已退出、文件句柄已
// 释放）。安装向导已经主动请旧版退出，这里只是给系统一点回收时间；超时仍
// 拿不到写句柄时才报错，并说明可以手动退出后重试。
func waitReplaceable(path string) error {
	deadline := now().Add(replaceProbeTimeout)
	var lastErr error
	for {
		err := checkReplaceable(path)
		if err == nil {
			return nil
		}
		lastErr = err
		if now().After(deadline) {
			return lastErr
		}
		time.Sleep(replaceProbeInterval)
	}
}

// checkReplaceable 预检已安装的程序能否被替换。运行中的 Windows 程序（镜像
// 文件）不能被写方式打开，拿不到写句柄就说明旧版还开着——提前给「先退出 QKA」
// 的明确提示，而不是让用户只看到 rename 的「拒绝访问」。
func checkReplaceable(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil // 安装目录/程序被删过：按重建处理，直接写即可
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return errors.New("无法替换已安装的程序（旧版本仍在运行且未能自动退出）；请手动退出 QKA（托盘图标 → 退出）后点「重试」")
	}
	// 探针文件只用来验证能否拿到写句柄；关闭失败不影响替换判断。
	_ = file.Close()
	return nil
}

// prepareInstallDir 校验并创建安装目录，返回清理后的绝对路径。
func prepareInstallDir(raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		return "", errors.New("安装位置不能为空")
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("安装位置必须是绝对路径")
	}
	dir = filepath.Clean(dir)
	if isVolumeRoot(dir) {
		return "", errors.New("不能把 QKA 安装到磁盘根目录，请选择一个子目录")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("无法创建安装目录 %s：%w", dir, err)
	}
	if err := probeWritable(dir); err != nil {
		return "", fmt.Errorf("安装目录不可写 %s：%w", dir, err)
	}
	return dir, nil
}

// isVolumeRoot 判定路径是否为盘符/卷根（C:\、\\server\share\、/）。把程序直接
// 铺在根目录既不整洁，系统盘根目录普通用户也没有写权限。
func isVolumeRoot(dir string) bool {
	volume := filepath.VolumeName(dir)
	rest := strings.TrimPrefix(dir, volume)
	rest = strings.Trim(rest, string(filepath.Separator))
	return rest == ""
}

// probeWritable 在目录里建一个临时文件再删掉，确认真的可写：MkdirAll 成功
// 不代表能写（只读挂载、ACL 拒绝都很常见）。
func probeWritable(dir string) error {
	probe, err := createTempFile(dir, ".qka-write-*.tmp")
	if err != nil {
		return err
	}
	name := probe.Name()
	closeErr := probe.Close()
	_ = os.Remove(name)
	return closeErr
}

// copyExecutable 把当前 exe 复制到安装目录：先写同目录临时文件，成功后再
// rename 覆盖目标，崩溃不会留下半个可执行文件。源与目标同一文件时直接跳过
// （用户从安装目录启动、而记录丢失后的重装场景）。
func copyExecutable(src, dst string) error {
	if samePath(src, dst) {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("读取当前程序 %s：%w", src, err)
	}
	if info.IsDir() {
		return fmt.Errorf("当前程序路径不是文件：%s", src)
	}
	in, err := openFile(src)
	if err != nil {
		return fmt.Errorf("打开当前程序 %s：%w", src, err)
	}
	defer func() { _ = in.Close() }()

	tmp, err := createTempFile(filepath.Dir(dst), ".QKA-*.tmp")
	if err != nil {
		return fmt.Errorf("创建安装目录临时文件：%w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("复制程序数据：%w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步程序数据：%w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置程序权限：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭程序临时文件：%w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("无法写入 %s（目标程序可能正在运行，请先退出 QKA 后重试）：%w", dst, err)
	}
	committed = true
	return nil
}

// writeRecord 原子写 install.json；配置目录可能还不存在（真正的第一次安装），
// 先建出来再写。目录权限收敛为 0700：里面是用户私有状态。
func writeRecord(configDir string, record Record) error {
	if err := atomicfile.EnsureDir(configDir, 0o700); err != nil {
		return err
	}
	// 只有 JSON 原生类型，不会失败。
	data, _ := json.MarshalIndent(record, "", "  ")
	data = append(data, '\n')
	return writeStateFile(filepath.Join(configDir, stateName), data)
}

// samePath 判断两个路径是否同一个。Windows 路径大小写不敏感，末尾分隔符也
// 不影响身份，所以统一 Clean 后 EqualFold。
func samePath(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
