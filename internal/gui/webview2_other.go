//go:build !windows

package gui

// CheckWebView2 在非 Windows 平台恒为 true：Wails 使用系统自带的 WebView
// （macOS 的 WKWebView、Linux 的 WebKitGTK），不存在需要单独安装的运行时。
func CheckWebView2() bool { return true }

// ensureWebView2Runtime 在非 Windows 平台是空操作，保证 gui.New 的启动路径在
// macOS/Linux 上完全不受影响（不会因为「检测」而退出进程）。
func ensureWebView2Runtime() {}
