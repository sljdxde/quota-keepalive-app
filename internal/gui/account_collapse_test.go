package gui

import (
	"strings"
	"testing"
)

// TestAccountCardsUseMacStyleRowsWithoutCollapse 断言账号卡采用行式语言，
// 且不再「点头部展开」：状态与额度必须首屏常驻。
// 结构：acct-title（点+账号名+备注+自动开关）→ acct-meta（额度 + 小圆圈刷新）
// → acct-status（今日签到/今日活跃/执行日志）→ acct-links（文字链操作）。
func TestAccountCardsUseMacStyleRowsWithoutCollapse(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`title.className = "acct-title"`,
		`meta.className = "acct-meta"`,
		`status.className = "acct-status"`,
		`links.className = "acct-links"`,
		`refreshBtn.className = "acct-quota-refresh"`,
		`checkinBtn.textContent = "手动签到"`,
		`.acct-title {`,
		`.acct-meta {`,
		`.acct-status {`,
		`.acct-link {`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("账号卡 mac 行式布局缺少 %q", want)
		}
	}

	// 旧折叠实现必须彻底移除：签到结果与额度不再被 body 收起藏住。
	for _, gone := range []string{
		`account-collapse-head`,
		`account-collapse`,
		`shouldCollapseAccount`,
		`nextAccountExpansion`,
		`expandedAccounts`,
		`box.classList.toggle("collapsed"`,
		`.account-card.collapsed .account-body { display: none; }`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("账号卡不应再保留折叠实现 %q", gone)
		}
	}

	// 卡片挂载顺序：名称行 → 额度行 → 状态行 → 操作行。
	titleAt := strings.Index(page, `box.appendChild(title)`)
	metaAt := strings.Index(page, `box.appendChild(meta)`)
	statusAt := strings.Index(page, `box.appendChild(status)`)
	linksAt := strings.Index(page, `box.appendChild(links)`)
	if titleAt < 0 || metaAt < 0 || statusAt < 0 || linksAt < 0 {
		t.Fatal("账号卡行式结构标记缺失")
	}
	if !(titleAt < metaAt && metaAt < statusAt && statusAt < linksAt) {
		t.Fatalf("账号卡顺序错误：title(%d) < meta(%d) < status(%d) < links(%d)",
			titleAt, metaAt, statusAt, linksAt)
	}
}
