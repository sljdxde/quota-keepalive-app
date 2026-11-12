//go:build windows

package gui

import (
	"unsafe"

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
// 按住可拖动。窗口背景走 BackgroundTypeTransparent（WebView2 alpha=0、不请求
// 系统材质），球外完全透明；Windows 10 没有 Win11 的 DWM 圆角与背景材质，
// 半透明（Translucent）会退化成一块覆盖整窗的模糊白底，见下方字段注释。
//
// Min==Max 让用户拖边也改不了大小；DisableResize 必须为 false，否则
// --wails-draggable 依赖的 WS_THICKFRAME 会消失、窗口拖不动。贴边收起时
// 用 SetSize(ballDockSize, ballDockSize) 临时放宽这一约束（Wails 会同步调小 Min/Max）。
func barWindowOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:          "bar",
		Title:         "Quota Keepalive",
		Width:         ballSize,
		Height:        ballSize,
		MinWidth:      ballSize,
		MinHeight:     ballSize,
		MaxWidth:      ballSize,
		MaxHeight:     ballSize,
		Frameless:     true,
		AlwaysOnTop:   true,
		DisableResize: false,
		Hidden:        true,
		// 全透明而不是半透明（Transparent ≠ Translucent）：这两个值都让
		// WebView2 的默认背景变成 alpha=0、页面自己决定哪儿不透明，但
		// Translucent 会额外向系统要一层「背景材质」——Win11 22H2+ 走
		// DWMWA_SYSTEMBACKDROP_TYPE（Auto 由系统挑 Mica），Win10 没有这个
		// 属性，Wails 退回 legacy ACCENT_ENABLE_BLURBEHIND。两条路都会把整个
		// 窗口铺成一块磨砂圆角矩形，也就是球四周那圈「背景框」；它在页面
		// 之下，CSS 的 background: transparent 盖不住，怎么改页面都去不掉。
		// Transparent 只写 alpha=0、不请求任何系统材质：窗口只剩页面真正画
		// 出来的像素，球外（含光晕与阴影之外）一律透明。
		BackgroundType: application.BackgroundTypeTransparent,
		URL:            themedURL("/bar.html", theme),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	}
}

// clearDwmFrame 把 DWM 的「延伸到客户区」边距清零，去掉无边框窗口上的
// 系统边框与右上角关闭按钮。
//
// 为什么不用 Wails 的 DisableFramelessWindowDecorations：它同时改动透明
// 窗口的预置（WS_EX_NOREDIRECTIONBITMAP + DirectComposition）。这里只把
// DWM 边距清零，透明合成路径完全不动，风险最小。
var (
	dwmapiDLL                        = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmExtendFrameIntoClientArea = dwmapiDLL.NewProc("DwmExtendFrameIntoClientArea")
)

type dwmMargins struct {
	Left, Right, Top, Bottom int32
}

func clearDwmFrame(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	hwnd := window.NativeWindow()
	if hwnd == nil {
		return
	}
	margins := dwmMargins{}
	_, _, _ = procDwmExtendFrameIntoClientArea.Call(
		uintptr(unsafe.Pointer(hwnd)),
		uintptr(unsafe.Pointer(&margins)),
	)
}

// shapeBarWindow / shapeBarWindowSide 曾经把窗口裁成正圆。现在窗口 = 1.9×
// 球径（122 对 64），球外的光晕、彗星与火花必须可见——圆形区域会把它们切掉，
// 所以不再裁剪窗口。背景是 Transparent（DirectComposition alpha=0），球外
// 本来就透明，不会出现白方角。保留这两个函数只为不动调用点与既有测试。
func shapeBarWindow(window *application.WebviewWindow) {
	shapeBarWindowSide(window, "")
}

// shapeBarWindowSide 不再设置窗口区域；窗口存在即视为「已处理」。
func shapeBarWindowSide(window *application.WebviewWindow, _ string) bool {
	return window != nil
}
