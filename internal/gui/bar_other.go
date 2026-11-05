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
