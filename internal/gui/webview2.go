package gui

import (
	"strconv"
	"strings"
)

// Windows 上 QKA.exe 是 windowsgui 程序：WebView2 运行时缺失时 Wails 创建窗口
// 会 fatal 后 os.Exit(1)，而 stderr 不可见，用户只看到「双击毫无反应」。所以启动
// GUI 前先检查运行时，缺失时用 Windows 原生弹窗说明原因并给出下载地址，再以非 0
// 退出码结束，避免静默消失。
//
// 本文件只放跨平台、可在 macOS/Linux 上单测的纯逻辑（注册表候选位置、版本解析、
// 弹窗文案）；真实注册表读取与 MessageBoxW 在 webview2_windows.go。

const (
	// webView2DownloadURL 是微软 WebView2 运行时 Evergreen 的官方下载地址。
	webView2DownloadURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

	// webView2ClientID 是 EdgeUpdate 里 WebView2 Runtime（稳定通道）的固定 GUID。
	webView2ClientID = "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

	// EdgeUpdate 会在多个位置登记运行时：Clients 下是安装信息（pv 为版本号），
	// ClientState 下是状态（EBWebView 为安装目录）；系统级/用户级安装、32/64 位
	// 注册表视图各不相同，都要查。
	webView2ClientsKey       = `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webView2ClientID
	webView2ClientStateKey   = `SOFTWARE\Microsoft\EdgeUpdate\ClientState\` + webView2ClientID
	webView2VersionValue     = "pv"
	webView2ClientStateValue = "EBWebView"

	// webView2MinimumVersion 与 Wails 的 webviewloader 一致（86.0.616.0）：低于
	// 该版本的运行时 Wails 同样视为缺失，提前判定才能给出同样的提示。
	webView2MinimumVersion = "86.0.616.0"
)

// webView2RegistryRoot 是注册表根键的简写。
type webView2RegistryRoot string

const (
	webView2HKLM webView2RegistryRoot = "HKLM"
	webView2HKCU webView2RegistryRoot = "HKCU"
)

// webView2RegistryValue 描述一次注册表读取：根键、相对路径、值名，以及是否按
// 32 位视图（WOW6432Node）读取。EdgeUpdate 自身是 32 位程序，系统级安装最常见
// 落在 32 位视图；用户级安装则可能落在任一视图。
type webView2RegistryValue struct {
	root   webView2RegistryRoot
	path   string
	name   string
	view32 bool
}

// webView2RegistryReadFunc 读取一个字符串注册表值；第二个返回值为 false 表示
// 键或值不存在。真实实现见 webview2_windows.go，单测注入假实现。
type webView2RegistryReadFunc func(root webView2RegistryRoot, path, name string, view32 bool) (string, bool)

// webView2RegistryCandidates 是运行时可能登记的位置，按命中概率排序。前四条查
// Clients\pv（安装器写版本号），后四条查 ClientState\EBWebView（Wails 自身检测
// 读取的位置，值形如 ...\EBWebView\141.0.3537.71）。
var webView2RegistryCandidates = []webView2RegistryValue{
	{webView2HKLM, webView2ClientsKey, webView2VersionValue, true},
	{webView2HKLM, webView2ClientsKey, webView2VersionValue, false},
	{webView2HKCU, webView2ClientsKey, webView2VersionValue, true},
	{webView2HKCU, webView2ClientsKey, webView2VersionValue, false},
	{webView2HKLM, webView2ClientStateKey, webView2ClientStateValue, true},
	{webView2HKLM, webView2ClientStateKey, webView2ClientStateValue, false},
	{webView2HKCU, webView2ClientStateKey, webView2ClientStateValue, true},
	{webView2HKCU, webView2ClientStateKey, webView2ClientStateValue, false},
}

// webView2Installed 用 read 依次读取候选位置，任一返回可用版本即视为已安装。
func webView2Installed(read webView2RegistryReadFunc) bool {
	for _, candidate := range webView2RegistryCandidates {
		raw, ok := read(candidate.root, candidate.path, candidate.name, candidate.view32)
		if !ok {
			continue
		}
		if webView2VersionValid(webView2VersionFromValue(raw)) {
			return true
		}
	}
	return false
}

// webView2VersionFromValue 从注册表值里取出裸版本号。pv 本身就是版本字符串；
// EBWebView 是安装目录（可能含空格，如 C:\Program Files (x86)\...），取最后
// 一段目录名。
func webView2VersionFromValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.LastIndexAny(raw, `\/`); i >= 0 {
		raw = raw[i+1:]
	}
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

// webView2VersionValid 判断版本字符串是否为至少 webView2MinimumVersion 的四段
// 数字版本（缺段按 0，与 Wails 的解析一致）。空串、非数字、段数过多或版本过低
// 都算无效。
func webView2VersionValid(raw string) bool {
	version, ok := parseWebView2Version(raw)
	if !ok {
		return false
	}
	minimum, ok := parseWebView2Version(webView2MinimumVersion)
	if !ok {
		return false
	}
	return version.compare(minimum) >= 0
}

// webView2Version 是解析后的四段版本号。
type webView2Version [4]int

// compare 返回 -1、0、1，分别表示 v 小于、等于、大于 other。
func (v webView2Version) compare(other webView2Version) int {
	for i := range v {
		switch {
		case v[i] < other[i]:
			return -1
		case v[i] > other[i]:
			return 1
		}
	}
	return 0
}

// parseWebView2Version 解析 "a.b.c.d"；接受 1-4 段（缺段按 0），每段必须是非负
// 十进制整数，段数超过 4 视为无效。
func parseWebView2Version(raw string) (webView2Version, bool) {
	var version webView2Version
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return version, false
	}
	parts := strings.Split(raw, ".")
	if len(parts) > len(version) {
		return webView2Version{}, false
	}
	for i, part := range parts {
		if part == "" {
			return webView2Version{}, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return webView2Version{}, false
		}
		version[i] = n
	}
	return version, true
}

// webView2Notice 返回缺失提示弹窗的标题与正文。正文中英文并列，并给出官方下载
// 地址和 winget 命令；用户点「是」时打开下载页。
func webView2Notice() (title, body string) {
	title = "Quota Keepalive"
	body = "QKA 需要 Microsoft Edge WebView2 运行时（Windows 10/11 均需要）。\r\n" +
		"\r\n" +
		"这台电脑上没有检测到 WebView2 运行时，所以 QKA 的窗口无法启动。\r\n" +
		"请先安装 WebView2 Runtime（官方下载地址）：\r\n" +
		webView2DownloadURL + "\r\n" +
		"安装完成后重新启动 QKA 即可。\r\n" +
		"\r\n" +
		"也可以在命令提示符或 PowerShell 中运行：\r\n" +
		"winget install Microsoft.EdgeWebView2Runtime\r\n" +
		"\r\n" +
		"QKA requires the Microsoft Edge WebView2 Runtime, which is not installed\r\n" +
		"on this computer. Install it from the URL above, then start QKA again.\r\n" +
		"\r\n" +
		"现在打开下载页面吗？ / Open the download page now?"
	return title, body
}
