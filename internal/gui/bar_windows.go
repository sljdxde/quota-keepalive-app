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
// 按住可拖动。圆形由 shapeBarWindow 的窗口区域裁出来（Windows 10 没有 Win11
// 的 DWM 圆角，透明窗口在 Wails 下不可靠）。
//
// Min==Max 让用户拖边也改不了大小；DisableResize 必须为 false，否则
// --wails-draggable 依赖的 WS_THICKFRAME 会消失、窗口拖不动。贴边收起时
// 用 SetSize(44,44) 临时放宽这一约束（Wails 会同步调小 Min/Max）。
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
		BackgroundType: application.BackgroundTypeSolid,
		URL:            themedURL("/bar.html", theme),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	}
}

// shapeBarWindow 把悬浮球窗口裁成正圆。
func shapeBarWindow(window *application.WebviewWindow) {
	shapeBarWindowSide(window, "")
}

// shapeBarWindowSide 把悬浮球窗口裁成圆；贴边态（left/right）再与半矩形求交，
// 只保留朝向屏幕内的半圆。这样即使贴边的一侧还有另一台显示器，被藏起来的
// 那一半也不会出现在隔壁屏上。返回是否真的设置了区域（调用方据此决定
// 是否记忆「已裁剪」状态，失败时下次要重试）。
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
		// 左贴边露出窗口的右半，右贴边露出左半。先算出半矩形，再把圆形与它
		// 求交到新的目标区域（不依赖 CombineRgn 的源/目标别名行为）。
		half := pxWidth / 2
		left, right := 0, half+1
		if dockedSide == ballDockLeft {
			left, right = half, pxWidth+1
		}
		halfRgn, _, _ := procCreateRectRgn.Call(
			uintptr(left), 0, uintptr(right), uintptr(pxHeight+1),
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
