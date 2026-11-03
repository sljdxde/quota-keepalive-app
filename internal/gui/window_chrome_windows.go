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

// applyPanelChrome 让 Windows 上的吸附面板用不透明背景并关掉「失焦即隐」：
// Windows 10 没有 Win11 的 DWM 半透明背景（会渲染成纯黑或全透明），而
// HideOnFocusLost 在 Windows 上依赖焦点事件，面板弹出后常常立刻自己消失，
// 表现就是「点托盘/刘海没反应」。收起由再点一次或按 Esc 负责。
func applyPanelChrome(opts *application.WebviewWindowOptions) {
	opts.BackgroundType = application.BackgroundTypeSolid
	opts.HideOnFocusLost = false
}
