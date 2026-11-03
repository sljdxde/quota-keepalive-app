//go:build !windows

package gui

import "testing"

// 非 Windows 平台没有独立的 WebView2 运行时，检测必须恒为 true，否则会在
// macOS/Linux 上把正常的启动路径挡掉。
func TestCheckWebView2TrueOffWindows(t *testing.T) {
	if !CheckWebView2() {
		t.Fatal("非 Windows 平台 CheckWebView2 应恒为 true")
	}
}

// 启动守卫在非 Windows 平台必须是空操作：不 panic、不退出进程。
func TestEnsureWebView2RuntimeNoopOffWindows(t *testing.T) {
	ensureWebView2Runtime()
}
