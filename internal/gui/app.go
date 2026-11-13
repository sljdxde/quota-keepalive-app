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
	"qka/internal/selfupdate"
	"qka/internal/settings"
	"qka/internal/status"
	"qka/internal/tokenspeed"
)

//go:embed frontend
var frontendFS embed.FS

// App 持有 Wails 应用与运行期窗口。
type App struct {
	wails       *application.App
	tray        *application.SystemTray
	trayStateMu sync.Mutex
	trayPet     string
	trayCaught  bool
	panel       *application.WebviewWindow
	ballPanel   *application.WebviewWindow
	petMini     *application.WebviewWindow
	ballMenu    *application.WebviewWindow
	bar         *application.WebviewWindow
	main        *application.WebviewWindow
	panelFocus  *panelFocusGuard
	// ballPanelFocus 管 Windows 精简面板的「点外面收起」，语义同 panelFocus。
	ballPanelFocus *panelFocusGuard
	petMiniFocus   *panelFocusGuard
	ballMenuFocus  *panelFocusGuard
	theme          string
	tokenSpeed     *tokenspeed.Service
	// settings 读取当前设置：菜单栏速度样式、采样频率都在运行中可能被改，
	// 不能只在启动时读一次。
	settings      func() *settings.Settings
	updateCheck   func(context.Context) (selfupdate.Status, error)
	status        func(context.Context) (status.View, error)
	providersWarm func(context.Context) error
	windowMu      sync.Mutex
	// barStateMu 串行化悬浮球状态文件的读写与贴边状态字段。
	barStateMu sync.Mutex
	// barDocked 是当前贴边侧（"" / "left" / "right"）；barRestoreX/Y 是贴边前
	// 的位置，点击弹回时用。
	barDocked   string
	barRestoreX int
	barRestoreY int
	// 贴边弹回后「点空白再收起」：点球把球从贴边弹成浮动时登记原贴边侧与贴边前
	// 位置（armed），用户随即点桌面/空白使球窗失焦 → 收回贴边。期间任何对球的
	// 正式操作（再点开宠物窗、拖动、右键菜单）都取消登记，避免误收。
	barRedockArmed bool
	barRedockSide  string
	barRedockResX  int
	barRedockResY  int
	// barShaped 记录上一次 SetWindowRgn 的尺寸与贴边侧：resize 事件反复到
	// 达时，同形状直接跳过，避免 SetWindowRgn 被连续调用带着屏幕闪。
	barShapedW    int
	barShapedH    int
	barShapedSide string
	startedAt     atomic.Int64
	dockShow      atomic.Bool
	failureOn     atomic.Bool
	// ballShow 记录「显示悬浮球」设置：false 时球与它的浮层都不再出现，
	// 也不参与「主窗口关闭后恢复」逻辑。
	ballShow  atomic.Bool
	dockApply func(bool)
	quitStop  func()
	// barShow/barHide 控制常驻状态项（Windows 悬浮球）的显隐。默认指向真实
	// 窗口操作；单测替换这两个字段即可验证「主窗口可见时收起、关闭后恢复」
	// 的策略，不必创建真实 WebView2 窗口（同 dockApply 的接缝写法）。
	barShow func()
	barHide func()
}

const panelGap = 8

// notchPanelGap 是旧吸附面板与顶栏之间的间距（像素）。
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
	showBall := true
	theme := "system"
	if deps.Settings != nil {
		current := deps.Settings()
		showDock = current.Get("show_dock_icon")
		showBall = current.Get("show_ball")
		theme = current.GetString("theme")
	}

	dockApply := deps.DockIconVisible
	if dockApply == nil {
		dockApply = setDockIconVisible
	}
	result := &App{
		theme: theme, tokenSpeed: deps.TokenSpeed,
		status: deps.Status, providersWarm: deps.ProvidersWarm,
		updateCheck: deps.UpdateCheck,
		settings:    deps.Settings,
		dockApply:   dockApply,
	}
	// 采样频率是运行时可调的设置项：启动时先按已保存的值生效，
	// 之后由设置页的保存回调继续更新。
	applyTokenSpeedPoll(result.tokenSpeed, deps.Settings)
	// 悬浮球显隐的真实实现；测试可替换这两个字段验证策略。
	result.barShow = result.showBar
	result.barHide = result.hideBar
	result.dockShow.Store(showDock)
	result.ballShow.Store(showBall)
	deps.DockIconVisible = result.setDockPreference
	deps.BallVisible = result.setBallPreference
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
	// 迷你宠物与自绘右键菜单的 Esc 收起。
	wails.Event.On("collapse-petmini", func(*application.CustomEvent) {
		go result.hidePetMini()
	})
	wails.Event.On("collapse-ballmenu", func(*application.CustomEvent) {
		go result.hideBallMenu()
	})
	// 前端切换伙伴时同步 macOS 菜单栏图标：主窗口与菜单栏面板都会发这条事件。
	wails.Event.On("pet-selected", func(event *application.CustomEvent) {
		if petID := trayPetFromEventData(event.Data); petID != "" {
			result.setTrayPet(petID)
		}
	})
	// 伙伴被收进精灵球/放出时，菜单栏图标也切成精灵球/伙伴剪影。
	wails.Event.On("pet-caught", func(event *application.CustomEvent) {
		if caught, ok := trayCaughtFromEventData(event.Data); ok {
			result.setTrayCaught(caught)
		}
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
	installMenuBarLabelParser()
	tray.SetTooltip("Quota Keepalive")
	tray.SetLabel("0 tok/s")
	// 单击托盘：异步切换窗口（原因同上，不能在托盘消息处理器里同步操作窗口）。
	// Windows 托盘左键直接进主界面；macOS 仍是菜单栏面板。
	tray.OnClick(func() {
		if goos == "windows" {
			go result.showMain()
			return
		}
		go result.togglePanel()
	})
	tray.SetMenu(menu)

	result.tray = tray
	// 恢复上次的菜单栏伙伴/精灵球状态：前端把选择存在 WebView 的
	// localStorage，后端启动时读不到；不恢复的话每次重启都会先退回 Q 图标。
	savedTray := loadTrayState()
	if savedTray.Pet == "" {
		savedTray.Pet = defaultTrayPet
	}
	result.trayPet = savedTray.Pet
	result.trayCaught = savedTray.Caught
	result.applyTrayState(savedTray.Pet, savedTray.Caught)
	// 常驻悬浮球（Windows 菜单栏状态项）在事件循环启动后由
	// installPlatformWindowBehavior 创建：此时窗口系统才真正就绪，位置和
	// 圆形区域才设得上。
	result.installPlatformWindowBehavior()
	wails.Event.OnApplicationEvent(events.Common.ApplicationStarted, result.onApplicationStarted)
	return result
}

// ensureBar 创建 Windows 悬浮球窗口（菜单栏状态项的 Windows 形态）并恢复上次的
// 位置/贴边状态。非 Windows 平台是 no-op：macOS 的菜单栏状态项由系统承载。
//
// 这里只创建、不显示：何时可见由 showBar/hideBar 决定（主窗口可见时收起）。
func (a *App) ensureBar() *application.WebviewWindow {
	if goos != "windows" {
		return nil
	}
	if a.wails == nil {
		// 测试与零值 App：窗口系统未装配，返回 nil 让调用方安全跳过
		// （showBar/hideBar 都对 nil 容忍）。
		return nil
	}
	a.windowMu.Lock()
	if a.bar != nil {
		bar := a.bar
		a.windowMu.Unlock()
		return bar
	}
	bar := a.wails.Window.NewWithOptions(barWindowOptions(a.theme))
	a.bar = bar
	a.windowMu.Unlock()

	// 以下都涉及主线程窗口操作（InvokeSync），必须在放锁之后执行：
	// 持着 windowMu 等主线程，而主窗口可能正在等 windowMu，会互等死锁。
	a.restoreBarPosition(bar)

	// 悬浮球必须常驻：Alt+F4 之类触发关闭时取消掉，不让窗口被销毁。
	bar.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
	})
	// DPI 变化（拖到不同缩放比例的显示器）后窗口物理尺寸会变，圆形区域是按
	// 物理像素算的，必须重新裁一次。
	bar.OnWindowEvent(events.Common.WindowDPIChanged, func(*application.WindowEvent) {
		a.shapeBall(bar)
	})
	// SetSize（贴边 44 / 弹回 64）也会改窗口尺寸，尺寸落定后重裁。
	bar.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) {
		a.shapeBall(bar)
	})
	// 用户拖动结束后再判定贴边/钳制：拖动过程中实时吸附会让球粘在边缘、
	// 无法拖过去。必须在原生移动循环结束时处理（SetPosition 不会触发）。
	//
	// Wails v3.0.0 在 WM_EXITSIZEMOVE 里按左键是否仍按下二选一发 EndMove /
	// EndResize，实际松手时通常落到 EndResize；两个都监听，谁到算谁。
	onBarDragEnd := func(*application.WindowEvent) { a.onBarMoveEnd(bar) }
	bar.OnWindowEvent(events.Windows.WindowEndMove, onBarDragEnd)
	bar.OnWindowEvent(events.Windows.WindowEndResize, onBarDragEnd)
	// 失焦即「用户点了桌面/别处」：若此前刚把球从贴边弹成浮动（待收起登记在），
	// 就把它收回贴边。只在登记状态下动作，普通浮动态失焦不受影响。
	bar.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		a.onBarBlur()
	})
	return bar
}

// restoreBarPosition 在创建窗口后恢复持久化的位置：普通态直接落位，贴边态
// 恢复成 36px 半圆；没有状态文件（首启、配置目录未知）时落在主屏顶部居中。
func (a *App) restoreBarPosition(bar *application.WebviewWindow) {
	if bar == nil {
		return
	}
	state, ok := loadBarState(barStatePath())
	if !ok {
		area := a.primaryWorkArea()
		x, y := ballDefaultOrigin(area)
		bar.SetPosition(x, y)
		return
	}
	if state.Docked != "" {
		area := a.workAreaForPoint(state.RestoreX, state.RestoreY)
		x, y := ballDockedPosition(area, state.Docked, state.RestoreX, state.RestoreY)
		bar.SetSize(ballDockSize, ballDockSize)
		bar.SetPosition(x, y)
		a.rememberBarState(barWindowState{
			X: x, Y: y, Docked: state.Docked,
			RestoreX: state.RestoreX, RestoreY: state.RestoreY,
		})
		return
	}
	area := a.workAreaForPoint(state.X, state.Y)
	x, y := clampBallPosition(area, state.X, state.Y, ballSize)
	bar.SetPosition(x, y)
}

// primaryWorkArea 返回主屏工作区（DIP）；窗口系统还没就绪时返回零矩形。
func (a *App) primaryWorkArea() application.Rect {
	if a.wails == nil {
		return application.Rect{}
	}
	screen := a.wails.Screen.GetPrimary()
	if screen == nil {
		return application.Rect{}
	}
	return screenWorkArea(screen)
}

// workAreaForPoint 返回包含该点（DIP）的屏幕工作区；找不到时退回主屏。
func (a *App) workAreaForPoint(x, y int) application.Rect {
	if a.wails != nil {
		if screen := a.wails.Screen.ScreenNearestDipPoint(application.Point{X: x, Y: y}); screen != nil {
			if area := screenWorkArea(screen); area.Width > 0 && area.Height > 0 {
				return area
			}
		}
	}
	return a.primaryWorkArea()
}

// screenWorkArea 取屏幕工作区，退化时用屏幕边界。
func screenWorkArea(screen *application.Screen) application.Rect {
	if screen == nil {
		return application.Rect{}
	}
	if area := screen.WorkArea; area.Width > 0 && area.Height > 0 {
		return area
	}
	return screen.Bounds
}

// ballShouldShow 决定悬浮球此刻是否应该可见：
// 设置关掉 → 不显示；主窗口可见 → 让位（主窗口自己有顶栏与速度页）；否则显示。
// 纯函数，便于表驱动单测覆盖全部组合。
func ballShouldShow(enabled bool, mainVisible bool) bool {
	return enabled && !mainVisible
}

// showBar 确保悬浮球可见（主窗口关闭等场景后仍要常驻）。
// 「显示悬浮球」设置关闭时不显示；托盘仍承担状态项职责。
func (a *App) showBar() {
	if goos != "windows" {
		return
	}
	if !a.ballShow.Load() {
		return
	}
	bar := a.ensureBar()
	if bar == nil {
		return
	}
	if !bar.IsVisible() {
		bar.Show()
		// 圆形区域必须在窗口真正创建（Show）之后设置：NativeWindow 之前是空的。
		a.shapeBall(bar)
	}
}

// hideBar 收起悬浮球（不销毁，供主窗口可见时让出屏幕）。位置保持不动，
// 状态面板仍以它为锚点。
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

// setBallPreference 实现 Deps.BallVisible：应用「显示悬浮球」设置并立即生效。
func (a *App) setBallPreference(visible bool) {
	a.ballShow.Store(visible)
	if !visible {
		a.hideBallPanel()
		a.hidePetMini()
		a.hideBallMenu()
		a.hideBar()
		return
	}
	// 重新显示：主窗口可见时球仍然让位（与 showMain 的策略一致）。
	a.windowMu.Lock()
	main := a.main
	a.windowMu.Unlock()
	mainVisible := main != nil && main.IsVisible()
	if ballShouldShow(true, mainVisible) {
		a.showBar()
	}
}

// barAnchor 返回悬浮球在屏幕上的位置与尺寸，用于把状态面板摆在它旁边。
func (a *App) barAnchor() (x, y, width, height int) {
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		return 0, 0, ballSize, ballSize
	}
	px, py := bar.Position()
	width, height = bar.Size()
	if width <= 0 || height <= 0 {
		return px, py, ballSize, ballSize
	}
	return px, py, width, height
}

// barDockState 读取贴边状态与弹回坐标。
func (a *App) barDockState() (string, int, int) {
	a.barStateMu.Lock()
	defer a.barStateMu.Unlock()
	return a.barDocked, a.barRestoreX, a.barRestoreY
}

// shapeBall 按当前贴边状态重裁悬浮球窗口：浮动态正圆，贴边态半圆。
// 同尺寸同侧直接跳过——SetWindowRgn 每次调用都会强制窗口与桌面区域重绘，
// 冗余调用在部分驱动下会表现为整屏闪动。
func (a *App) shapeBall(bar *application.WebviewWindow) {
	if bar == nil {
		return
	}
	side, _, _ := a.barDockState()
	width, height := bar.Size()
	a.barStateMu.Lock()
	same := a.barShapedW == width && a.barShapedH == height && a.barShapedSide == side
	a.barStateMu.Unlock()
	if same {
		return
	}
	if !shapeBarWindowSide(bar, side) {
		// 没裁成（窗口还没创建/系统调用失败）：不记忆，下次事件重试。
		return
	}
	a.barStateMu.Lock()
	a.barShapedW = width
	a.barShapedH = height
	a.barShapedSide = side
	a.barStateMu.Unlock()
}

// rememberBarState 更新内存中的贴边状态并写盘（原子写、失败只记日志）。
func (a *App) rememberBarState(state barWindowState) {
	a.barStateMu.Lock()
	a.barDocked = state.Docked
	a.barRestoreX = state.RestoreX
	a.barRestoreY = state.RestoreY
	path := barStatePath()
	a.barStateMu.Unlock()
	if err := saveBarState(path, state); err != nil {
		applog.Warnf("gui", "保存悬浮球位置失败：%v", err)
	}
}

// onBarMoveEnd 处理用户拖动结束：贴近任一边缘就吸附成 36px 半圆，否则钳进
// 工作区并记忆位置。
func (a *App) onBarMoveEnd(bar *application.WebviewWindow) {
	if bar == nil {
		return
	}
	x, y := bar.Position()
	width, height := bar.Size()
	if width <= 0 || height <= 0 {
		return
	}
	area := a.workAreaForPoint(x+width/2, y+height/2)
	if area.Width <= 0 || area.Height <= 0 {
		return
	}
	docked, restoreX, restoreY := a.barDockState()
	// 落点判定在 resolveBarDrop（纯函数、表驱动单测覆盖），这里只把结果
	// 落到窗口上并记忆。
	drop := resolveBarDrop(area, x, y, width, docked, restoreX, restoreY)

	a.hideBallPanel()
	if drop.Action == "dock" {
		// 吸附成半圆：宠物舞台与速度数字都放不下了，浮层一并收起。
		a.hidePetMini()
		a.hideBallMenu()
	}
	if drop.Size != width {
		bar.SetSize(drop.Size, drop.Size)
	}
	bar.SetPosition(drop.X, drop.Y)
	a.rememberBarState(barWindowState{
		X: drop.X, Y: drop.Y, Docked: drop.Docked,
		RestoreX: drop.RestoreX, RestoreY: drop.RestoreY,
	})
	a.shapeBall(bar)
}

// undockBar 把贴边态弹回浮动态：恢复 52px 与贴边前的位置（再钳进工作区）。
func (a *App) undockBar(restoreX, restoreY int) {
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		return
	}
	area := a.workAreaForPoint(restoreX, restoreY)
	if area.Width <= 0 || area.Height <= 0 {
		return
	}
	bar.SetSize(ballSize, ballSize)
	x, y := clampBallPosition(area, restoreX, restoreY, ballSize)
	bar.SetPosition(x, y)
	a.rememberBarState(barWindowState{X: x, Y: y})
	a.shapeBall(bar)
}

// BarToggle 实现悬浮球左键点击：贴边时先弹回，否则切换三 tab 面板
// （panel.html，与 macOS 菜单栏面板同一页面）。
func (a *App) BarToggle() {
	if goos != "windows" {
		return
	}
	if side, rx, ry := a.barDockState(); side != "" {
		// 贴边态点击只把球弹回浮动态；再点一次才开面板。登记「待收起」，
		// 让用户点桌面/空白（球窗失焦）时球自动收回贴边。
		a.armBarRedock(side, rx, ry)
		a.undockBar(rx, ry)
		// 主动聚焦球窗：无边框置顶球点击后若未获焦，「点别处」不会产生失焦事件，
		// 收回就不会触发。用户既然点了球，聚焦它是符合预期的。
		a.focusBar()
		return
	}
	// 浮动态点击是打开面板，取消任何待收起登记。
	a.clearBarRedock()
	a.hidePetMini()
	a.hideBallMenu()
	a.toggleBallPanel()
}

// armBarRedock / clearBarRedock / takeBarRedock 管理「贴边弹回后待重新收起」的
// 一次性登记，读写都在 barStateMu 下串行，避免失焦事件与点击互相抢。
func (a *App) armBarRedock(side string, rx, ry int) {
	a.barStateMu.Lock()
	defer a.barStateMu.Unlock()
	a.barRedockArmed = true
	a.barRedockSide = side
	a.barRedockResX = rx
	a.barRedockResY = ry
}

func (a *App) clearBarRedock() {
	a.barStateMu.Lock()
	defer a.barStateMu.Unlock()
	a.barRedockArmed = false
	a.barRedockSide = ""
}

// takeBarRedock 原子地取出并清空登记；未登记时 ok=false。
func (a *App) takeBarRedock() (side string, rx, ry int, ok bool) {
	a.barStateMu.Lock()
	defer a.barStateMu.Unlock()
	if !a.barRedockArmed {
		return "", 0, 0, false
	}
	side, rx, ry = a.barRedockSide, a.barRedockResX, a.barRedockResY
	a.barRedockArmed = false
	a.barRedockSide = ""
	return side, rx, ry, true
}

// onBarBlur 是球窗失焦处理：只有处于「刚弹回待收起」状态才把球收回贴边，
// 普通浮动态失焦（例如去用主窗口）不改变球的位置。
func (a *App) onBarBlur() {
	side, rx, ry, ok := a.takeBarRedock()
	if !ok || side == "" {
		return
	}
	a.dockBarNow(side, rx, ry)
}

// dockBarNow 把球按指定侧吸附成 36px 半圆，供失焦收回复用（等价于
// onBarMoveEnd 的 dock 分支，但吸附侧是弹回前那一侧，位置用贴边前坐标）。
func (a *App) dockBarNow(side string, restoreX, restoreY int) {
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil || side == "" {
		return
	}
	area := a.workAreaForPoint(restoreX, restoreY)
	if area.Width <= 0 || area.Height <= 0 {
		return
	}
	a.hideBallPanel()
	a.hidePetMini()
	a.hideBallMenu()
	if w, _ := bar.Size(); w != ballDockSize {
		bar.SetSize(ballDockSize, ballDockSize)
	}
	x, y := ballDockedPosition(area, side, restoreX, restoreY)
	bar.SetPosition(x, y)
	a.rememberBarState(barWindowState{
		X: x, Y: y, Docked: side, RestoreX: restoreX, RestoreY: restoreY,
	})
	a.shapeBall(bar)
}

// focusBar 把球窗带到前台并聚焦，让随后的「点桌面/别处」能触发失焦收回。
func (a *App) focusBar() {
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		return
	}
	bar.Focus()
}

// BarStartDrag 让悬浮球进入系统原生移动循环（页面在「按住并移动超过
// 阈值」时调用，把窗口对中到指针下之后）。等价于 Wails 内置的
// "wails:drag" 消息：ReleaseCapture + WM_NCLBUTTONDOWN(HTCAPTION)，
// 拖动期间 runtime 会吞掉后续鼠标事件，不会误触发球上的手势。
func (a *App) BarStartDrag() {
	if goos != "windows" {
		return
	}
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		return
	}
	// 拖动是对球的正式操作：取消「点空白收回贴边」的一次性登记，
	// 否则拖到半路失焦会被强行吸回原来的边。
	a.clearBarRedock()
	bar.HandleMessage("wails:drag")
}

// BarMenu 实现悬浮球右键点击：收起其它浮层，切换自绘菜单（打开主界面 /
// 设置 / 退出）。菜单是一个独立小窗，样式按 magpie 的 .row-menu 做。
func (a *App) BarMenu() {
	if goos != "windows" {
		return
	}
	a.windowMu.Lock()
	bar := a.bar
	a.windowMu.Unlock()
	if bar == nil {
		return
	}
	// 右键菜单是对球的正式操作：取消待收起登记，交给菜单自身的失焦收起。
	a.clearBarRedock()
	a.hideBallPanel()
	a.hidePetMini()
	a.toggleBallMenu()
}

// installPopoverFocusClose 给球旁边的浮层挂「点击外部收起」，复用吸附面板的
// panelFocusGuard：失焦只登记延迟收起，球点击进入 toggle 时先作废登记，
// 避免「点球，浮层闪关又弹出」。
func (a *App) installPopoverFocusClose(win *application.WebviewWindow, hide func()) *panelFocusGuard {
	if goos != "windows" || win == nil {
		return nil
	}
	guard := newPanelFocusGuard(panelFocusHideDelay, hide, win.IsFocused)
	win.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		guard.focusLost()
	})
	win.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		guard.noteFocusRegained()
	})
	return guard
}

// ensureBallPanel 创建 Windows 精简状态面板（首次使用时惰性创建）。
func (a *App) ensureBallPanel() *application.WebviewWindow {
	if goos != "windows" {
		return nil
	}
	if a.wails == nil {
		// 窗口系统未装配（零值 App / 单测）：返回 nil 让调用方安全跳过。
		return nil
	}
	a.windowMu.Lock()
	if a.ballPanel != nil {
		panel := a.ballPanel
		a.windowMu.Unlock()
		return panel
	}
	win := a.wails.Window.NewWithOptions(ballPanelOptions(a.theme))
	a.ballPanel = win
	a.windowMu.Unlock()
	// 收到 WM_CLOSE（Alt+F4 / 关闭类工具）时取消销毁、只隐藏：窗口被销毁后
	// a.ballPanel 会悬空，之后的 Show 全部静默失败，面板永远弹不出来。失焦
	// 与 Esc 本就走 Hide，这里只挡真正的销毁路径。
	win.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		win.Hide()
	})
	a.ballPanelFocus = a.installPopoverFocusClose(win, a.hideBallPanel)
	return win
}

// showBallPanel 在悬浮球旁边显示精简状态面板（Windows 托盘左键）。
func (a *App) showBallPanel() {
	a.ballPanelFocus.noteShowIntended()
	a.hidePetMini()
	a.hideBallMenu()
	panel := a.ensureBallPanel()
	if panel == nil {
		return
	}
	x, y := a.ballPanelPosition()
	panel.SetPosition(x, y)
	panel.Show().Focus()
}

// toggleBallPanel 切换精简状态面板。
func (a *App) toggleBallPanel() {
	a.ballPanelFocus.noteShowIntended()
	panel := a.ensureBallPanel()
	if panel == nil {
		return
	}
	if panel.IsVisible() {
		panel.Hide()
		return
	}
	a.showBallPanel()
}

// hideBallPanel 收起精简状态面板。
func (a *App) hideBallPanel() {
	a.ballPanelFocus.noteHidden()
	a.windowMu.Lock()
	panel := a.ballPanel
	a.windowMu.Unlock()
	if panel != nil && panel.IsVisible() {
		panel.Hide()
	}
}

// ballPanelPosition 计算精简面板位置：球右侧优先，右侧放不下放左侧。
func (a *App) ballPanelPosition() (int, int) {
	bx, by, width, height := a.barAnchor()
	area := a.workAreaForPoint(bx+width/2, by+height/2)
	if area.Width <= 0 || area.Height <= 0 {
		return bx, by
	}
	return ballPanelOrigin(area, bx, by, width, ballPanelWidth, ballPanelHeight)
}

// ensurePetMini 创建迷你宠物窗口（首次使用时惰性创建）。
func (a *App) ensurePetMini() *application.WebviewWindow {
	if goos != "windows" {
		return nil
	}
	if a.wails == nil {
		// 窗口系统未装配（零值 App / 单测）：返回 nil 让调用方安全跳过。
		return nil
	}
	a.windowMu.Lock()
	if a.petMini != nil {
		win := a.petMini
		a.windowMu.Unlock()
		return win
	}
	win := a.wails.Window.NewWithOptions(petMiniOptions(a.theme))
	a.petMini = win
	a.windowMu.Unlock()
	// 同 ballPanel：WM_CLOSE 取消销毁、只隐藏，避免 a.petMini 悬空后再也弹不出。
	win.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		win.Hide()
	})
	a.petMiniFocus = a.installPopoverFocusClose(win, a.hidePetMini)
	return win
}

// showPetMini 在悬浮球旁边显示迷你宠物。
func (a *App) showPetMini() {
	a.petMiniFocus.noteShowIntended()
	a.hideBallPanel()
	a.hideBallMenu()
	win := a.ensurePetMini()
	if win == nil {
		return
	}
	x, y := a.petMiniPosition()
	win.SetPosition(x, y)
	win.Show().Focus()
}

// togglePetMini 切换迷你宠物（悬浮球左键点击）。
func (a *App) togglePetMini() {
	a.petMiniFocus.noteShowIntended()
	win := a.ensurePetMini()
	if win == nil {
		return
	}
	if win.IsVisible() {
		win.Hide()
		return
	}
	a.showPetMini()
}

// hidePetMini 收起迷你宠物。
func (a *App) hidePetMini() {
	a.petMiniFocus.noteHidden()
	a.windowMu.Lock()
	win := a.petMini
	a.windowMu.Unlock()
	if win != nil && win.IsVisible() {
		win.Hide()
	}
}

// petMiniPosition 计算迷你宠物位置：球右侧优先，右侧放不下放左侧。
func (a *App) petMiniPosition() (int, int) {
	bx, by, width, height := a.barAnchor()
	area := a.workAreaForPoint(bx+width/2, by+height/2)
	if area.Width <= 0 || area.Height <= 0 {
		return bx, by
	}
	return ballPanelOrigin(area, bx, by, width, petMiniWidth, petMiniHeight)
}

// ensureBallMenu 创建自绘右键菜单窗口（首次使用时惰性创建）。
func (a *App) ensureBallMenu() *application.WebviewWindow {
	if goos != "windows" {
		return nil
	}
	if a.wails == nil {
		// 窗口系统未装配（零值 App / 单测）：返回 nil 让调用方安全跳过。
		return nil
	}
	a.windowMu.Lock()
	if a.ballMenu != nil {
		win := a.ballMenu
		a.windowMu.Unlock()
		return win
	}
	win := a.wails.Window.NewWithOptions(ballMenuOptions(a.theme))
	a.ballMenu = win
	a.windowMu.Unlock()
	// 同 ballPanel：WM_CLOSE 取消销毁、只隐藏，避免 a.ballMenu 悬空后再也弹不出。
	win.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		win.Hide()
	})
	a.ballMenuFocus = a.installPopoverFocusClose(win, a.hideBallMenu)
	return win
}

// showBallMenu 在悬浮球左侧显示右键菜单（左侧放不下时在右侧）。
func (a *App) showBallMenu() {
	a.ballMenuFocus.noteShowIntended()
	a.hideBallPanel()
	a.hidePetMini()
	win := a.ensureBallMenu()
	if win == nil {
		return
	}
	x, y := a.ballMenuPosition()
	win.SetPosition(x, y)
	win.Show().Focus()
}

// toggleBallMenu 切换右键菜单。
func (a *App) toggleBallMenu() {
	a.ballMenuFocus.noteShowIntended()
	win := a.ensureBallMenu()
	if win == nil {
		return
	}
	if win.IsVisible() {
		win.Hide()
		return
	}
	a.showBallMenu()
}

// hideBallMenu 收起右键菜单。
func (a *App) hideBallMenu() {
	a.ballMenuFocus.noteHidden()
	a.windowMu.Lock()
	win := a.ballMenu
	a.windowMu.Unlock()
	if win != nil && win.IsVisible() {
		win.Hide()
	}
}

// ballMenuPosition 计算右键菜单位置：球左侧优先，与状态面板左右分置。
func (a *App) ballMenuPosition() (int, int) {
	bx, by, width, height := a.barAnchor()
	area := a.workAreaForPoint(bx+width/2, by+height/2)
	if area.Width <= 0 || area.Height <= 0 {
		return bx, by
	}
	return ballMenuOrigin(area, bx, by, width, height, ballMenuWidth, ballMenuHeight)
}

// PanelToggle 实现 gui.Deps.PanelToggle：切换 392px 吸附面板（旧入口）。
func (a *App) PanelToggle() { a.togglePanel() }

// PanelToggleAt 实现 gui.Deps.PanelToggleAt：把面板摆在悬浮球下方。
func (a *App) PanelToggleAt(x, y int) { a.togglePanelAtNotch(x, y) }

func (a *App) onApplicationStarted(*application.ApplicationEvent) {
	a.startedAt.Store(time.Now().UnixNano())
	// 菜单栏速度项固定宽度：数字位数变化时不再左右挤动相邻状态项。
	// 事件循环启动后状态项才真正创建，所以放在这里；monitorTokenSpeed
	// 每一轮也会再试一次，成功一次即停。
	installMenuBarFixedWidth(a.tray)
}

// trayPetFromEventData 从「pet-selected」事件里取出伙伴 id：前端发的是
// { pet: "pikachu" }，个别调用方也可能直接发字符串。
func trayPetFromEventData(data any) string {
	switch value := data.(type) {
	case map[string]any:
		if pet, ok := value["pet"].(string); ok {
			return strings.TrimSpace(pet)
		}
	case map[string]string:
		return strings.TrimSpace(value["pet"])
	case string:
		return strings.TrimSpace(value)
	}
	return ""
}

// trayCaughtFromEventData 解析「pet-caught」事件里的布尔状态。
func trayCaughtFromEventData(data any) (bool, bool) {
	switch value := data.(type) {
	case map[string]any:
		if caught, ok := value["caught"].(bool); ok {
			return caught, true
		}
	case map[string]bool:
		if caught, ok := value["caught"]; ok {
			return caught, true
		}
	case bool:
		return value, true
	}
	return false, false
}

// setTrayPet 记录当前伙伴并刷新菜单栏图标。macOS 上模板图标会随菜单栏
// 深浅色自动反相；Windows 仍用彩色应用图标（单色 glyph 在深色任务栏上看不清）。
func (a *App) setTrayPet(petID string) {
	a.trayStateMu.Lock()
	a.trayPet = petID
	caught := a.trayCaught
	a.trayStateMu.Unlock()
	saveTrayState(trayState{Pet: petID, Caught: caught})
	a.applyTrayState(petID, caught)
}

// setTrayCaught 切换「已捕获」展示：菜单栏图标变成精灵球，放出后恢复伙伴。
func (a *App) setTrayCaught(caught bool) {
	a.trayStateMu.Lock()
	a.trayCaught = caught
	petID := a.trayPet
	a.trayStateMu.Unlock()
	saveTrayState(trayState{Pet: petID, Caught: caught})
	a.applyTrayState(petID, caught)
}

func (a *App) applyTrayState(petID string, caught bool) {
	tray := a.tray
	if tray == nil {
		return
	}
	// Wails 的 SetIcon/SetTemplateIcon 内部已经 InvokeSync 回主线程；
	// 这里不要再套一层，避免事件处理器本身就在主线程时自锁。
	applyTrayIconForPetState(tray, goos, petID, caught)
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
	a.hideBallPanel()
	a.hidePetMini()
	a.hideBallMenu()
	a.windowMu.Lock()
	main := a.main
	a.windowMu.Unlock()
	if main != nil {
		main.Hide()
	}
	// 与「关闭主窗口」保持一致：主窗口不可见时悬浮球应回来。
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
		// 主窗口收起后让常驻悬浮球回来：它才是窗口隐藏期间唯一的速度指示与
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

// togglePanelAtNotch 在小屏/旧入口下把 392px 吸附面板摆在悬浮球下方。
// 悬浮球左键已改走迷你宠物（togglePetMini）；这里保留给 PanelToggleAt
// 与既有测试使用。
func (a *App) togglePanelAtNotch(x, y int) {
	// 关键竞态：点击状态项时面板先失焦（守卫已登记延迟收起）、点击后到。
	// 这里先作废那份登记再切换，收起/展开都由本次点击一次完成，不会
	// 「点了状态项，面板闪关又弹出」。
	a.panelFocus.noteShowIntended()
	panel := a.ensurePanel()
	if panel.IsVisible() {
		panel.Hide()
		return
	}
	// 优先用悬浮球窗口的实际位置：面板贴在它下方。窗口不可用时退回页面
	// 给的坐标。
	bx, by, _, bh := a.barAnchor()
	if bh <= 0 {
		bx, by, bh = x, y, ballSize
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
	a.hideBallPanel()
	a.hidePetMini()
	a.hideBallMenu()
	a.dockApply(a.dockShow.Load())
	main := a.ensureMainWindow()
	main.Show()
	main.Focus()
	// 主窗口可见时收起悬浮球：它是独立置顶窗口，挡住主窗口内容会很难受；
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

// ballPanelOptions 是 Windows 悬浮球左键弹出的面板（392×620）：加载与
// macOS 菜单栏面板同一份 panel.html（签到 / 速度 / 宠物三 tab），不透明
// 背景（panel.html 自己按 platform-opaque 铺底色）、固定尺寸、失焦由
// ballPanelFocus 守卫收起、Esc 直接收起。macOS 的菜单栏面板不走这里。
func ballPanelOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:           "ballPanel",
		Title:          "Quota Keepalive",
		Width:          ballPanelWidth,
		Height:         ballPanelHeight,
		MinWidth:       ballPanelWidth,
		MinHeight:      ballPanelHeight,
		MaxWidth:       ballPanelWidth,
		MaxHeight:      ballPanelHeight,
		Frameless:      true,
		AlwaysOnTop:    true,
		Hidden:         true,
		DisableResize:  true,
		HideOnEscape:   true,
		BackgroundType: application.BackgroundTypeSolid,
		URL:            themedURL("/panel.html", theme),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
			// 同悬浮球：不保留原生装饰，否则 Wails 会把 DWM 边框与标题栏
			// 延伸进自绘浮层的客户区（一条系统标题栏 + ×）。
			DisableFramelessWindowDecorations: true,
		},
	}
}

// petMiniOptions 是悬浮球左键弹出的迷你宠物窗口：只放伙伴、等级和当日
// Token；同样固定尺寸、不透明、失焦由 petMiniFocus 守卫收起。
func petMiniOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:           "petMini",
		Title:          "Quota Keepalive",
		Width:          petMiniWidth,
		Height:         petMiniHeight,
		MinWidth:       petMiniWidth,
		MinHeight:      petMiniHeight,
		MaxWidth:       petMiniWidth,
		MaxHeight:      petMiniHeight,
		Frameless:      true,
		AlwaysOnTop:    true,
		Hidden:         true,
		DisableResize:  true,
		HideOnEscape:   true,
		BackgroundType: application.BackgroundTypeSolid,
		URL:            themedURL("/petmini.html", theme),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
			// 同悬浮球：不保留原生装饰，否则 Wails 会把 DWM 边框与标题栏
			// 延伸进自绘浮层的客户区（一条系统标题栏 + ×）。
			DisableFramelessWindowDecorations: true,
		},
	}
}

// ballMenuOptions 是悬浮球右键的自绘菜单窗口（3 行，magpie .row-menu 风格）。
func ballMenuOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:           "ballMenu",
		Title:          "Quota Keepalive",
		Width:          ballMenuWidth,
		Height:         ballMenuHeight,
		MinWidth:       ballMenuWidth,
		MinHeight:      ballMenuHeight,
		MaxWidth:       ballMenuWidth,
		MaxHeight:      ballMenuHeight,
		Frameless:      true,
		AlwaysOnTop:    true,
		Hidden:         true,
		DisableResize:  true,
		HideOnEscape:   true,
		BackgroundType: application.BackgroundTypeSolid,
		URL:            themedURL("/ballmenu.html", theme),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
			// 同悬浮球：不保留原生装饰，否则 Wails 会把 DWM 边框与标题栏
			// 延伸进自绘浮层的客户区（一条系统标题栏 + ×）。
			DisableFramelessWindowDecorations: true,
		},
	}
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
	// 启动后延迟检查一次更新，之后每 6 小时一次；发现新版本推事件让前端提示，
	// 不自动下载（是否安装由用户点）。
	go a.monitorUpdates(ctx)
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

var (
	statusPollInterval = 10 * time.Second
	// updateCheckDelay 让更新检查避开启动时的供应商运行时预热。
	updateCheckDelay = 20 * time.Second
	// updateCheckInterval 是自动检查频率；手动「检查」不受它限制。
	updateCheckInterval = 6 * time.Hour
)

// updateNotice 决定是否要为这次检查结果提示用户：只在「发现可用的新版本且
// 尚未提示过同一版本」时返回 true，避免每 6 小时重复弹同一个横幅。
func updateNotice(status selfupdate.Status, notified string) (string, bool) {
	if !status.Available || strings.TrimSpace(status.LatestVersion) == "" {
		return notified, false
	}
	if status.LatestVersion == notified {
		return notified, false
	}
	return status.LatestVersion, true
}

func (a *App) monitorUpdates(ctx context.Context) {
	if a.updateCheck == nil || a.wails == nil {
		return
	}
	notified := ""
	timer := time.NewTimer(updateCheckDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		status, err := a.updateCheck(ctx)
		if err == nil {
			var notice bool
			notified, notice = updateNotice(status, notified)
			if notice {
				a.wails.Event.Emit("update-available", status)
			}
		}
		timer.Reset(updateCheckInterval)
	}
}

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

// tokenSpeedPublishInterval 是速度快照向界面广播的最小间隔。
// 它跟随「采样频率」设置：采样 200ms 却仍按 750ms 广播，等于把用户刚调密的
// 频率又压回去。上限 200ms 是为了不给 WebView 制造无意义的空转。
func tokenSpeedPublishInterval(sample time.Duration) time.Duration {
	const floor = 200 * time.Millisecond
	if sample < floor {
		return floor
	}
	if sample > time.Second {
		return time.Second
	}
	return sample
}

func (a *App) monitorTokenSpeed(ctx context.Context) {
	// 广播节奏跟随设置：用户把采样调到 200ms，界面就该每 200ms 收到一次。
	interval := tokenSpeedPublishInterval(a.tokenSpeedSampleInterval())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastLabel := ""
	lastTooltip := ""
	lastSignature := ""
	lastInterval := interval
	lastLabelAt := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if next := tokenSpeedPublishInterval(a.tokenSpeedSampleInterval()); next != lastInterval {
				ticker.Reset(next)
				lastInterval = next
			}
			// 状态项未就绪时这里拿不到原生指针，成功后会置位、后续直接返回。
			installMenuBarFixedWidth(a.tray)
			snapshot := a.tokenSpeed.Snapshot()
			style := a.menuBarSpeedStyle()
			label := menuBarLabel(style, snapshot.OutputTokensPerSecond)
			// 补到固定宽度：状态项会把「图标 + 标题」整体居中，位数变一位
			// 数字就左右挪几像素；补齐后数字左缘固定在这个位置上。
			label = padMenuBarLabel(label, visibleMenuBarLabel(label))
			// 内容变化才刷新；另外每 10 秒补写一次，防止某个系统版本上
			// 状态项标题被 AppKit 重排/重建后丢掉，让数字自己恢复。
			if label != lastLabel || time.Since(lastLabelAt) >= 10*time.Second {
				a.tray.SetLabel(label)
				lastLabel = label
				lastLabelAt = time.Now()
			}
			// compact 样式不显示单位，tooltip 必须把「tok/s」补回来。
			if tooltip := menuBarTooltip(style, snapshot.OutputTokensPerSecond); tooltip != lastTooltip {
				a.tray.SetTooltip(tooltip)
				lastTooltip = tooltip
			}
			// 内容没变就不广播：此前每 750ms 无条件 Emit，常驻悬浮球与面板
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

// tokenSpeedSampleInterval 返回当前生效的采样间隔；没有设置来源时用默认值。
func (a *App) tokenSpeedSampleInterval() time.Duration {
	if a.tokenSpeed != nil {
		return a.tokenSpeed.PollInterval()
	}
	if a.settings != nil {
		return a.settings().SpeedPollInterval()
	}
	return 500 * time.Millisecond
}

// menuBarSpeedStyle 返回当前菜单栏速度样式；没有设置来源时用纯文本。
func (a *App) menuBarSpeedStyle() string {
	if a.settings == nil {
		return settings.MenuBarSpeedLabel
	}
	return a.settings().MenuBarSpeedStyle()
}

// applyTokenSpeedPoll 把「速度采样频率」「目录扫描间隔」「红线」设置应用到
// 速度服务。设置来源或服务缺失时安全跳过（测试与零值 App）。
func applyTokenSpeedPoll(service *tokenspeed.Service, readSettings func() *settings.Settings) {
	if service == nil || readSettings == nil {
		return
	}
	st := readSettings()
	service.SetPollInterval(st.SpeedPollInterval())
	service.SetWalkInterval(st.SpeedWalkInterval())
	service.SetRedline(st.SpeedRedline())
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
