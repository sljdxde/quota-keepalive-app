package gui

import (
	"runtime"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Windows 主窗口保留系统原生标题栏（applyWindowChrome 故意不改成无边框：
// Wails v3 的 frameless 在 Windows 上出错时表现为「窗口创建了但看不见」，
// 见 window_chrome_windows.go）；页面顶栏只负责应用内导航。其它平台保持各自
// 的既有形态。
func TestMainWindowChromeFollowsPlatform(t *testing.T) {
	opts := mainOptions("system")
	if runtime.GOOS == "windows" {
		if opts.Frameless {
			t.Fatal("Windows 主窗口应保留原生标题栏，不应无边框")
		}
		if opts.Windows.NonClientRegionSupport {
			t.Error("Windows 主窗口不应开 NonClientRegionSupport")
		}
		return
	}
	if opts.Frameless {
		t.Fatal("非 Windows 平台不应改为无边框窗口")
	}
	if opts.Mac.TitleBar != application.MacTitleBarHiddenInset {
		t.Errorf("macOS 仍应使用隐藏内嵌标题栏，实际 %+v", opts.Mac.TitleBar)
	}
}

// 平台真的销毁主窗口后，ensureMainWindow 必须重建，而不是复用已失效指针
// 导致 Show() 静默失败、主界面再也打不开。
func TestMainWindowRebuildsAfterPlatformDestroy(t *testing.T) {
	app := New(Deps{})
	if app.mainWindowAlive() {
		t.Fatal("未创建主窗口时不应报告存活")
	}

	first := app.ensureMainWindow()
	if !app.mainWindowAlive() {
		t.Fatal("创建后的主窗口应存活")
	}

	// Wails 默认关闭监听销毁窗口时会把它移出窗口管理器，用同一路径模拟。
	app.wails.Window.Remove(first.ID())
	if app.mainWindowAlive() {
		t.Fatal("被移出窗口管理器后不应再报告存活")
	}

	second := app.ensureMainWindow()
	if second.ID() == first.ID() {
		t.Fatal("窗口已销毁时必须重建，不能复用旧指针")
	}
	if !app.mainWindowAlive() {
		t.Fatal("重建后的主窗口应存活")
	}
}

// 主窗口关闭只隐藏、不销毁：QKA 常驻菜单栏/托盘，必须能反复打开。
func TestMainWindowCloseHidesInsteadOfDestroying(t *testing.T) {
	app := New(Deps{})
	main := app.ensureMainWindow()
	app.showMain()

	main.HandleWindowEvent(uint(events.Common.WindowClosing))
	if !app.mainWindowAlive() {
		t.Fatal("关闭主窗口后窗口仍应存活（只隐藏）")
	}
	if main.IsVisible() {
		t.Fatal("关闭主窗口后应隐藏")
	}
	if app.ensureMainWindow() != main {
		t.Fatal("隐藏的主窗口应被复用，不需要重建")
	}
}

// 非 macOS 上顶栏就是标题栏：去掉 macOS 红绿灯安全区，并渲染页面内窗口按钮。
func TestMainHTMLTopbarAdaptsToCustomTitleBar(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{
		"platform-native-titlebar",
		"html.platform-native-titlebar .topbar { padding-left: 14px; }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("main.html 缺少非 macOS 顶部栏适配: %q", want)
		}
	}
}
