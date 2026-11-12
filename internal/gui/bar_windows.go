//go:build windows

package gui

import (

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"

)

var (
	user32DLL              = windows.NewLazySystemDLL("user32.dll")
	gdi32DLL               = windows.NewLazySystemDLL("gdi32.dll")
	procCreateRoundRectRgn = gdi32DLL.NewProc("CreateRoundRectRgn")
	procCreateRectRgn      = gdi32DLL.NewProc("CreateRectRgn")
	procCombineRgn         = gdi32DLL.NewProc("CombineRgn")
	procSetWindowRgn       = user32DLL.NewProc("SetWindowRgn")
	procDeleteObject       = gdi32DLL.NewProc("DeleteObject")
	procGetDpiForWindow    = user32DLL.NewProc("GetDpiForWindow")
)

// RGN_AND 是 CombineRgn 的求交模式。
const rgnAnd = 1

// barWindowOptions 是 Windows 悬浮球窗口的选项：无边框、置顶、不进任务栏，
// 等价于 macOS 菜单栏上的 QKA 状态项——一直存在、左键弹状态面板、右键出菜单、
// 按住可拖动。圆形由 shapeBarWindow 的窗口区域裁出来（Windows 10 没有 Win11
// 的 DWM 圆角，透明窗口在 Wails 下不可靠）。
//
// Min==Max 让用户拖边也改不了大小；DisableResize 必须为 false，否则
// --wails-draggable 依赖的 WS_THICKFRAME 会消失、窗口拖不动。贴边收起时
// 用 SetSize(ballDockSize, ballDockSize) 临时放宽这一约束（Wails 会同步调小 Min/Max）。
func barWindowOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:           "bar",
		Title:          "Quota Keepalive",
		Width:          ballSize,
		Height:         ballSize,
		MinWidth:       ballSize,
		MinHeight:      ballSize,
		MaxWidth:       ballSize,
		MaxHeight:      ballSize,
		Frameless:      true,
		AlwaysOnTop:    true,
		DisableResize:  false,
		Hidden:         true,
		// 半透明而不是实心：WebView2 走 DirectComposition 合成，SetWindowRgn
		// 裁出的圆角区域管不住合成层，实心底色会在球四周留下一个白色方框。
		// Translucent 让窗口背景 alpha=0、由页面决定哪儿不透明，圆外自然透明。
		BackgroundType: application.BackgroundTypeTranslucent,
		URL:            themedURL("/bar.html", theme),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
			// 无边框窗口默认会「保留原生装饰」：Wails 把 DWM 边框延伸进
			// 客户区，方形窗口上就是一块白底 + 右上角关闭按钮。以前的圆形
			// SetWindowRgn 把它挡在圆外，取消裁剪后就露出来了。
			DisableFramelessWindowDecorations: true,
		},
	}
}

// shapeBarWindow / shapeBarWindowSide 曾经把窗口裁成正圆。现在窗口 = 1.5×
// 球径，球外的光晕、彗星与火花必须可见——圆形区域会把它们切掉，所以不再
// 裁剪窗口。背景是 Translucent（DirectComposition alpha=0），球外本来就
// 透明，不会出现白方角。保留这两个函数只为不动调用点与既有测试。
func shapeBarWindow(window *application.WebviewWindow) {
	shapeBarWindowSide(window, "")
}

// shapeBarWindowSide 不再设置窗口区域；窗口存在即视为「已处理」。
func shapeBarWindowSide(window *application.WebviewWindow, _ string) bool {
	return window != nil
}
