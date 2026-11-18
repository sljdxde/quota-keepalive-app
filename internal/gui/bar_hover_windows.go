//go:build windows

package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// barHoverCursor 是贴边态 hover 呼出的物理坐标探测。抽出来是为了在测试里
// 替换（真实实现读系统光标与窗口物理矩形，测试注入假值跑纯判定）。
var barHoverCursor = func(bar *application.WebviewWindow) (rect wRect, mx, my int, ok bool) {
	if bar == nil {
		return rect, 0, 0, false
	}
	// 窗口位置/尺寸是 DIP，物理矩形用 Wails 的全局换算（内部按光标所在屏
	// 的 DPI 取整）。窗口半出屏时换算不受影响：线性映射。
	px, py := bar.Position()
	w, h := bar.Size()
	dip := application.Rect{X: px, Y: py, Width: w, Height: h}
	phys := application.DipToPhysicalRect(dip)
	mx, my, ok = w32.GetCursorPos()
	if !ok {
		return wRect{X: phys.X, Y: phys.Y, W: phys.Width, H: phys.Height}, 0, 0, false
	}
	return wRect{X: phys.X, Y: phys.Y, W: phys.Width, H: phys.Height}, mx, my, true
}
