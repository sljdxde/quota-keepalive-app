//go:build windows

package gui

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// errorCancelled 是 ERROR_CANCELLED（用户点了取消）对应的 HRESULT：
// Wails 的目录选择框把「取消」也当成失败返回，不能把它展示成故障。
const errorCancelled = 0x800704C7

// isDialogCancel 识别「用户取消」。cfd 的两条取消路径会给出不同形态：Show 返回
// OleError(HRESULT ERROR_CANCELLED)，GetResult 返回 "cancelled by user"。
func isDialogCancel(err error) bool {
	if err == nil {
		return false
	}
	var oleErr *ole.OleError
	if errors.As(err, &oleErr) && uint32(oleErr.Code()) == errorCancelled {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cancel") || strings.Contains(message, "取消")
}

// wailsFolderDialog 用 Wails 的通用文件对话框选目录。startDir 为空时完全不设
// 初始目录：Windows 的 cfd 实现会在 DialogConfig.apply 里 os.Stat 初始目录，
// 不存在就直接返回错误、连对话框都不创建（安装前默认目录还不存在的场景）。
//
// SetButtonText 不参与 Windows 对话框的构造（Wails 只把 title/role/filters/
// folder 传给 cfd），设了也不会生效，所以这里不设。
func wailsFolderDialog(app *application.App, window *application.WebviewWindow, startDir string) (string, error) {
	dialog := app.Dialog.OpenFile().
		SetTitle("选择 QKA 的安装位置").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		AttachToWindow(window)
	if startDir != "" {
		dialog.SetDirectory(startDir)
	}
	return dialog.PromptForSingleSelection()
}

// browseInstallDir 是浏览安装位置的主流程：
//  1. 把初始目录收敛到最近存在的父目录（不存在的路径会让 Wails 对话框直接失败）；
//  2. 先试带初始目录的调用，失败再试不设初始目录的最简调用；
//  3. 都不行就退回 shell32 原生 SHBrowseForFolderW。
//
// 用户取消一律归一成 ("", nil)，页面据此保持原输入、不报错。
func browseInstallDir(app *application.App, window *application.WebviewWindow, current string) (string, error) {
	startDir := resolveStartDir(current, dirExists)

	picked, err := wailsFolderDialog(app, window, startDir)
	if err == nil {
		return strings.TrimSpace(picked), nil
	}
	if isDialogCancel(err) {
		return "", nil
	}

	// 带初始目录失败时不放弃：初始目录的边界情况（权限、被删、长路径）都可能
	// 让 cfd 在构造阶段失败，不设初始目录的调用形态没有这个风险。
	if startDir != "" {
		picked, retryErr := wailsFolderDialog(app, window, "")
		if retryErr == nil {
			return strings.TrimSpace(picked), nil
		}
		if isDialogCancel(retryErr) {
			return "", nil
		}
		err = retryErr
	}

	// 最后兜底：Wails/cfd 在这个环境里彻底不可用时直接调 shell32。
	nativePicked, nativeErr := browseNativeFolder(window)
	if nativeErr != nil {
		return "", fmt.Errorf("打开目录选择框失败：%w（原生兜底也失败：%v）", err, nativeErr)
	}
	return strings.TrimSpace(nativePicked), nil
}

// BROWSEINFO.ulFlags 取值（shlobj.h）。BIF_USENEWUI 已包含 BIF_NEWDIALOGSTYLE
// 与 BIF_EDITBOX：新版对话框、可新建文件夹、可粘贴路径。
const (
	bifReturnOnlyFSDirs = 0x0001
	bifEditBox          = 0x0010
	bifNewDialogStyle   = 0x0040
	bifUseNewUI         = bifNewDialogStyle | bifEditBox
)

// browseInfoW 是 shell32 的 BROWSEINFOW 结构。字段顺序与平台 ABI 一致：
// 前四个是指针，ulFlags 为 UINT，lpfn/lParam 为指针，iImage 为 int。
type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

var (
	shell32DLL               = windows.NewLazySystemDLL("shell32.dll")
	ole32DLL                 = windows.NewLazySystemDLL("ole32.dll")
	procSHBrowseForFolderW   = shell32DLL.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32DLL.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree        = ole32DLL.NewProc("CoTaskMemFree")
)

// browseNativeFolder 是 Wails 对话框不可用时的原生兜底：直接弹 shell32 的
// SHBrowseForFolderW。对话框要跑自己的消息循环，必须在 UI 线程上弹，所以用
// application.InvokeSync 派发；window 为 nil 时退回无 owner 的桌面模态。
// 返回值空串表示用户取消。
func browseNativeFolder(window *application.WebviewWindow) (string, error) {
	if application.Get() == nil {
		return "", errors.New("应用尚未启动，无法打开目录选择框")
	}
	title, err := windows.UTF16PtrFromString("选择 QKA 的安装位置")
	if err != nil {
		return "", err
	}
	var owner uintptr
	if window != nil {
		if handle := window.NativeWindow(); handle != nil {
			owner = uintptr(handle)
		}
	}
	displayName := make([]uint16, windows.MAX_PATH)
	info := browseInfoW{
		hwndOwner:      owner,
		pszDisplayName: &displayName[0],
		lpszTitle:      title,
		ulFlags:        bifReturnOnlyFSDirs | bifUseNewUI,
	}

	var pidl uintptr
	var picked string
	application.InvokeSync(func() {
		folder, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
		if folder == 0 {
			return // 取消（SHBrowseForFolder 取消与错误都返回 NULL）
		}
		pidl = folder
		path := make([]uint16, windows.MAX_PATH)
		if ok, _, _ := procSHGetPathFromIDListW.Call(folder, uintptr(unsafe.Pointer(&path[0]))); ok != 0 {
			picked = windows.UTF16ToString(path)
		}
		procCoTaskMemFree.Call(folder)
	})
	if pidl == 0 {
		return "", nil
	}
	if picked == "" {
		return "", errors.New("无法读取所选目录的路径（路径可能超过 260 个字符）")
	}
	return picked, nil
}
