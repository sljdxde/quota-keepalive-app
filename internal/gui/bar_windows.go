//go:build windows

package gui

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"

	"qka/internal/applog"
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
			// 必须关掉 Wails 的「无边框但保留原生装饰」模式。开启时 Wails 在
			// 每次 WM_ACTIVATE 都会调用 DwmExtendFrameIntoClientArea(1) 把 DWM
			// 边框延伸进客户区：透明页面上就是一层暗色圆角底板 + 右上角 ×，
			// 而且它在页面之下，CSS 怎么改都盖不住。在窗口创建后调用一次
			// DwmExtendFrameIntoClientArea(0) 清边距没用——下一次获得焦点时
			// Wails 会重新延伸（0.1.95 的回归正是这么来的）。
			//
			// 这个开关在 wails v3.0.0 里只参与 framelessWithDecorations() 的
			// 判断（webview_window_windows.go），不会改动透明合成或 DComp
			// 预置，因此和 BackgroundTypeTransparent 可以共存。
			DisableFramelessWindowDecorations: true,
		},
	}
}

// shapeBarWindow 把悬浮球窗口裁成正圆。
func shapeBarWindow(window *application.WebviewWindow) {
	shapeBarWindowSide(window, "")
}

// shapeBarWindowSide 把悬浮球窗口裁成正圆；贴边态（left/right/top/bottom）
// 再与半矩形求交，只保留朝向屏幕内的半圆。窗口=球径后球外不再有像素，
// 圆外区域既不占位也不抢点击。返回是否真的设置了区域（调用方据此决定是否
// 记忆「已裁剪」状态，失败时下次要重试）。
func shapeBarWindowSide(window *application.WebviewWindow, dockedSide string) bool {
	if window == nil {
		return false
	}
	hwnd := window.NativeWindow()
	if hwnd == nil {
		return false
	}
	width, height := window.Size()
	if width <= 0 {
		width = ballSize
	}
	if height <= 0 {
		height = ballSize
	}
	// SetWindowRgn 用的是物理像素，而窗口尺寸是 DIP：按当前 DPI 缩放，否则
	// 150%/200% 缩放下圆会比窗口小一圈。
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(unsafe.Pointer(hwnd)))
	if dpi == 0 {
		dpi = 96
	}
	scale := float64(dpi) / 96.0
	pxWidth := int(float64(width)*scale + 0.5)
	pxHeight := int(float64(height)*scale + 0.5)
	radius := pxWidth
	if pxHeight < radius {
		radius = pxHeight
	}
	radius /= 2
	rgn, _, err := procCreateRoundRectRgn.Call(
		0, 0, uintptr(pxWidth+1), uintptr(pxHeight+1),
		uintptr(radius*2), uintptr(radius*2),
	)
	if rgn == 0 {
		applog.Warnf("gui", "创建悬浮球窗口圆形区域失败：%v", err)
		return false
	}
	if dockedSide != "" {
		// 贴边态只保留朝向屏幕内的半圆：先算出半矩形，再把圆形与它求交到
		// 新的目标区域（不依赖 CombineRgn 的源/目标别名行为）。
		left, top, right, bottom := 0, 0, pxWidth+1, pxHeight+1
		switch dockedSide {
		case ballDockLeft:
			left = pxWidth / 2
		case ballDockRight:
			right = pxWidth/2 + 1
		case ballDockTop:
			top = pxHeight / 2
		case ballDockBottom:
			bottom = pxHeight/2 + 1
		}
		halfRgn, _, _ := procCreateRectRgn.Call(
			uintptr(left), uintptr(top), uintptr(right), uintptr(bottom),
		)
		if halfRgn == 0 {
			applog.Warnf("gui", "创建悬浮球贴边区域失败")
			_, _, _ = procDeleteObject.Call(rgn)
			return false
		}
		clipped, _, _ := procCreateRectRgn.Call(0, 0, 0, 0)
		if clipped == 0 {
			applog.Warnf("gui", "创建悬浮球裁剪区域失败")
			_, _, _ = procDeleteObject.Call(halfRgn)
			_, _, _ = procDeleteObject.Call(rgn)
			return false
		}
		if ret, _, callErr := procCombineRgn.Call(clipped, rgn, halfRgn, rgnAnd); ret == 0 {
			applog.Warnf("gui", "裁剪悬浮球贴边区域失败：%v", callErr)
			_, _, _ = procDeleteObject.Call(clipped)
			_, _, _ = procDeleteObject.Call(halfRgn)
			_, _, _ = procDeleteObject.Call(rgn)
			return false
		}
		_, _, _ = procDeleteObject.Call(halfRgn)
		_, _, _ = procDeleteObject.Call(rgn)
		rgn = clipped
	}
	// SetWindowRgn 成功时接管 rgn 的所有权，不需要（也不能）再 DeleteObject；
	// 失败时所有权仍在调用方，必须补删，否则每次失败泄漏一个 HRGN。
	if ret, _, callErr := procSetWindowRgn.Call(uintptr(unsafe.Pointer(hwnd)), rgn, 1); ret == 0 {
		applog.Warnf("gui", "设置悬浮球窗口圆形区域失败：%v", callErr)
		_, _, _ = procDeleteObject.Call(rgn)
		return false
	}
	return true
}
