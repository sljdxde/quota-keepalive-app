//go:build !windows

package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// applyWindowChrome 在非 Windows 平台不做任何改动：macOS 走
// MacTitleBarHiddenInset（隐藏标题栏但保留红绿灯），页面顶栏的 92px 左内边距
// 正好给红绿灯让位。
func applyWindowChrome(*application.WebviewWindowOptions) {}

// applyPanelChrome 在非 Windows 平台不改动面板选项：macOS 用 NSPanel 的半透明
// 背景与失焦即隐，观感是系统原生的。
func applyPanelChrome(*application.WebviewWindowOptions) {}
