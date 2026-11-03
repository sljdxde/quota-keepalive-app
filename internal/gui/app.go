// Package gui 组装 QKA 的菜单栏托盘、面板与主窗口（Wails v3）。
package gui

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"qka/internal/applog"
	"qka/internal/procquit"
	"qka/internal/status"
	"qka/internal/tokenspeed"
)

//go:embed frontend
var frontendFS embed.FS

// App 持有 Wails 应用与运行期窗口。
type App struct {
	wails      *application.App
	tray       *application.SystemTray
	panel      *application.WebviewWindow
	bar        *application.WebviewWindow
	main       *application.WebviewWindow
	theme      string
	tokenSpeed *tokenspeed.Service
	status     func(context.Context) (status.View, error)
	windowMu   sync.Mutex
	startedAt  atomic.Int64
	dockShow   atomic.Bool
	failureOn  atomic.Bool
	dockApply  func(bool)
	quitStop   func()
}

const panelGap = 8

// 常驻刘海（Windows 菜单栏状态项）的尺寸：只放 Q 图标和 tok/s，按内容取固定
// 宽高，避免窗口大小随速度数字变化而跳动。
const (
	barWidth        = 118
	barHeight       = 34
	barCornerRadius = 15
	// barTopGap 是胶囊与屏幕工作区顶边的间距：留一点缝隙，观感上像「吸附在
	// 顶部」而不是贴死在边缘。
	barTopGap = 6
)

// notchPanelGap 是刘海下方面板与顶栏底边之间的间距（像素），与 panelGap 一致
// 的视觉留白。
const notchPanelGap = 6

func assetHandler() http.Handler {
	sub, _ := fs.Sub(frontendFS, "frontend")
	return http.FileServer(http.FS(sub))
}

// activationPolicy 把 show_dock_icon 设置映射为 macOS 激活策略：
// 开启显示 Dock 图标（regular），关闭后仅在菜单栏驻留（accessory）。
func activationPolicy(showDockIcon bool) application.ActivationPolicy {
	if showDockIcon {
		return application.ActivationPolicyRegular
	}
	return application.ActivationPolicyAccessory
}

// New 创建 Wails 应用：根据 show_dock_icon 选择激活策略（默认 regular 显示 Dock 图标），
// 组装托盘、面板与五页签主窗口。
func New(deps Deps) *App {
	// Windows 上 WebView2 运行时缺失时 Wails 会 fatal 后 os.Exit(1)，而 windowsgui
	// 程序没有可见的 stderr，用户只看到「双击毫无反应」。所以必须在 application.New
	// 之前检测：缺失就弹 Windows 原生提示并给出下载地址，再以非 0 退出码结束。
	// 非 Windows 平台是空操作。
	ensureWebView2Runtime()

	// Windows 上先声明应用身份（AppUserModelID）：任务栏才会显示图标、提供
	// 「固定到任务栏」，通知也能正确归类。必须早于任何窗口创建。
	setProcessAppUserModelID()

	showDock := true
	theme := "system"
	if deps.Settings != nil {
		current := deps.Settings()
		showDock = current.Get("show_dock_icon")
		theme = current.GetString("theme")
	}

	dockApply := deps.DockIconVisible
	if dockApply == nil {
		dockApply = setDockIconVisible
	}
	result := &App{
		theme: theme, tokenSpeed: deps.TokenSpeed,
		status: deps.Status, dockApply: dockApply,
	}
	result.dockShow.Store(showDock)
	deps.DockIconVisible = result.setDockPreference
	bound := NewBoundService(deps)

	wails := application.New(application.Options{
		Name: "Quota Keepalive",
		Icon: applicationIcon(256),
		// Wails 内部的 warning/error 也落盘：非 debug 构建默认把内部日志丢弃，
		// windowsgui 没有控制台，出问题时用户拿不到任何线索。未 Init 日志时
		// 这里是丢弃日志器，与旧行为一致。
		Logger: applog.Module("wails"),
		Services: []application.Service{
			application.NewService(bound),
		},
		Assets: application.AssetOptions{
			Handler: assetHandler(),
		},
		Mac: application.MacOptions{
			ActivationPolicy: activationPolicy(showDock),
		},
	})

	result.wails = wails
	currentApp.Store(result)
	// 重复双击 exe 时由已有实例的代持者唤醒主窗口（Windows）；其它平台 no-op。
	setInstanceWakeHandler(result.showMain)

	// 升级向导会先请旧版退出（命名事件），这里收到就正常收尾退出，让新版能
	// 覆盖安装目录里的 exe。没有这条通道的旧构建由安装向导按路径结束进程。
	quitStop := procquit.Listen(func() {
		applog.Infof("gui", "收到退出请求（安装/升级向导），正在退出")
		wails.Quit()
	})
	result.quitStop = quitStop

	// 菜单栏面板的设置入口统一回到主窗口的第五页签。
	wails.Event.On("open-settings", func(*application.CustomEvent) {
		go result.showMain()
	})
	wails.Event.On("open-checkin", func(*application.CustomEvent) {
		go result.showMain()
	})

	// 面板「打开主窗口」入口。
	wails.Event.On("open-main", func(*application.CustomEvent) {
		go result.showMain()
	})
	wails.Event.On("quit", func(*application.CustomEvent) {
		go wails.Quit()
	})
	wails.Event.On("collapse-panel", func(*application.CustomEvent) {
		go result.hidePanel()
	})

	// 测试钩子：QKA_DEBUG_SHOW=main/settings/both 时，事件循环启动后显示对应窗口（不影响正式行为）。
	if debugShow := os.Getenv("QKA_DEBUG_SHOW"); debugShow != "" {
		go func() {
			time.Sleep(750 * time.Millisecond)
			switch debugShow {
			case "panel":
				result.showPanel()
			case "main":
				result.showMain()
			case "settings":
				result.showMain()
				wails.Event.Emit("open-settings")
			case "both":
				result.showMain()
			}
		}()
	}

	// 托盘菜单。所有回调都在托盘窗口的消息处理器里执行，必须异步再操作窗口：
	// Show/Focus/Position 内部是 InvokeSync + SendMessage，在消息处理器里同步
	// 调用会自锁（表现为「点了没反应」「未响应」）。
	menu := application.NewMenu()
	menu.Add("打开主窗口").OnClick(func(*application.Context) {
		go result.showMain()
	})
	menu.Add("设置").OnClick(func(*application.Context) {
		go func() {
			result.showMain()
			wails.Event.Emit("open-settings")
		}()
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		go wails.Quit()
	})

	// 托盘：平台对应的图标、吸附面板（点击切换、失焦即隐）、菜单。
	tray := wails.SystemTray.New()
	applyTrayIcon(tray, goos)
	tray.SetTooltip("Quota Keepalive")
	tray.SetLabel("0 tok/s")
	// 单击托盘：异步切换面板（原因同上，不能在托盘消息处理器里同步操作窗口）。
	tray.OnClick(func() { go result.togglePanel() })
	tray.SetMenu(menu)

	result.tray = tray
	// 常驻刘海（Windows 菜单栏状态项）在事件循环启动后由
	// installPlatformWindowBehavior 创建：此时窗口系统才真正就绪，位置和
	// 圆角区域才设得上。
	result.installPlatformWindowBehavior()
	wails.Event.OnApplicationEvent(events.Common.ApplicationStarted, result.onApplicationStarted)
	return result
}

// ensureBar 创建常驻刘海窗口并把它钉在主屏左上角。非 Windows 平台是 no-op：
// macOS 的菜单栏状态项由系统承载。
func (a *App) ensureBar() {
	if goos != "windows" {
		return
	}
	a.windowMu.Lock()
	if a.bar != nil {
		bar := a.bar
		a.windowMu.Unlock()
		_ = bar
		return
	}
	bar := a.wails.Window.NewWithOptions(barWindowOptions())
	a.bar = bar
	a.windowMu.Unlock()

	// 以下都涉及主线程窗口操作（InvokeSync），必须在放锁之后执行：
	// 持着 windowMu 等主线程，而主线程可能正在等 windowMu，会互等死锁。
	screen := a.wails.Screen.GetPrimary()
	x, y := barOrigin(screen)
	bar.SetPosition(x, y)
	// 刘海必须常驻：Alt+F4 之类触发关闭时取消掉，不让窗口被销毁。
	bar.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
	})
	// DPI 变化（拖到不同缩放比例的显示器）后窗口物理尺寸会变，圆角区域是按
	// 物理像素算的，必须重新裁一次，否则胶囊会被切掉一块。
	bar.OnWindowEvent(events.Common.WindowDPIChanged, func(*application.WindowEvent) {
		shapeBarWindow(bar)
	})
	bar.Show()
	// 圆角必须在窗口真正创建（Show）之后设置：NativeWindow 之前是空的。
	shapeBarWindow(bar)
}

// showBar 确保刘海可见（主窗口关闭、退出面板等场景后仍要常驻）。
func (a *App) showBar() {
	if goos != "windows" {
		return
	}
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		a.ensureBar()
		return
	}
	if !bar.IsVisible() {
		bar.Show()
	}
}

// barAnchor 返回刘海在屏幕上的位置与尺寸，用于把面板贴在刘海正下方。
func (a *App) barAnchor() (x, y, height int) {
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		return 0, 0, barHeight
	}
	px, py := bar.Position()
	width, barHeightNow := bar.Size()
	_ = width
	return px, py, barHeightNow
}

// BarToggle 实现刘海左键点击：在刘海正下方切换吸附面板（等价于 macOS 点
// 菜单栏图标）。
func (a *App) BarToggle() { a.togglePanelAtNotch(0, 0) }

// BarMenu 实现刘海右键点击：在光标处弹出与托盘一致的菜单（打开主窗口 /
// 设置 / 退出），这样用户不依赖托盘图标也能关掉 QKA。
func (a *App) BarMenu() {
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		return
	}
	menu := application.NewContextMenu("qka-bar-menu")
	menu.Add("打开主窗口").OnClick(func(*application.Context) {
		go a.showMain()
	})
	menu.Add("设置").OnClick(func(*application.Context) {
		go func() {
			a.showMain()
			a.wails.Event.Emit("open-settings")
		}()
	})
	menu.AddSeparator()
	menu.Add("退出 Quota Keepalive").OnClick(func(*application.Context) {
		go a.wails.Quit()
	})
	bar.OpenContextMenu(&application.ContextMenuData{Id: "qka-bar-menu"})
}

// PanelToggle 实现 gui.Deps.PanelToggle：切换吸附面板（Windows 刘海点击）。
func (a *App) PanelToggle() { a.togglePanel() }

// PanelToggleAt 实现 gui.Deps.PanelToggleAt：把面板摆在刘海正下方。
func (a *App) PanelToggleAt(x, y int) { a.togglePanelAtNotch(x, y) }

func (a *App) onApplicationStarted(*application.ApplicationEvent) {
	a.startedAt.Store(time.Now().UnixNano())
}

func (a *App) setDockPreference(show bool) {
	a.dockShow.Store(show)
	a.dockApply(show)
}

func (a *App) hideDockForMenuBar() {
	a.dockApply(false)
}

func (a *App) hideRestoredWindows() {
	a.hidePanel()
	a.windowMu.Lock()
	main := a.main
	a.windowMu.Unlock()
	if main != nil {
		main.Hide()
	}
}

func (a *App) ensurePanel() *application.WebviewWindow {
	a.windowMu.Lock()
	defer a.windowMu.Unlock()
	if a.panel != nil {
		return a.panel
	}
	// The panel is created on first use. Pre-creating a hidden macOS window
	// lets AppKit/Wails restore it during an application reopen event.
	a.panel = a.wails.Window.NewWithOptions(panelOptions(a.theme))
	a.tray.AttachWindow(a.panel).WindowOffset(panelGap)
	return a.panel
}

func (a *App) ensureMainWindow() *application.WebviewWindow {
	a.windowMu.Lock()
	defer a.windowMu.Unlock()
	return a.ensureMainWindowLocked()
}

func (a *App) ensureMainWindowLocked() *application.WebviewWindow {
	if a.main != nil && a.mainWindowAlive() {
		return a.main
	}
	// 缓存指针可能已经被平台销毁（例如关闭事件被绕过）：重建而不是复用，
	// 否则 Show() 会对一个已销毁窗口静默失败，用户再也打不开主界面。
	main := a.wails.Window.NewWithOptions(mainOptions(a.theme))
	a.main = main
	main.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		// 关闭主窗口不销毁：QKA 常驻菜单栏/托盘，隐藏后托盘与双击都要能再打开。
		main.Hide()
		// Closing the main window leaves QKA resident in the menu bar, so the
		// Dock icon should disappear until the user opens the main window again.
		a.hideDockForMenuBar()
		event.Cancel()
	})
	return main
}

// mainWindowAlive 报告缓存的主窗口是否仍由 Wails 的窗口管理器持有。
// 关闭被取消时窗口只是隐藏，仍算存活；被真正销毁后管理器会把它移出，
// 此时必须重建。
func (a *App) mainWindowAlive() bool {
	if a.main == nil {
		return false
	}
	live, ok := a.wails.Window.GetByID(a.main.ID())
	return ok && live != nil
}

type panelController interface {
	visible() bool
	show()
	hide()
}

type panelActions struct {
	window *application.WebviewWindow
}

func (p panelActions) visible() bool { return p.window.IsVisible() }
func (p panelActions) show()         { p.window.Show().Focus() }
func (p panelActions) hide()         { p.window.Hide() }

func togglePanelWindow(panel panelController) {
	if panel.visible() {
		panel.hide()
		return
	}
	panel.show()
}

func (a *App) showPanel() {
	panel := a.ensurePanel()
	_ = a.tray.PositionWindow(panel, panelGap)
	panel.Show().Focus()
}

func (a *App) togglePanel() {
	panel := a.ensurePanel()
	_ = a.tray.PositionWindow(panel, panelGap)
	togglePanelWindow(panelActions{window: panel})
}

// togglePanelAtNotch 在顶栏刘海（Windows 菜单栏状态项）下方切换吸附面板。
// 坐标是刘海在窗口内的 DIP 位置，换算成屏幕坐标后把面板摆到刘海正下方，
// 与 macOS 点菜单栏图标时面板贴着菜单栏展开的观感一致。
func (a *App) togglePanelAtNotch(x, y int) {
	panel := a.ensurePanel()
	if panel.IsVisible() {
		panel.Hide()
		return
	}
	// 优先用常驻刘海窗口的实际位置：面板贴在刘海正下方，和 macOS 点菜单栏
	// 图标时面板贴着菜单栏展开一致。刘海窗口不可用时退回页面给的坐标。
	bx, by, bh := a.barAnchor()
	if bh <= 0 {
		bx, by, bh = x, y, barHeight
	}
	panel.SetPosition(bx, by+bh+notchPanelGap)
	panel.Show().Focus()
}

func (a *App) hidePanel() {
	a.windowMu.Lock()
	panel := a.panel
	a.windowMu.Unlock()
	if panel != nil {
		panel.Hide()
	}
}

func (a *App) showMain() {
	// 先隐藏置顶面板，再激活主窗口；顺序反过来会让主窗口短暂位于
	// 非激活 NSPanel 下方。
	a.hidePanel()
	a.dockApply(a.dockShow.Load())
	main := a.ensureMainWindow()
	main.Show()
	main.Focus()
	a.showBar()
}

func mainOptions(theme string) application.WebviewWindowOptions {
	opts := application.WebviewWindowOptions{
		Name:            "main",
		Title:           "Quota Keepalive",
		Width:           1240,
		Height:          820,
		MinWidth:        820,
		MinHeight:       600,
		InitialPosition: application.WindowCentered,
		Hidden:          true,
		BackgroundType:  application.BackgroundTypeSolid,
		URL:             themedURL("/main.html", theme),
		Mac: application.MacWindow{
			TitleBar:   application.MacTitleBarHiddenInset,
			Appearance: macAppearance(theme),
		},
	}
	// Windows 上用页面顶栏充当标题栏（隐藏原生标题栏），见 window_chrome_windows.go。
	applyWindowChrome(&opts)
	return opts
}

func settingsOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:            "settings",
		Title:           "Quota Keepalive 设置",
		Width:           480,
		Height:          560,
		Hidden:          true,
		InitialPosition: application.WindowCentered,
		BackgroundType:  application.BackgroundTypeSolid,
		URL:             themedURL("/settings.html", theme),
		Mac: application.MacWindow{
			TitleBar:   application.MacTitleBarHiddenInset,
			Appearance: macAppearance(theme),
		},
	}
}

func panelOptions(theme string) application.WebviewWindowOptions {
	opts := application.WebviewWindowOptions{
		Name:            "panel",
		Title:           "Quota Keepalive",
		Width:           392,
		Height:          620,
		MinWidth:        392,
		MinHeight:       620,
		MaxWidth:        392,
		MaxHeight:       620,
		Frameless:       true,
		AlwaysOnTop:     true,
		Hidden:          true,
		DisableResize:   true,
		HideOnFocusLost: true,
		HideOnEscape:    true,
		BackgroundType:  application.BackgroundTypeTranslucent,
		URL:             themedURL("/panel.html", theme),
		Mac: application.MacWindow{
			Backdrop:     application.MacBackdropTranslucent,
			CornerRadius: 12,
			Appearance:   macAppearance(theme),
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary,
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true},
		},
	}
	applyPanelChrome(&opts)
	return opts
}

func themedURL(path, theme string) string {
	if theme != "light" && theme != "dark" {
		return path
	}
	return path + "?theme=" + theme
}

func macAppearance(theme string) application.MacAppearanceType {
	switch theme {
	case "light":
		return application.NSAppearanceNameAqua
	case "dark":
		return application.NSAppearanceNameDarkAqua
	default:
		return application.DefaultAppearance
	}
}

// SetFailureState updates the tray tooltip. The menu-bar glyph always remains
// the native template icon so macOS never renders a detached status dot.
func (a *App) SetFailureState(hasFailure bool) {
	if a.failureOn.Swap(hasFailure) == hasFailure {
		return
	}
	tooltip := "Quota Keepalive"
	if hasFailure {
		tooltip += " · 有待处理"
	}
	if a.tray != nil {
		a.tray.SetTooltip(tooltip)
	}
}

// Run 启动 GUI 事件循环。
func (a *App) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	var statusDone chan struct{}
	if a.tokenSpeed != nil {
		a.tokenSpeed.Start(ctx)
		go a.monitorTokenSpeed(ctx)
	}
	if a.status != nil {
		statusDone = make(chan struct{})
		go func() {
			defer close(statusDone)
			a.monitorStatus(ctx)
		}()
	}
	err := a.wails.Run()
	if a.quitStop != nil {
		a.quitStop()
	}
	cancel()
	if statusDone != nil {
		<-statusDone
	}
	if a.tokenSpeed != nil {
		a.tokenSpeed.Wait()
	}
	return err
}

var statusPollInterval = 10 * time.Second

func (a *App) monitorStatus(ctx context.Context) {
	last, _ := a.pollStatus(ctx)
	ticker := time.NewTicker(statusPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 定时巡检跑在独立进程里，窗口收不到通知。状态投影一变就推一次
			// 事件，让签到页自动刷新，而不是在前端放轮询定时器。
			signature, ok := a.pollStatus(ctx)
			if !ok || signature == last {
				continue
			}
			last = signature
			if a.wails != nil {
				a.wails.Event.Emit("accounts-changed", nil)
			}
		}
	}
}

// pollStatus reads the read-only status projection once: it refreshes the tray
// failure state and returns a fingerprint used to spot what changed.
func (a *App) pollStatus(ctx context.Context) (string, bool) {
	if a.status == nil {
		return "", false
	}
	view, err := a.status(ctx)
	if err != nil {
		return "", false
	}
	a.SetFailureState(statusNeedsAttention(view))
	return statusSignature(view), true
}

// statusSignature is a read-only fingerprint of the status projection.
func statusSignature(view status.View) string {
	var builder strings.Builder
	builder.WriteString(view.LastInvocation)
	for _, account := range view.Accounts {
		builder.WriteString("|")
		builder.WriteString(account.Target)
		builder.WriteString("/")
		builder.WriteString(account.AccountID)
		builder.WriteString(":")
		builder.WriteString(account.SourceHealth)
		if account.Enabled {
			builder.WriteString("+")
		}
		for _, op := range account.Operations {
			builder.WriteString(op.Operation)
			builder.WriteString("=")
			builder.WriteString(op.State)
			if op.Credits != nil {
				builder.WriteString(fmt.Sprintf("%.4f", *op.Credits))
			}
			if op.Balance != nil {
				builder.WriteString("@")
				builder.WriteString(fmt.Sprintf("%.4f", *op.Balance))
			}
			builder.WriteString(";")
		}
	}
	return builder.String()
}

func (a *App) refreshFailureState(ctx context.Context) {
	if a.status == nil {
		return
	}
	view, err := a.status(ctx)
	if err != nil {
		return
	}
	a.SetFailureState(statusNeedsAttention(view))
}

func statusNeedsAttention(view status.View) bool {
	for _, account := range view.Accounts {
		if account.SourceHealth == "reauth_required" {
			return true
		}
		for _, operation := range account.Operations {
			switch operation.State {
			case "manual_required", "failed_retryable", "failed_final":
				return true
			}
		}
	}
	return false
}

func (a *App) monitorTokenSpeed(ctx context.Context) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	lastLabel := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot := a.tokenSpeed.Snapshot()
			label := tokenspeed.FormatSpeed(snapshot.OutputTokensPerSecond) + " tok/s"
			if label != lastLabel {
				a.tray.SetLabel(label)
				lastLabel = label
			}
			a.wails.Event.Emit("tokenspeed-changed", snapshot)
		}
	}
}

// currentApp 是最近一次 New() 创建的应用实例，供 Deps 回调在事件循环里
// 反查当前 GUI（避免把 *App 再穿一层依赖到 internal/app）。
var currentApp atomic.Pointer[App]

// Current 返回当前运行的 App；未启动时为 nil。
func Current() *App { return currentApp.Load() }
