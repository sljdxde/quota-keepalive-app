//go:build windows

package gui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// 弹窗样式：只有「确定」、错误图标、置前。用于启动前就注定失败的场景
	// （如本次构建没内嵌运行时），此时还没有任何 QKA 窗口可承载提示。
	fatalDialogOK            = 0x00000000 // MB_OK
	fatalDialogIconError     = 0x00000010 // MB_ICONERROR
	fatalDialogSetForeground = 0x00010000 // MB_SETFOREGROUND
	fatalDialogTopmost       = 0x00040000 // MB_TOPMOST
)

var procFatalDialogMessageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

// ShowFatalDialog 弹出 Windows 原生错误弹窗并等用户确认。
// windowsgui 程序没有可见的 stderr：启动阶段的致命错误必须用原生弹窗，
// 否则用户只会看到「双击没反应」或「安装失败」而拿不到原因。
func ShowFatalDialog(title, body string) {
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	bodyPtr, err := windows.UTF16PtrFromString(body)
	if err != nil {
		return
	}
	_, _, _ = procFatalDialogMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(bodyPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		fatalDialogOK|fatalDialogIconError|fatalDialogSetForeground|fatalDialogTopmost,
	)
}
