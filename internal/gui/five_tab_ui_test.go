package gui

import (
	"strings"
	"testing"
)

// TestMainUsesFiveTabProductShell 断言主窗口是「应用管理 / Token 速度 /
// 宠物设置 / 日志 / 设置」五页签产品外壳；签到管理已并入应用管理的 Agent
// 详情弹层，不再有独立入口。
func TestMainUsesFiveTabProductShell(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	orderedTabs := []string{
		`data-tab="apps"`,
		`data-tab="speed"`,
		`data-tab="freetokens"`,
		`data-tab="pet"`,
		`data-tab="logs"`,
		`data-tab="settings"`,
	}
	lastIndex := -1
	for _, marker := range orderedTabs {
		index := strings.Index(page, marker)
		if index < 0 {
			t.Fatalf("主导航缺少 %q", marker)
		}
		if index <= lastIndex {
			t.Fatalf("主导航顺序错误：%q 必须排在上一项之后", marker)
		}
		lastIndex = index
	}
	for _, want := range []string{
		`data-tab="apps"`,
		`data-tab="speed"`,
		`data-tab="logs"`,
		`data-tab="pet"`,
		`data-tab="settings"`,
		`data-tab="freetokens"`,
		`id="pet"`,
		`id="settings"`,
		`id="settings-body"`,
		`id="agent-modal"`,
		`id="agent-modal-body"`,
		"Bound.SettingsSchema()",
		`Events.On("open-settings"`,
		`Events.On("open-checkin"`,
		`function focusCheckinRequest(`,
		`function openAgentModal(`,
		`function fillAgentModal(`,
		`function finishAuthorizedProviderLogin(`,
		"scrollbar-width: none",
		`values.__qka_version`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("五页签主窗口缺少 %q", want)
		}
	}
	for _, forbidden := range []string{
		`data-tab="checkin"`,
		`id="checkin"`,
		`data-tab="accounts"`,
		`data-tab="agent"`,
		`data-tab="plugins"`,
		`id="agent"`,
		`id="plugins"`,
		"Bound.AgentStatus()",
		"Bound.PluginsList()",
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("主窗口不应再出现旧入口 %q", forbidden)
		}
	}
}
