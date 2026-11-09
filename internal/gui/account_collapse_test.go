package gui

import (
	"strings"
	"testing"
)

// TestAccountCardsUseMacStyleRowsWithoutCollapse 断言账号卡采用 mac 版的行式语言，
// 且不再「点头部展开」：签到结果与额度必须首屏常驻。
// 结构：acct-title → acct-meta（签到结果 + 额度）→ acct-ops（状态 chips）→ acct-links。
func TestAccountCardsUseMacStyleRowsWithoutCollapse(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`copy.className = "account-body"`,
		`title.className = "acct-title"`,
		`meta.className = "acct-meta"`,
		`ops.className = "acct-ops"`,
		`links.className = "acct-links"`,
		`trigger.className = "mini-action"`,
		`resultText.textContent = "今日尚未执行"`,
		`right.appendChild(trigger)`,
		`.acct-title {`,
		`.acct-meta {`,
		`.acct-ops {`,
		`.acct-link {`,
		`.mini-action {`,
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

	// 左列挂载顺序：标题 → 摘要 → chips → 操作；右列放开关与立即签到。
	titleAt := strings.Index(page, `copy.appendChild(title)`)
	metaAt := strings.Index(page, `copy.appendChild(meta)`)
	opsAt := strings.Index(page, `copy.appendChild(ops)`)
	linksAt := strings.Index(page, `copy.appendChild(links)`)
	if titleAt < 0 || metaAt < 0 || opsAt < 0 || linksAt < 0 {
		t.Fatal("账号卡行式结构标记缺失")
	}
	if !(titleAt < metaAt && metaAt < opsAt && opsAt < linksAt) {
		t.Fatalf("账号卡顺序错误：title(%d) < meta(%d) < ops(%d) < links(%d)",
			titleAt, metaAt, opsAt, linksAt)
	}
}
