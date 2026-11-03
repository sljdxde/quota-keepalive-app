//go:build windows

package gui

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"qka/internal/applog"
)

// appUserModelID 是 Windows 的应用身份标识。没有它，任务栏会把 QKA 当成
// 「未知程序」：图标不显示、右键菜单里没有「固定到任务栏」、通知也无法归类。
// 与 winres.json 里 RT_MANIFEST 的 identity 保持一致。
const appUserModelID = "com.qka.agent"

// shell32DLL 与 installer_dialog_windows.go 共用同一个包级句柄。
var procSetCurrentProcessExplicitAppUserModelID = shell32DLL.NewProc("SetCurrentProcessExplicitAppUserModelID")

// setProcessAppUserModelID 在进程启动时声明应用身份，必须早于任何窗口创建。
// 失败只记日志：没有 AUMID 时应用仍可运行，只是任务栏归类和固定功能退化。
func setProcessAppUserModelID() {
	id, err := windows.UTF16PtrFromString(appUserModelID)
	if err != nil {
		return
	}
	if _, _, callErr := procSetCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(id))); callErr != nil {
		applog.Warnf("gui", "设置 AppUserModelID 失败：%v", callErr)
	}
}
