//go:build windows

package gui

import "testing"

// 真实注册表读取的 plumbing（只能在 Windows 上跑）：存在的值读得到，不存在的
// 键返回 false 而不是报错。
func TestReadWebView2RegistryValue(t *testing.T) {
	name, ok := readWebView2RegistryValue(webView2HKLM,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName", false)
	if !ok || name == "" {
		t.Fatalf("应读到 ProductName，ok=%v name=%q", ok, name)
	}
	if _, ok := readWebView2RegistryValue(webView2HKLM, `SOFTWARE\QKA-Does-Not-Exist`, "pv", true); ok {
		t.Fatal("不存在的键应返回 false")
	}
}

// 缺失时的完整流程：弹提示、按需打开下载页（这里替换掉，避免真实弹窗）、把
// 非 0 退出码交给可注入的退出函数。
func TestEnsureWebView2RuntimeMissingPromptsAndExits(t *testing.T) {
	oldRead, oldPrompt, oldExit := webView2ReadRegistry, webView2PromptConfirm, exitAfterWebView2Prompt
	t.Cleanup(func() {
		webView2ReadRegistry, webView2PromptConfirm, exitAfterWebView2Prompt = oldRead, oldPrompt, oldExit
	})

	webView2ReadRegistry = func(webView2RegistryRoot, string, string, bool) (string, bool) {
		return "", false
	}
	prompted := false
	webView2PromptConfirm = func() bool {
		prompted = true
		return false // 用户点「否」，不真正打开浏览器
	}
	exitCode := -1
	exitAfterWebView2Prompt = func(code int) { exitCode = code }

	ensureWebView2Runtime()

	if !prompted {
		t.Fatal("缺少运行时应弹出原生提示")
	}
	if exitCode != webView2ExitCode {
		t.Fatalf("退出码 = %d，期望 %d", exitCode, webView2ExitCode)
	}
	if exitCode == 0 {
		t.Fatal("退出码必须非 0，否则仍会静默消失")
	}
}

// 运行时存在时不弹窗、不退出。
func TestEnsureWebView2RuntimePresentDoesNothing(t *testing.T) {
	oldRead, oldPrompt, oldExit := webView2ReadRegistry, webView2PromptConfirm, exitAfterWebView2Prompt
	t.Cleanup(func() {
		webView2ReadRegistry, webView2PromptConfirm, exitAfterWebView2Prompt = oldRead, oldPrompt, oldExit
	})

	webView2ReadRegistry = func(webView2RegistryRoot, string, string, bool) (string, bool) {
		return "141.0.3537.71", true
	}
	webView2PromptConfirm = func() bool {
		t.Fatal("运行时存在时不应弹窗")
		return false
	}
	exitAfterWebView2Prompt = func(int) { t.Fatal("运行时存在时不应退出") }

	ensureWebView2Runtime()
}
