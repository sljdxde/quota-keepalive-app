//go:build !windows

package gui

// setProcessAppUserModelID 在非 Windows 平台是 no-op：macOS 的应用身份由
// .app bundle 的 Info.plist 决定。
func setProcessAppUserModelID() {}
