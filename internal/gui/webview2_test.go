package gui

import (
	"fmt"
	"strings"
	"testing"
)

// webView2RegKey 把注册表读取位置字符串化，用于在假注册表里查找。
func webView2RegKey(root webView2RegistryRoot, path, name string, view32 bool) string {
	return fmt.Sprintf("%s|%s|%s|%t", root, path, name, view32)
}

func TestWebView2VersionFromValue(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
		want string
	}{
		{"裸版本号", "141.0.3537.71", "141.0.3537.71"},
		{"前后空白", "  141.0.3537.71  ", "141.0.3537.71"},
		{"EBWebView 安装目录", `C:\Program Files (x86)\Microsoft\EdgeWebView\Application\141.0.3537.71`, "141.0.3537.71"},
		{"正斜杠目录", `C:/Program Files/Microsoft/EdgeWebView/Application/141.0.3537.71`, "141.0.3537.71"},
		{"带通道后缀", "141.0.3537.71 beta", "141.0.3537.71"},
		{"空值", "", ""},
		{"纯空白", "   ", ""},
	} {
		if got := webView2VersionFromValue(testCase.raw); got != testCase.want {
			t.Errorf("%s: webView2VersionFromValue(%q) = %q，期望 %q", testCase.name, testCase.raw, got, testCase.want)
		}
	}
}

func TestWebView2VersionValid(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
		want bool
	}{
		{"常见版本", "141.0.3537.71", true},
		{"最低版本边界", "86.0.616.0", true},
		{"三段的边界版本缺段按 0", "86.0.616", true},
		{"更高版本", "120.0.0.0", true},
		{"低于最低版本", "85.0.615.99", false},
		{"零版本", "0.0.0.0", false},
		{"空串", "", false},
		{"纯空白", "   ", false},
		{"非数字", "abc", false},
		{"段数过多", "86.0.616.0.1", false},
		{"空段", "86..616.0", false},
		{"负数段", "86.0.-1.0", false},
		{"带前缀", "v141.0.3537.71", false},
	} {
		if got := webView2VersionValid(testCase.raw); got != testCase.want {
			t.Errorf("%s: webView2VersionValid(%q) = %v，期望 %v", testCase.name, testCase.raw, got, testCase.want)
		}
	}
}

// 系统级/用户级、32/64 位视图、Clients 与 ClientState 都要覆盖，否则会漏掉
// Win10 上常见的用户级安装。
func TestWebView2RegistryCandidatesCoverAllInstalls(t *testing.T) {
	found := make(map[string]bool, len(webView2RegistryCandidates))
	for _, candidate := range webView2RegistryCandidates {
		found[webView2RegKey(candidate.root, candidate.path, candidate.name, candidate.view32)] = true
	}
	for _, root := range []webView2RegistryRoot{webView2HKLM, webView2HKCU} {
		for _, view32 := range []bool{true, false} {
			for _, location := range []struct{ path, name string }{
				{webView2ClientsKey, webView2VersionValue},
				{webView2ClientStateKey, webView2ClientStateValue},
			} {
				key := webView2RegKey(root, location.path, location.name, view32)
				if !found[key] {
					t.Errorf("缺少注册表候选位置 %s", key)
				}
			}
		}
	}
}

func TestWebView2InstalledFromRegistry(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{
			name: "没有任何运行时",
			want: false,
		},
		{
			name: "系统级 32 位视图 pv",
			values: map[string]string{
				webView2RegKey(webView2HKLM, webView2ClientsKey, webView2VersionValue, true): "141.0.3537.71",
			},
			want: true,
		},
		{
			name: "系统级 64 位视图 pv",
			values: map[string]string{
				webView2RegKey(webView2HKLM, webView2ClientsKey, webView2VersionValue, false): "141.0.3537.71",
			},
			want: true,
		},
		{
			name: "用户级 32 位视图 pv",
			values: map[string]string{
				webView2RegKey(webView2HKCU, webView2ClientsKey, webView2VersionValue, true): "141.0.3537.71",
			},
			want: true,
		},
		{
			name: "用户级 64 位视图 pv",
			values: map[string]string{
				webView2RegKey(webView2HKCU, webView2ClientsKey, webView2VersionValue, false): "141.0.3537.71",
			},
			want: true,
		},
		{
			name: "ClientState 的 EBWebView 目录",
			values: map[string]string{
				webView2RegKey(webView2HKLM, webView2ClientStateKey, webView2ClientStateValue, true): `C:\Program Files (x86)\Microsoft\EdgeWebView\Application\141.0.3537.71`,
			},
			want: true,
		},
		{
			name: "值为空",
			values: map[string]string{
				webView2RegKey(webView2HKLM, webView2ClientsKey, webView2VersionValue, true): "",
			},
			want: false,
		},
		{
			name: "值不是版本号",
			values: map[string]string{
				webView2RegKey(webView2HKLM, webView2ClientsKey, webView2VersionValue, true): "not-a-version",
			},
			want: false,
		},
		{
			name: "版本低于 Wails 最低兼容版本",
			values: map[string]string{
				webView2RegKey(webView2HKLM, webView2ClientsKey, webView2VersionValue, true): "85.0.615.99",
			},
			want: false,
		},
		{
			name: "无效值不妨碍后续候选命中",
			values: map[string]string{
				webView2RegKey(webView2HKLM, webView2ClientsKey, webView2VersionValue, true):  "",
				webView2RegKey(webView2HKCU, webView2ClientsKey, webView2VersionValue, false): "120.0.0.0",
			},
			want: true,
		},
	} {
		values := testCase.values
		read := func(root webView2RegistryRoot, path, name string, view32 bool) (string, bool) {
			value, ok := values[webView2RegKey(root, path, name, view32)]
			return value, ok
		}
		if got := webView2Installed(read); got != testCase.want {
			t.Errorf("%s: webView2Installed = %v，期望 %v", testCase.name, got, testCase.want)
		}
	}
}

func TestWebView2Notice(t *testing.T) {
	if webView2DownloadURL != "https://go.microsoft.com/fwlink/p/?LinkId=2124703" {
		t.Fatalf("下载地址必须是官方地址，实际 %q", webView2DownloadURL)
	}
	title, body := webView2Notice()
	if title != "Quota Keepalive" {
		t.Errorf("弹窗标题 = %q，期望 \"Quota Keepalive\"", title)
	}
	for _, want := range []string{
		"WebView2",
		"运行时",
		"Windows 10/11",
		"QKA 需要",
		"重新启动 QKA",
		"requires",
		webView2DownloadURL,
		"winget install Microsoft.EdgeWebView2Runtime",
		"\r\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("弹窗正文缺少 %q：\n%s", want, body)
		}
	}
	if !strings.HasSuffix(body, "?") {
		t.Errorf("弹窗正文应以询问是否打开下载页结尾：\n%s", body)
	}
	if strings.Contains(body, "%!") {
		t.Errorf("弹窗正文格式串有误：\n%s", body)
	}
}
