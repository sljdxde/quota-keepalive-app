package gui

import (
	"strings"
	"testing"
)

// 主窗口 Agent 详情也支持把账号标识切成马赛克：Windows 没有菜单栏面板，
// 账号全在主窗口里，这个开关让两个平台都有同一份隐私能力。
func TestMainWindowAccountPrivacyToggle(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	requireStrings(t, page, "主窗口账号隐私开关",
		`id="agent-modal-privacy"`,
		`>隐藏账号</button>`,
		`ACCOUNT_PRIVACY_STORAGE_KEY = "qka.accounts.hidden"`,
		`function mosaicIdentityNode(`,
		`function toggleAccountPrivacy(`,
		`function syncAccountPrivacyButton(`,
		`title.appendChild(mosaicIdentityNode(identity));`,
		`document.getElementById("agent-modal-privacy").onclick = toggleAccountPrivacy;`,
		`.acct-mosaic {`,
		`(hideAccountIdentities ? "该账号" : "账号 " + identity)`,
		`localStorage.getItem("qka.panel.hideAccounts") === "1"`,
	)
	// 显示/隐藏分支必须分开：隐藏时不能把明文写进 DOM。
	for _, want := range []string{
		`if (hideAccountIdentities) {`,
		`title.appendChild(document.createTextNode(identity));`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("主窗口账号隐私缺少 %q", want)
		}
	}
}
