package gui

import (
	"strings"
	"testing"
)

func TestLogsUseTopLevelCategoryTabs(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`id="log-categories"`,
		`data-log-category="all"`,
		`data-log-category="schedule"`,
		`data-log-category="checkin"`,
		`data-log-category="refresh"`,
		`data-log-category="model_test"`,
		"logCategoryOperations",
		`schedule: "schedule"`,
		`refresh: "refresh"`,
		`model_test: "model_test"`,
		`model_test: "模型测试"`,
		"appendLogs(true)",
		// 账号展示与应用管理同一口径：默认完整显示，可切马赛克。
		`id="logs-privacy"`,
		"function syncLogsPrivacyButton(",
		"function loadLogsIdentityIndex(",
		"function logAccountText(",
		"logAccountText(e.target, e.account_id, identityIndex,",
		"hideAccountIdentities ? e.account_masked : e.account_identity",
		"logTargetText(e.target, identityIndex, e.target_name)",
		"mosaicIdentityNode(accountText)",
		"account.identity || account.masked_identity",
		"function logTimeParts(",
		"logTargetText(e.target, identityIndex, e.target_name)",
		"messageText.indexOf(opName) !== 0",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("日志页缺少顶部分类能力 %q", want)
		}
	}
	styleData, err := frontendFS.ReadFile("frontend/assets/qka-ui.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(styleData), "grid-template-columns: 90px 116px minmax(0,1fr) 56px") {
		t.Error("日志行应为日期/时间两行预留更宽的列")
	}
	if strings.Contains(page, "maskLogAccount") {
		t.Error("日志账号不应再固定打码；改为可切换的完整/马赛克展示")
	}
	if strings.Contains(page, `id="log-operation"`) {
		t.Error("日志类别不应继续使用底部操作输入框")
	}
	if strings.Contains(page, `data-log-category="keepalive"`) {
		t.Error("保活已全局隐藏，日志页不应保留保活分类")
	}
}
