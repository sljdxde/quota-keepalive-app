package gui

import (
	"strings"
	"testing"
)

// 菜单栏面板的账号隐私开关：眼睛图标切换「正常显示 / 马赛克隐藏」，隐藏时
// DOM 里不保留明文标识，且选择会持久化到 localStorage。
func TestPanelAccountPrivacyMosaicToggle(t *testing.T) {
	pageData, err := frontendFS.ReadFile("frontend/panel.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(pageData)
	requireStrings(t, page, "面板账号隐私开关",
		`id="account-privacy"`,
		`aria-label="隐藏账号（马赛克）"`,
		`id="account-privacy-label"`,
		`<span id="account-privacy-label">隐藏账号</span>`,
		`class="eye-open"`,
		`class="eye-off"`,
		`.acct-mosaic {`,
		`.checkin-page.is-private .privacy-toggle .eye-off { display: block; }`,
	)

	scriptData, err := frontendFS.ReadFile("frontend/assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptData)
	requireStrings(t, script, "面板账号马赛克逻辑",
		`const accountPrivacyStorageKey = "qka.accounts.hidden";`,
		`const legacyPanelPrivacyStorageKey = "qka.panel.hideAccounts";`,
		`function mosaicIdentityNode(`,
		`function syncPrivacyToggle(`,
		`function toggleAccountPrivacy(`,
		`mosaicIdentityNode(identity)`,
		`function accountIdentityText(`,
		`known.identity`,
		`"打开主窗口查看账号（已隐藏）"`,
		`document.getElementById("account-privacy").addEventListener("click"`,
		`text.textContent = hideAccounts ? "显示账号" : "隐藏账号"`,
	)
	// 隐藏时不能再把明文写进 DOM：identity 变量只在非隐藏分支用 createTextNode。
	for _, want := range []string{
		`if (hideAccounts) {`,
		`title.appendChild(mosaicIdentityNode(identity));`,
		`title.appendChild(document.createTextNode(identity));`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("隐藏/显示分支缺少 %q", want)
		}
	}
}
