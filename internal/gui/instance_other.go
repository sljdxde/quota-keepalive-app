//go:build !windows

package gui

// AcquireGUIInstance 在非 Windows 平台总是成功：macOS 由 LaunchServices 保证
// 同一份 .app 只会有一个进程，重复打开走 App 的 reopen 事件（见
// window_behavior_darwin.go）。
func AcquireGUIInstance() bool { return true }

// setInstanceWakeHandler 在非 Windows 平台没有跨进程唤醒通道，保持 no-op。
func setInstanceWakeHandler(func()) {}
