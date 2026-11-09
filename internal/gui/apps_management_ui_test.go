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
		// 0.1.36「模型区去二级折叠」删掉了折叠实现，却漏删了这个调用点：
		// providerModelPanel 里调用未定义函数会抛 ReferenceError，而
		// openAgentModal 是 async 且没有 catch，异常静默变成 rejected
		// promise，`remove("hidden")` 永远执行不到 —— 用户侧表现为
		// 「点 Agent 行没反应」。保留此断言防止同类悬空调用再次出现。
		`syncPanelCollapsed()`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("应用管理不应再保留旧折叠卡片实现 %q", gone)
		}
	}
	// 点行的链路是「先填充、后移除 hidden」：填充里任何未定义调用都会让
	// 显示语句执行不到。这里断言显示语句仍是单行写法（不做跨行匹配，
	// 避免缩进/CRLF 变化导致假失败）。
	if !strings.Contains(page, `document.getElementById("agent-modal").classList.remove("hidden")`) {
		t.Error("openAgentModal 缺少显示弹层的语句，点行会没有反应")
	}

	// 「立即巡检」按钮已移除：自动执行每 2 小时跑一次，账号级也有立即签到/立即活跃，
	// 全局手动巡检属于重复入口。
	for _, forbidden := range []string{`id="run-all"`, `立即巡检`, `document.getElementById("run-all")`} {
		if strings.Contains(page, forbidden) {
			t.Errorf("应用管理页不应再保留立即巡检入口: %q", forbidden)
		}
	}
}
