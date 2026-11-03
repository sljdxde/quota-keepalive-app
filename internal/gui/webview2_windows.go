//go:build windows

package gui

import (
	"log"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"qka/internal/openurl"
)

// webView2ExitCode 是缺少 WebView2 运行时时的进程退出码。QKA 的 CLI 契约用 2
// 表示启动失败（见 internal/cli），因此与「未知命令/启动失败」保持一致；关键是
// 非 0，脚本和用户才能分辨「正常退出」与「静默消失」。
const webView2ExitCode = 2

var (
	// exitAfterWebView2Prompt 默认直接结束进程；测试可替换，避免杀掉测试进程。
	exitAfterWebView2Prompt = os.Exit

	// webView2ReadRegistry 读取真实注册表；单测注入假实现即可覆盖判定逻辑。
	webView2ReadRegistry webView2RegistryReadFunc = readWebView2RegistryValue

	// webView2PromptConfirm 弹出原生「是/否」提示；测试可替换，避免真实弹窗
	// 阻塞自动化。默认实现见 webView2Box。
	webView2PromptConfirm = webView2Confirm
)

// CheckWebView2 报告本机是否安装了可用的 WebView2 运行时。检测方式为读取
// EdgeUpdate 注册表（系统级/用户级、32/64 位视图、Clients\pv 与
// ClientState\EBWebView）。它只读注册表、不创建 Wails 应用，因此可以在
// application.New 之前调用（那个调用每进程只能有一次）。
func CheckWebView2() bool {
	return webView2Installed(webView2ReadRegistry)
}

// ensureWebView2Runtime 在运行时缺失时用 Windows 原生弹窗说明原因：用户点
// 「是」则在默认浏览器打开官方下载页，随后以非 0 退出码结束进程。它在
// application.New 之前、GUI 事件循环之外执行，不会阻塞事件循环。
func ensureWebView2Runtime() {
	if CheckWebView2() {
		return
	}
	log.Printf("qka: 未检测到 Microsoft Edge WebView2 运行时，窗口无法启动；请从 %s 安装", webView2DownloadURL)
	if webView2PromptConfirm() {
		if err := openurl.Open(webView2DownloadURL); err != nil {
			log.Printf("qka: 打开 WebView2 下载页面失败：%v", err)
		}
	}
	exitAfterWebView2Prompt(webView2ExitCode)
}

// readWebView2RegistryValue 按指定根键与视图读取一个字符串值；键或值不存在时
// 返回 false（缺失运行时是正常情况，不当作错误向上抛）。
func readWebView2RegistryValue(root webView2RegistryRoot, path, name string, view32 bool) (string, bool) {
	var rootKey registry.Key
	switch root {
	case webView2HKLM:
		rootKey = registry.LOCAL_MACHINE
	case webView2HKCU:
		rootKey = registry.CURRENT_USER
	default:
		return "", false
	}
	access := uint32(registry.QUERY_VALUE)
	if view32 {
		access |= registry.WOW64_32KEY
	} else {
		access |= registry.WOW64_64KEY
	}
	key, err := registry.OpenKey(rootKey, path, access)
	if err != nil {
		return "", false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return "", false
	}
	return value, true
}

const (
	webView2MBYesNo         = 0x00000004 // MB_YESNO
	webView2MBIconError     = 0x00000010 // MB_ICONERROR
	webView2MBSetForeground = 0x00010000 // MB_SETFOREGROUND
	webView2MBTopmost       = 0x00040000 // MB_TOPMOST
	webView2IDYes           = 6          // IDYES
)

var procWebView2MessageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

// webView2Box 弹出 Windows 原生 MessageBoxW（错误图标 + 是/否），返回按钮 ID。
// hwnd 传 0：此时还没有任何 QKA 窗口，弹窗挂在桌面上。
func webView2Box(title, body string) int {
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	bodyPtr, err := windows.UTF16PtrFromString(body)
	if err != nil {
		return 0
	}
	result, _, _ := procWebView2MessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(bodyPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		webView2MBYesNo|webView2MBIconError|webView2MBSetForeground|webView2MBTopmost,
	)
	return int(result)
}

// webView2Confirm 弹出缺失提示，用户点「是」返回 true。
func webView2Confirm() bool {
	title, body := webView2Notice()
	return webView2Box(title, body) == webView2IDYes
}
