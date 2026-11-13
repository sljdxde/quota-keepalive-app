//go:build !darwin || ios

package gui

import "github.com/wailsapp/wails/v3/pkg/application"

// installMenuBarFixedWidth 只在 macOS 上需要：其它平台的托盘标签宽度由
// 系统自己决定（Windows 托盘本来就不显示 label 文本），这里保持空实现。
func installMenuBarFixedWidth(*application.SystemTray) {}

// padMenuBarLabel 只在 macOS 上需要：其它平台不按像素宽度渲染菜单栏文本。
func padMenuBarLabel(label, _ string) string { return label }
