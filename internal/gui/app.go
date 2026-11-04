// Package gui 组装 QKA 的菜单栏托盘、面板与主窗口（Wails v3）。
package gui

import (
	"context"
	"embed"
	"encoding/json"
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
	wails         *application.App
	tray          *application.SystemTray
	panel         *application.WebviewWindow
	bar           *application.WebviewWindow
	main          *application.WebviewWindow
	panelFocus    *panelFocusGuard
	theme         string
	tokenSpeed    *tokenspeed.Service
	status        func(context.Context) (status.View, error)
	providersWarm func(context.Context) error
	windowMu      sync.Mutex
	startedAt     atomic.Int64
	dockShow      atomic.Bool
	failureOn     atomic.Bool
	dockApply     func(bool)
	quitStop      func()
	// barShow/barHide 控制常驻刘海的显隐。默认指向真实窗口操作；单测替换
	// 这两个字段即可验证「主窗口可见时收起刘海、关闭主窗口后恢复」的策略，
	// 不必创建真实 WebView2 窗口（同 dockApply 的接缝写法）。
	barShow func()
	barHide func()
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
		status: deps.Status, providersWarm: deps.ProvidersWarm,
		dockApply: dockApply,
	}
	// 刘海显隐的真实实现；测试可替换这两个字段验证策略。
	result.barShow = result.showBar
	result.barHide = result.hideBar
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

// ensureBar 创建常驻刘海窗口并把它钉在主屏顶部居中。非 Windows 平台是 no-op：
// macOS 的菜单栏状态项由系统承载。
//
// 这里只创建、不显示：刘海何时可见由 showBar/hideBar 决定（主窗口可见时必须
// 收起，否则会压住主窗口顶栏中间那组导航——刘海是独立置顶窗口，钉在屏幕顶部
// 正中，正好和居中的导航重合）。
func (a *App) ensureBar() *application.WebviewWindow {
	if goos != "windows" {
		return nil
	}
	a.windowMu.Lock()
	if a.bar != nil {
		bar := a.bar
		a.windowMu.Unlock()
		return bar
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
	return bar
}

// showBar 确保刘海可见（主窗口关闭、退出面板等场景后仍要常驻）。
func (a *App) showBar() {
	if goos != "windows" {
		return
	}
	bar := a.ensureBar()
	if bar == nil {
		return
	}
	if !bar.IsVisible() {
		bar.Show()
		// 圆角必须在窗口真正创建（Show）之后设置：NativeWindow 之前是空的。
		shapeBarWindow(bar)
	}
}

// hideBar 收起刘海（不销毁，供主窗口可见时让出顶栏）。面板仍以它的位置为
// 锚点，所以位置保持不动。
func (a *App) hideBar() {
	if goos != "windows" {
		return
	}
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar != nil && bar.IsVisible() {
		bar.Hide()
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
	// 与「关闭主窗口」保持一致：主窗口不可见时刘海应回来。
	if a.barShow != nil {
		a.barShow()
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
	a.installPanelFocusClose(a.panel)
	return a.panel
}

// installPanelFocusClose 给吸附面板挂「点击外部收起」（issue #5）：监听
// WindowLostFocus，经 panelFocusGuard 延迟复核后收起。仅 Windows 需要：
// macOS 用 NSPanel 原生的 HideOnFocusLost，即时收起是系统默认行为，且没有
// 刘海点击竞态。Windows 上不启用 Wails 原生 HideOnFocusLost（它是「失焦立即
// 隐藏」，既挡不住刘海点击竞态，也会被焦点抖动触发成秒关，见
// applyPanelChrome），由这里的守卫统一负责。
func (a *App) installPanelFocusClose(panel *application.WebviewWindow) {
	if goos != "windows" {
		return
	}
	guard := newPanelFocusGuard(panelFocusHideDelay, a.hidePanel, panel.IsFocused)
	a.panelFocus = guard
	panel.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		guard.focusLost()
	})
	panel.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		guard.noteFocusRegained()
	})
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
	//
	// 尺寸必须按当前屏幕工作区收敛：1240×820 在 125%/150% 缩放的笔记本上会
	// 超出工作区，标题栏的最小化/最大化/关闭按钮被挤出屏幕，用户既关不掉也
	// 缩不小。
	size, workX, workY, workWidth, workHeight := a.fitMainWindowSize()
	applog.Infof("gui", "主窗口尺寸 %dx%d DIP，工作区 %dx%d DIP（超限会收敛）",
		size.Width, size.Height, workWidth, workHeight)
	main := a.wails.Window.NewWithOptions(mainOptionsSized(a.theme, size))
	a.main = main
	if workWidth > 0 && workHeight > 0 {
		x, y := centeredOrigin(size.Width, size.Height, workX, workY, workWidth, workHeight)
		main.SetPosition(x, y)
	}
	// 拖到不同缩放比例的显示器、或改分辨率后，窗口物理尺寸会变；重新收敛一次
	// 才能保证标题栏按钮始终在屏幕内。
	main.OnWindowEvent(events.Common.WindowDPIChanged, func(*application.WindowEvent) {
		a.refitMainWindowToScreen(main)
	})
	main.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		// 关闭主窗口不销毁：QKA 常驻菜单栏/托盘，隐藏后托盘与双击都要能再打开。
		main.Hide()
		// Closing the main window leaves QKA resident in the menu bar, so the
		// Dock icon should disappear until the user opens the main window again.
		a.hideDockForMenuBar()
		// 主窗口收起后让常驻刘海回来：它才是窗口隐藏期间唯一的速度指示与
		// 点击入口。必须异步——这里跑在窗口消息处理器里，同步 Show 会对同一
		// 线程发窗口消息，造成自锁（参见 installPlatformWindowBehavior）。
		if a.barShow != nil {
			go a.barShow()
		}
		event.Cancel()
	})
	return main
}

// fitMainWindowSize 返回收敛到主屏工作区内的主窗口尺寸与工作区几何（均为
// DIP）。屏幕信息拿不到时返回理想尺寸与零几何，交由平台默认摆放。
func (a *App) fitMainWindowSize() (size windowSize, workX, workY, workWidth, workHeight int) {
	size = mainWindowSize()
	if a.wails == nil {
		return size, 0, 0, 0, 0
	}
	screen := a.wails.Screen.GetPrimary()
	if screen == nil {
		return size, 0, 0, 0, 0
	}
	area := screen.WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		area = screen.Bounds
	}
	if area.Width <= 0 || area.Height <= 0 {
		return size, 0, 0, 0, 0
	}
	// Wails 的 LayoutScreens 已把 WorkArea 换算成 DIP，可直接与窗口尺寸比较。
	return fitWindowSize(size, area.Width, area.Height, windowFitMargin),
		area.X, area.Y, area.Width, area.Height
}

// refitMainWindowToScreen 在屏幕/缩放变化后把主窗口重新收敛到工作区：
// 只在「当前尺寸已超出可用空间」时收小，不覆盖用户手动调整过的合理尺寸。
func (a *App) refitMainWindowToScreen(main *application.WebviewWindow) {
	if main == nil {
		return
	}
	size, workX, workY, workWidth, workHeight := a.fitMainWindowSize()
	if workWidth <= 0 || workHeight <= 0 {
		return
	}
	width, height := main.Size()
	if width <= size.Width && height <= size.Height {
		return
	}
	if width > size.Width {
		width = size.Width
	}
	if height > size.Height {
		height = size.Height
	}
	main.SetSize(width, height)
	// 窗口变小后可能仍压在屏幕边缘外：重新居中。
	x, y := centeredOrigin(width, height, workX, workY, workWidth, workHeight)
	main.SetPosition(x, y)
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

// panelFocusHideDelay 是面板失焦到真正收起之间的缓冲（毫秒级，观感上仍是
// 「点外面就收」）。点击刘海/托盘收面板时，面板的失焦事件先到、刘海的点击
// （HTML 事件 → JS 绑定 → Go）后到，这个缓冲让 toggle 先跑完，避免「点了
// 刘海，面板关了又弹出来/闪关」。要求覆盖 100-300ms，取 250ms。
const panelFocusHideDelay = 250 * time.Millisecond

// panelFocusGuard 消解「失焦自动收起」与「点击刘海/托盘切换面板」的竞态。
//
// 在 WindowLostFocus 里立即 Hide 有两个坑：
//  1. 点击刘海收面板时，面板先失焦、刘海的点击后到（webview JS 绑定绕一圈，
//     慢于原生焦点切换），先 Hide 后 toggle 会把面板又弹出来或闪关；
//  2. Show+Focus 过程中的焦点抖动会把刚弹出的面板秒关（见 applyPanelChrome
//     的历史注释，Win10 上曾因此整体关掉 HideOnFocusLost）。
//
// 方案：失焦只登记一个延迟收起，用代数（generation）标记；期间任何显示意图
// （noteShowIntended，showPanel/togglePanel* 入口）、收起（noteHidden）、
// 焦点回到面板（noteFocusRegained，窗口事件 WindowFocus）都会换代作废登记；
// 到期再复核面板是否确实仍无焦点，才执行收起。hide/after/isFocused 都可注入，
// 不开窗口即可单测。所有方法对 nil 接收者安全：非 Windows 平台不装守卫，
// 调用点无需判空。
type panelFocusGuard struct {
	// delay 是失焦到复核收起的缓冲时长。
	delay time.Duration
	// hide 执行真正的收起（生产环境是 App.hidePanel）。
	hide func()
	// isFocused 复核面板当前是否实际持有焦点：到期时为 true 说明失焦只是
	// 焦点抖动/焦点已回来，跳过收起。
	isFocused func() bool
	// after 登记延迟回调（生产环境是 time.AfterFunc，测试换成手动触发的假时钟）。
	after func(d time.Duration, fn func())

	mu         sync.Mutex
	generation uint64
}

// newPanelFocusGuard 创建守卫：delay 为 0 时退化为「立即收起」（仍保留代数
// 与焦点复核）。
func newPanelFocusGuard(delay time.Duration, hide func(), isFocused func() bool) *panelFocusGuard {
	g := &panelFocusGuard{
		delay:     delay,
		hide:      hide,
		isFocused: isFocused,
	}
	g.after = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	return g
}

// focusLost 登记一次失焦。回调跑在定时器 goroutine 上，真正的 Hide 经
// InvokeSync 回主线程，不会占用窗口事件分发线程。
func (g *panelFocusGuard) focusLost() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.generation++
	gen := g.generation
	g.mu.Unlock()
	g.after(g.delay, func() { g.fire(gen) })
}

// fire 是延迟到期后的复核：只有登记仍是最新一代、且面板确实没有拿回焦点，
// 才收起。旧登记（期间发生过显示/收起/回焦）到这里已经过期，直接放弃。
func (g *panelFocusGuard) fire(gen uint64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	current := g.generation == gen
	g.mu.Unlock()
	if !current || g.isFocused == nil || g.isFocused() {
		return
	}
	g.hide()
}

// noteShowIntended 在一切「即将操作面板可见性」的入口（showPanel/togglePanel/
// togglePanelAtNotch）先调用：作废挂起的失焦收起。点刘海收面板的竞态正靠它
// 化解——失焦登记后刘海点击进入 toggle，先作废登记，再按可见性决定显示或
// 隐藏，无论哪个分支都不会再被到期的旧登记补一刀。
func (g *panelFocusGuard) noteShowIntended() { g.bump() }

// noteHidden 在面板已被其它路径收起（Esc、面板退出按钮、toggle 的隐藏分支、
// showMain 前的收起）后调用：作废挂起的失焦收起，避免二次 Hide。
func (g *panelFocusGuard) noteHidden() { g.bump() }

// noteFocusRegained 在焦点回到面板（窗口事件 WindowFocus）时调用：作废挂起
// 的失焦收起——失焦只是抖动，面板仍是活动窗口。
func (g *panelFocusGuard) noteFocusRegained() { g.bump() }

// bump 换代：让所有已登记未到期的延迟收起全部过期。
func (g *panelFocusGuard) bump() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.generation++
	g.mu.Unlock()
}

// currentGeneration 返回当前代数。测试用它等待异步派发的窗口事件监听器
// 真正落到守卫上（HandleWindowEvent 的监听器跑在独立 goroutine）。
func (g *panelFocusGuard) currentGeneration() uint64 {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.generation
}

func (a *App) showPanel() {
	// 先作废挂起的失焦收起：面板弹出瞬间的焦点抖动不该把面板收回去。
	a.panelFocus.noteShowIntended()
	panel := a.ensurePanel()
	_ = a.tray.PositionWindow(panel, panelGap)
	a.fitPanelHeightToWorkArea(panel, panelTop(panel))
	panel.Show().Focus()
}

func (a *App) togglePanel() {
	// 同 showPanel：进入切换前作废挂起的失焦收起（见 panelFocusGuard）。
	a.panelFocus.noteShowIntended()
	panel := a.ensurePanel()
	_ = a.tray.PositionWindow(panel, panelGap)
	// 只在即将显示时收敛高度（隐藏时不必动窗口，避免多余的主线程调用）。
	if !panel.IsVisible() {
		a.fitPanelHeightToWorkArea(panel, panelTop(panel))
	}
	togglePanelWindow(panelActions{window: panel})
}

// panelTop 返回面板当前的屏幕 Y 坐标（DIP）；拿不到时返回 -1（调用方按
// 「位置未知」处理，不收敛尺寸）。
func panelTop(panel *application.WebviewWindow) int {
	if panel == nil {
		return -1
	}
	_, y := panel.Position()
	return y
}

// togglePanelAtNotch 在顶栏刘海（Windows 菜单栏状态项）下方切换吸附面板。
// 坐标是刘海在窗口内的 DIP 位置，换算成屏幕坐标后把面板摆到刘海正下方，
// 与 macOS 点菜单栏图标时面板贴着菜单栏展开的观感一致。
func (a *App) togglePanelAtNotch(x, y int) {
	// 关键竞态：点击刘海时面板先失焦（守卫已登记延迟收起）、刘海的点击后到。
	// 这里先作废那份登记再切换，收起/展开都由本次点击一次完成，不会
	// 「点了刘海，面板闪关又弹出」。
	a.panelFocus.noteShowIntended()
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
	a.fitPanelHeightToWorkArea(panel, by+bh+notchPanelGap)
	panel.Show().Focus()
}

// fitPanelHeightToWorkArea 把小屏上的吸附面板高度收敛到工作区内：面板固定在
// 刘海下方展开，屏幕矮时 620 会顶出底边、底部内容（退出按钮等）被裁掉。
// 读取失败时保持原尺寸，不因探测问题而缩小面板。
func (a *App) fitPanelHeightToWorkArea(panel *application.WebviewWindow, top int) {
	if panel == nil || a.wails == nil {
		return
	}
	screen := a.wails.Screen.GetPrimary()
	if screen == nil {
		return
	}
	area := screen.WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		area = screen.Bounds
	}
	if area.Width <= 0 || area.Height <= 0 {
		return
	}
	width, current := panel.Size()
	height := fitPanelHeight(top, area.Y+area.Height, panelIdealHeight)
	if height == current {
		return
	}
	if width <= 0 {
		width = panelIdealWidth
	}
	panel.SetSize(width, height)
}

func (a *App) hidePanel() {
	// 面板已被主动收起，作废挂起的失焦收起，避免延迟到期后再补一次 Hide。
	a.panelFocus.noteHidden()
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
	// 主窗口可见时必须收起常驻刘海：刘海是独立置顶窗口、钉在屏幕顶部正中，
	// 而主窗口的导航条也居中——两者会精确重合，把「宠物设置」等页签压住。
	// 主窗口自己已经有完整的顶栏与速度页，收起后信息不丢；关闭主窗口时再恢复。
	if a.barHide != nil {
		a.barHide()
	}
}

// mainOptions 返回主窗口默认选项（理想尺寸）。真正创建窗口时走
// mainOptionsSized，把尺寸收敛到当前屏幕工作区。
func mainOptions(theme string) application.WebviewWindowOptions {
	return mainOptionsSized(theme, mainWindowSize())
}

// mainOptionsSized 按给定尺寸约束构造主窗口选项。
func mainOptionsSized(theme string, size windowSize) application.WebviewWindowOptions {
	opts := application.WebviewWindowOptions{
		Name:            "main",
		Title:           "Quota Keepalive",
		Width:           size.Width,
		Height:          size.Height,
		MinWidth:        size.MinWidth,
		MinHeight:       size.MinHeight,
		InitialPosition: application.WindowCentered,
		Hidden:          true,
		BackgroundType:  application.BackgroundTypeSolid,
		URL:             themedURL("/main.html", theme),
		Mac: application.MacWindow{
			TitleBar:   application.MacTitleBarHiddenInset,
			Appearance: macAppearance(theme),
		},
	}
	// 平台差异（标题栏形态等）由 applyWindowChrome 决定：Windows 保留系统
	// 原生标题栏，见 window_chrome_windows.go。
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
	// 打开窗口时才读供应商列表会现场冷启动随包 Bun 运行时，首屏因此空几秒。
	// 启动即预热一次，把运行时与模型目录都准备好，首次打开就是现成的投影。
	if a.providersWarm != nil {
		go func() { _ = a.providersWarm(ctx) }()
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
		// 退出不应被一轮慢扫描无限拖住：扫描已支持取消，这里再兜一个上限，
		// 避免网络盘等异常文件系统上「点退出后进程滞留」。
		a.tokenSpeed.WaitWithin(2 * time.Second)
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
	lastSignature := ""
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
			// 内容没变就不广播：此前每 750ms 无条件 Emit，常驻刘海与面板
			// 的 WebView2 渲染进程因此永远无法空闲（实测 CPU 空转主源）。
			signature := tokenSpeedSignature(snapshot)
			if signature == lastSignature {
				continue
			}
			lastSignature = signature
			if a.wails != nil {
				a.wails.Event.Emit("tokenspeed-changed", snapshot)
			}
		}
	}
}

// tokenSpeedSignature 把快照收敛成内容指纹。UpdatedAt 每轮都变，必须排除，
// 否则去重恒定失效；其余字段都是界面真正展示的内容。
func tokenSpeedSignature(snapshot tokenspeed.Snapshot) string {
	snapshot.UpdatedAt = ""
	raw, err := json.Marshal(snapshot)
	if err != nil {
		// 序列化失败时退回广播，宁可多刷新也不让界面定格。
		return time.Now().Format(time.RFC3339Nano)
	}
	return string(raw)
}

// currentApp 是最近一次 New() 创建的应用实例，供 Deps 回调在事件循环里
// 反查当前 GUI（避免把 *App 再穿一层依赖到 internal/app）。
var currentApp atomic.Pointer[App]

// Current 返回当前运行的 App；未启动时为 nil。
func Current() *App { return currentApp.Load() }
