//go:build !windows

package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// barWindowOptions 在非 Windows 平台不使用：macOS 本身就是菜单栏应用，状态项
// 由系统菜单栏承载（SetTemplateIcon + SetLabel），不需要额外窗口。
func barWindowOptions(string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{}
}

// shapeBarWindow 在非 Windows 平台是 no-op：没有悬浮球窗口需要裁圆。
func shapeBarWindow(*application.WebviewWindow) {}

// shapeBarWindowSide 在非 Windows 平台是 no-op，恒返回「没设置」。
func shapeBarWindowSide(*application.WebviewWindow, string) bool { return false }

// hoverCursorStub 与 barHoverCursor 是 darwin 编译占位：app.go 的
// barHoverCheck 在非 Windows 上先按 goos 短路返回，永远走不到这两个符号；
// 它们只为让 darwin（无 w32/winuser）编译通过。真实实现见 bar_hover_windows.go。
var barHoverCursor = func(bar *application.WebviewWindow) (rect wRect, mx, my int, ok bool) {
	return rect, 0, 0, false
}
