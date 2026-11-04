//go:build windows

package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// applyWindowChrome 在 Windows 上保留系统原生标题栏。
//
// 主窗口不采用无边框：Windows 的 frameless 窗口在 Wails v3 下需要额外的
// 非客户区处理，一旦出问题就是「窗口创建了但看不见」，用户表现为「双击/右键
// 都打不开应用」。菜单栏状态项（常驻刘海）已经由独立的置顶小窗承载，主窗口
// 用原生标题栏最稳，关闭/最小化/最大化都交给系统。
func applyWindowChrome(*application.WebviewWindowOptions) {}

// applyPanelChrome 让 Windows 上的吸附面板用不透明背景并关掉 Wails 原生的
// 「失焦即隐」：Windows 10 没有 Win11 的 DWM 半透明背景（会渲染成纯黑或全
// 透明），而原生 HideOnFocusLost 是「失焦立即隐藏」，既处理不了「点刘海收
// 面板时面板先失焦、刘海点击后到」的竞态，也会被 Show+Focus 的焦点抖动触发
// 成「面板弹出后立刻自己消失」。点击外部收起改由 app.go 的 panelFocusGuard
// 负责（监听 WindowLostFocus，延迟复核后再 hidePanel），这里必须保持关闭，
// 否则原生立即隐藏会和守卫的延迟收起互相打架。
func applyPanelChrome(opts *application.WebviewWindowOptions) {
	opts.BackgroundType = application.BackgroundTypeSolid
	opts.HideOnFocusLost = false
	// 放宽最小高度：小屏（如 1280×720，工作区只有 680 高）上面板展开到 620
	// 会顶出屏幕底边，底部内容被裁掉。显示时由 fitPanelHeightToWorkArea 按
	// 工作区收敛；用户仍然不能拖拽改变尺寸（DisableResize 为 true）。
	opts.MinHeight = panelMinFittedHeight
}
