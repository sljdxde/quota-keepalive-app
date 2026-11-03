//go:build !windows

package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// barWindowOptions 在非 Windows 平台不使用：macOS 本身就是菜单栏应用，状态项
// 由系统菜单栏承载（SetTemplateIcon + SetLabel），不需要额外窗口。
func barWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{}
}

// barOrigin 在非 Windows 平台没有常驻刘海窗口。
func barOrigin(*application.Screen) (int, int) { return 0, 0 }

// shapeBarWindow 在非 Windows 平台是 no-op：没有常驻刘海窗口需要裁圆角。
func shapeBarWindow(*application.WebviewWindow) {}
