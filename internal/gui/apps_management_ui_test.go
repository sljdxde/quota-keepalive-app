package gui

import (
	"strings"
	"testing"
)

// TestAppsPageUsesAgentRowsAndModal 断言应用管理改成「Agent 行列表 + 详情弹层」：
// 已启用 / 已停用分组、点行开弹层、开关独立处理、弹层里有账号与模型区块。
func TestAppsPageUsesAgentRowsAndModal(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`id="apps-body"`,
		`id="agent-modal"`,
		`id="agent-modal-body"`,
		`id="apps-search"`,
		`function rowNode(provider)`,
		`item.className = "row-item"`,
		`row.className = "row"`,
		`providers.filter(function (p) { return p.enabled && match(p); })`,
		`providers.filter(function (p) { return !p.enabled && match(p); })`,
		`enabledList.className = "list"`,
		`head.className = "fold-head"`,
		`offList.classList.toggle("hidden", !appsDisabledOpen)`,
		`let appsDisabledOpen = false;`,
		`openAgentModal(provider)`,
		`async function openAgentModal(provider)`,
		`function fillAgentModal(provider)`,
		`function agentModalSection(`,
		`await Bound.ProvidersSetEnabled(provider.id, !provider.enabled)`,
		`providerAccountCard(provider, account)`,
		`providerModelPanel(provider)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("应用管理 Agent 行/弹层缺少 %q", want)
		}
	}
	// 不再使用旧的整页折叠卡片实现。
	for _, gone := range []string{
		`item.className = "app-item"`,
		`appsDisabledOpen = disabledGroup.open`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("应用管理不应再保留旧折叠卡片实现 %q", gone)
		}
	}
}
