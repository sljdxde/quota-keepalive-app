package gui

import (
	"sync"
	"sync/atomic"

	"qka/internal/applog"
)

// viewportReport 是页面在启动时上报的真实渲染参数：CSS 视口尺寸与
// devicePixelRatio（CSS 像素 → 物理像素的倍数）。
//
// 为什么需要它：Windows 上「界面被裁掉 / 字特别大」既可能是窗口尺寸问题，也
// 可能是 WebView2 的 DPI/缩放没跟上系统设置，仅看 Go 侧的窗口尺寸无法区分。
// 有了这三个数就能直接算出「1 CSS px 对应多少物理像素」，诊断面板也据此展示。
type viewportReport struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	DPR    float64 `json:"dpr"`
	// Scale 是页面实际生效的界面缩放（CSS zoom），1 表示未缩放。
	Scale float64 `json:"scale"`
}

var (
	// lastViewport 保存最近一次页面上报的渲染参数；未上报时 Valid 为 false。
	lastViewport atomic.Pointer[viewportReport]
	viewportOnce sync.Once
)

// ReportViewport 由页面在加载完成时调用（见 main.html），记录真实 CSS 视口、
// 设备像素比与界面缩放。只写一行 info 日志（首次），用于排查「界面展示不全」。
func (s *BoundService) ReportViewport(width, height int, dpr, scale float64) {
	if width <= 0 || height <= 0 || dpr <= 0 {
		return
	}
	if scale <= 0 {
		scale = 1
	}
	report := &viewportReport{Width: width, Height: height, DPR: dpr, Scale: scale}
	lastViewport.Store(report)
	viewportOnce.Do(func() {
		applog.Infof("gui", "渲染视口 CSS=%dx%d dpr=%.2f 缩放=%.2f（缩放<1 表示按窗口高度收缩了界面）",
			width, height, dpr, scale)
	})
}

// currentViewport 返回最近一次上报的渲染参数。
func currentViewport() viewportReport {
	if report := lastViewport.Load(); report != nil {
		return *report
	}
	return viewportReport{}
}
