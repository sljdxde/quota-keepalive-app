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
	procSetWindowRgn       = user32DLL.NewProc("SetWindowRgn")
	procDeleteObject       = gdi32DLL.NewProc("DeleteObject")
	procGetDpiForWindow    = user32DLL.NewProc("GetDpiForWindow")
)

// barWindowOptions 是 Windows 常驻刘海窗口的选项：无边框、置顶、不进任务栏，
// 放在主屏顶部居中，等价于 macOS 菜单栏上的 QKA 状态项：一直存在、点击弹面板、
// 右键出菜单、按住可拖动。圆角由 shapeBarWindow 的窗口区域裁出来（Windows 10
// 没有 Win11 的 DWM 圆角）。
func barWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:        "bar",
		Title:       "Quota Keepalive",
		Width:       barWidth,
		Height:      barHeight,
		MinWidth:    barWidth,
		MinHeight:   barHeight,
		MaxWidth:    barWidth,
		MaxHeight:   barHeight,
		Frameless:   true,
		AlwaysOnTop: true,
		// 必须允许 resize 才能让 Wails 的 --wails-draggable 拖动生效：它的实现是
		// 发 WM_NCLBUTTONDOWN(HTCAPTION)，没有 WS_THICKFRAME 的窗口不会进入移动
		// 循环。这里用 Min==Max 的硬约束把尺寸锁死，用户拖边也改不了大小。
		DisableResize:  false,
		Hidden:         true,
		BackgroundType: application.BackgroundTypeSolid,
		URL:            "/bar.html",
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	}
}

// barOrigin 返回常驻刘海应放的屏幕坐标：主屏顶部居中（吸附在顶部中间）。
func barOrigin(screen *application.Screen) (int, int) {
	if screen == nil {
		return 0, 0
	}
	area := screen.WorkArea
	if area.Width <= 0 {
		area = screen.Bounds
	}
	x := area.X + (area.Width-barWidth)/2
	y := area.Y + barTopGap
	if y < area.Y {
		y = area.Y
	}
	return x, y
}

// shapeBarWindow 把刘海窗口裁成圆角胶囊。CreateRoundRectRgn 的椭圆宽高是
// 直径（radius*2），SetWindowRgn 之后窗口在该区域外不参与绘制与命中测试，
// 因此四个角是真正的圆角，而不是画在矩形窗口里的假圆角。
func shapeBarWindow(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	hwnd := window.NativeWindow()
	if hwnd == nil {
		return
	}
	// SetWindowRgn 用的是物理像素，而窗口尺寸是 DIP：按当前 DPI 缩放，否则
	// 150%/200% 缩放下圆角区域会小于窗口，把右侧和底部裁掉。
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(unsafe.Pointer(hwnd)))
	if dpi == 0 {
		dpi = 96
	}
	scale := float64(dpi) / 96.0
	pxWidth := int(float64(barWidth)*scale + 0.5)
	pxHeight := int(float64(barHeight)*scale + 0.5)
	pxRadius := int(float64(barCornerRadius)*scale + 0.5)
	rgn, _, err := procCreateRoundRectRgn.Call(
		0, 0, uintptr(pxWidth+1), uintptr(pxHeight+1),
		uintptr(pxRadius*2), uintptr(pxRadius*2),
	)
	if rgn == 0 {
		applog.Warnf("gui", "创建刘海窗口圆角区域失败：%v", err)
		return
	}
	// SetWindowRgn 成功时接管 rgn 的所有权，不需要（也不能）再 DeleteObject；
	// 失败时所有权仍在调用方，必须补删，否则每次失败泄漏一个 HRGN。
	if ret, _, callErr := procSetWindowRgn.Call(uintptr(unsafe.Pointer(hwnd)), rgn, 1); ret == 0 {
		applog.Warnf("gui", "设置刘海窗口圆角失败：%v", callErr)
		_, _, _ = procDeleteObject.Call(rgn)
	}
}
