//go:build windows

package applog

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// MessageBoxW 的参数：确定按钮 + 错误图标 + 置前 + 置顶。panic 后进程马上就
// 退出，提示窗口必须出现在最前面，用户才看得到。
const (
	mbOK            = 0x00000000 // MB_OK
	mbIconError     = 0x00000010 // MB_ICONERROR
	mbSetForeground = 0x00010000 // MB_SETFOREGROUND
	mbTopmost       = 0x00040000 // MB_TOPMOST
)

var procMessageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

// showCrashNotice 弹 Windows 原生提示。windowsgui 没有控制台，panic 后进程
// 直接消失，用户唯一能看到的线索就是这条弹窗里的日志目录。
func showCrashNotice(logDir string) {
	title, err := windows.UTF16PtrFromString("QKA — Quota Keepalive")
	if err != nil {
		return
	}
	body, err := windows.UTF16PtrFromString(
		"QKA 遇到未处理的异常，程序即将退出。\n\n诊断日志在：\n" + logDir)
	if err != nil {
		return
	}
	_, _, _ = procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(body)),
		uintptr(unsafe.Pointer(title)),
		mbOK|mbIconError|mbSetForeground|mbTopmost)
}
