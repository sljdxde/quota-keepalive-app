package gui

import (
	"strings"
	"testing"
)

// 账号备注是用户在 QKA 里给账号起的显示名（例如上游只肯给脱敏手机号时，
// 他自己填的真实号码）。这里锁住三件事：主窗口能编辑、面板优先显示备注、
// 前端调用的绑定确实存在。
func TestAccountLabelUIWired(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`rename.textContent = "改备注"`,
		"function startAccountRename(title, provider, account)",
		"Bound.ProvidersSetAccountLabel(",
		`input.maxLength = 40`,
		`const label = account && typeof account.label === "string"`,
		"account.label || account.identity || account.masked_identity",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("主窗口缺少账号备注逻辑: %q", want)
		}
	}

	panelData, err := frontendFS.ReadFile("frontend/assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	panel := strings.ReplaceAll(string(panelData), "\r\n", "\n")
	for _, want := range []string{
		`const label = known && typeof known.label === "string"`,
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("菜单栏面板缺少备注优先逻辑: %q", want)
		}
	}

	bindingData, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bindingData), "export function ProvidersSetAccountLabel(") {
		t.Error("生成绑定里缺少 ProvidersSetAccountLabel")
	}
}
