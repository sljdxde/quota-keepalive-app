package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"qka/internal/installer"
)

// installerProgressEvent 是安装向导把进度推给页面的 Wails 事件名。
const installerProgressEvent = "installer-progress"

// InstallerDeps 是安装向导窗口的依赖。安装向导只做本机安装，不装配应用
// 组合根（不启动 HTTP 网关、不加载适配器）。
type InstallerDeps struct {
	// ConfigDir 是配置目录：install.json 与解压出来的运行时都放在这里。
	ConfigDir string
	// Version 是当前应用版本，写进安装记录并决定运行时解压目录。
	Version string
	// SelfPath 是当前正在运行的 exe 路径，安装时复制到安装目录。
	SelfPath string
	// DetectExisting 是「无安装记录时探测既有安装」的接缝；nil 表示用默认
	// 实现（默认目录/App Paths/开始菜单快捷方式）。单测注入空实现，避免
	// 结果取决于真机是否恰好装过 QKA。
	DetectExisting func() string
}

// Installer 是安装向导的 Wails 应用与窗口。
type Installer struct {
	wails   *application.App
	window  *application.WebviewWindow
	service *InstallerService
}

// InstallerProgress 是推送给安装向导页面的进度数据。
type InstallerProgress struct {
	// Step 是当前步骤：check/copy/runtime/shortcut/finish/failed。
	Step string `json:"step"`
	// Detail 是给用户看的中文状态文字。
	Detail string `json:"detail"`
}

// InstallerStatus 是安装向导启动时的模式信息，页面加载后先取它来决定文案与
// 是否显示「安装位置」输入框。
type InstallerStatus struct {
	// Mode 是向导模式：install（首次安装）或 upgrade（已有安装，替换程序）。
	Mode string `json:"mode"`
	// Version 是本次要安装/升级到的版本。
	Version string `json:"version"`
	// InstalledVersion 是升级模式下的旧版本（install 模式为空）。
	InstalledVersion string `json:"installed_version,omitempty"`
	// InstallDir 是升级模式下已安装的目录（install 模式为空，页面用默认目录）。
	InstallDir string `json:"install_dir,omitempty"`
}

// InstallerService 是暴露给安装向导页面的服务。
type InstallerService struct {
	configDir string
	version   string
	selfPath  string

	status     InstallerStatus
	install    func(context.Context, installer.Options) (installer.Result, error)
	upgrade    func(context.Context, installer.Options) (installer.Result, error)
	defaultDir func() string
	browse     func(current string) (string, error)
	launch     func(exe string, args []string) error
	quit       func()
	emit       func(name string, data any)

	mu           sync.Mutex
	running      bool
	installedExe string
}

// resolveStartDir 从 current 沿目录树向上找最近的已存在目录，作为系统目录选择
// 框的初始位置。安装向导的默认目录（%LOCALAPPDATA%\Programs\Quota Keepalive）
// 在安装前通常还不存在，而 Windows 的通用文件对话框在初始目录不存在时会在创建
// 阶段就失败（cfd.DialogConfig.apply 里 os.Stat 不通过），所以不能把不存在的
// 路径交给它。返回空串表示找不到任何存在的目录，调用方应完全不设初始目录。
//
// exists 是判定函数：生产传 dirExists，测试传假实现，便于在 mac 上覆盖分支。
func resolveStartDir(current string, exists func(string) bool) string {
	dir := strings.TrimSpace(current)
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for {
		if exists(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// dirExists 报告 path 是否存在且是一个目录（文件不算可用的初始目录）。
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// launchInstalled 启动刚安装好的 QKA。Windows 上它是 windowsgui 程序，没有
// 控制台窗口，不需要也不应该隐藏窗口；Start 启动后立即返回，安装向导随即退出。
var launchInstalled = func(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	return cmd.Start()
}

// NewInstaller 创建安装向导的 Wails 应用：一个约 560x430 的窗口，加载
// /installer.html，窗口在事件循环启动后才显示。
func NewInstaller(deps InstallerDeps) *Installer {
	// Windows 上 WebView2 运行时缺失时 Wails 会静默退出，安装向导同样是窗口，
	// 所以和主应用一样先做守卫：缺失时弹原生提示并以非 0 退出码结束。
	ensureWebView2Runtime()

	service := newInstallerService(deps)
	wails := application.New(application.Options{
		Name: "Quota Keepalive 安装程序",
		Icon: applicationIcon(256),
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: assetHandler(),
		},
		// 关闭向导窗口（放弃安装）时结束进程：Windows 默认就是「最后一个窗口
		// 关闭即退出」，macOS 上显式打开同样的行为，避免开发时留下无窗口进程。
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	service.quit = func() { wails.Quit() }
	service.emit = func(name string, data any) { wails.Event.Emit(name, data) }

	window := wails.Window.NewWithOptions(installerWindowOptions())
	if service.status.Mode == installer.ModeUpgrade.String() {
		window.SetTitle("Quota Keepalive 升级程序")
	}
	service.browse = func(current string) (string, error) {
		return browseInstallDir(wails, window, current)
	}

	// 窗口以 Hidden 创建，等事件循环起来再显示，避免启动时先闪一个空白窗口。
	result := &Installer{wails: wails, window: window, service: service}
	wails.Event.OnApplicationEvent(events.Common.ApplicationStarted, result.onApplicationStarted)
	return result
}

// newInstallerService 组装绑定服务；默认实现全部指向真实安装动作，测试直接
// 构造 InstallerService 并替换这些字段来覆盖 UI 分支。
func newInstallerService(deps InstallerDeps) *InstallerService {
	return &InstallerService{
		configDir:  deps.ConfigDir,
		version:    deps.Version,
		selfPath:   deps.SelfPath,
		status:     installerStatusFor(deps),
		install:    installer.Install,
		upgrade:    installer.Upgrade,
		defaultDir: func() string { return installer.DefaultDir(deps.ConfigDir, deps.SelfPath) },
		launch:     launchInstalled,
	}
}

// installerStatusFor 根据安装记录与当前 exe 路径判定向导模式，并把升级展示需要
// 的旧版本与安装目录一并算好（页面加载时通过 Status() 取）。
func installerStatusFor(deps InstallerDeps) InstallerStatus {
	detect := deps.DetectExisting
	if detect == nil {
		detect = installer.DetectExistingInstall
	}
	status := InstallerStatus{
		Mode:    installer.PlanWith(deps.ConfigDir, deps.SelfPath, detect).String(),
		Version: deps.Version,
	}
	if status.Mode != installer.ModeUpgrade.String() {
		return status
	}
	// Plan 返回 upgrade 的前提是记录可读，或探测到了残留安装（install.json 被
	// 用户删掉/写坏，见 installer 的 detect.go）；这里再读一次拿展示字段。
	if record, ok := installer.Load(deps.ConfigDir); ok {
		status.InstalledVersion = record.Version
		status.InstallDir = record.InstallDir
		return status
	}
	// 记录丢失但探测到残留安装：已安装版本号探测不出来，留空由页面显示
	// 「旧版」；安装位置用探测到的目录，向用户讲清楚「将就地升级」。
	if detected := detect(); detected != "" {
		status.InstallDir = filepath.Dir(detected)
	}
	return status
}

// installerWindowOptions 是安装向导窗口的选项：固定尺寸、居中、初始隐藏，
// 由 ApplicationStarted 事件显示。
func installerWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:            "installer",
		Title:           "Quota Keepalive 安装程序",
		Width:           560,
		Height:          430,
		MinWidth:        560,
		MinHeight:       430,
		DisableResize:   true,
		InitialPosition: application.WindowCentered,
		Hidden:          true,
		BackgroundType:  application.BackgroundTypeSolid,
		URL:             "/installer.html",
	}
}

// onApplicationStarted 在事件循环启动后显示安装向导窗口。
func (i *Installer) onApplicationStarted(*application.ApplicationEvent) {
	// 必须异步：ApplicationStarted 在窗口消息处理器里回调，而 Show/Focus 内部是
	// InvokeSync + SendMessage 向同一线程同步发消息，直接调用会自锁（安装向导
	// 会停在白屏/黑屏并显示「未响应」）。
	go func() {
		i.window.Show()
		i.window.Focus()
	}()
}

// Run 启动安装向导事件循环，阻塞到用户完成安装（启动 QKA 后退出）或关闭窗口。
func (i *Installer) Run() error {
	return i.wails.Run()
}

// Status 返回向导模式与升级展示需要的旧版本/路径；页面加载后先调用它。
func (s *InstallerService) Status() InstallerStatus {
	return s.status
}

// DefaultDir 返回默认安装位置（Windows: %LOCALAPPDATA%\Programs\Quota Keepalive）。
func (s *InstallerService) DefaultDir() string {
	if s.defaultDir == nil {
		return ""
	}
	return s.defaultDir()
}

// BrowseDir 弹出原生目录选择框；用户取消时返回空串，页面保持原输入。
func (s *InstallerService) BrowseDir(current string) (string, error) {
	if s.browse == nil {
		return "", errors.New("当前环境不支持目录选择，请直接输入安装路径")
	}
	return s.browse(current)
}

// StartInstall 在后台开始安装或升级并立即返回；进度通过 installer-progress
// 事件推给页面，页面据此点亮步骤。升级模式下 dir 被忽略（目录锁定在安装记录
// 里，不允许用户改）。
func (s *InstallerService) StartInstall(dir string) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("安装正在进行中，请稍候")
	}
	s.running = true
	s.mu.Unlock()
	go s.runInstall(dir)
	return nil
}

// runInstall 按模式执行首次安装或升级。失败时 installer 的 Install/Upgrade 已经
// 通过 Progress 推送了 StepFailed，这里不再重复推事件。
func (s *InstallerService) runInstall(dir string) {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	var result installer.Result
	var err error
	if s.status.Mode == installer.ModeUpgrade.String() {
		// 升级：安装目录固定为记录里的目录，页面传来的 dir 不参与。
		result, err = s.upgrade(context.Background(), installer.Options{
			ConfigDir: s.configDir,
			Version:   s.version,
			SelfPath:  s.selfPath,
			Progress:  s.report,
		})
	} else {
		result, err = s.install(context.Background(), installer.Options{
			ConfigDir:  s.configDir,
			Version:    s.version,
			InstallDir: dir,
			SelfPath:   s.selfPath,
			Progress:   s.report,
		})
	}
	if err != nil {
		return
	}
	s.mu.Lock()
	s.installedExe = result.InstalledExe
	s.mu.Unlock()
}

// report 把安装进度翻译成页面事件。
func (s *InstallerService) report(step installer.Step, detail string) {
	if s.emit == nil {
		return
	}
	s.emit(installerProgressEvent, InstallerProgress{Step: string(step), Detail: detail})
}

// QuitInstaller 关闭安装向导并结束进程。
func (s *InstallerService) QuitInstaller() {
	if s.quit == nil {
		return
	}
	s.quit()
}

// LaunchInstalled 启动已安装的 QKA，然后退出安装向导。
func (s *InstallerService) LaunchInstalled() error {
	s.mu.Lock()
	exe := s.installedExe
	s.mu.Unlock()
	if exe == "" {
		return errors.New("还没有完成安装，无法启动 QKA")
	}
	if s.launch == nil {
		return errors.New("当前环境不支持启动已安装的程序")
	}
	if err := s.launch(exe, nil); err != nil {
		return fmt.Errorf("启动已安装的 QKA 失败：%w", err)
	}
	s.QuitInstaller()
	return nil
}
