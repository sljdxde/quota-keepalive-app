package gui

import (
	"strings"
	"testing"
)

// 吞吐三连（输入吞吐/总吞吐/正在生成）的布局回归保护。
//
// 背景：小窗口或 150% 缩放的笔记本上，这一行的标签/提示会挤到卡片右边界
// （用户反馈「字体超过边界」）；而如果为了防溢出给数值加省略号，又会把
// 「33.37k」剪成「3…」——数值是这一格的信息本体，绝不能截断。
// 约定：标签与提示单行 + 省略号，数值单行但完整显示。
func TestMetricRowNeverClipsValues(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)

	valueRule := cssRule(page, ".metric-card .value {")
	if valueRule == "" {
		t.Fatal("找不到 .metric-card .value 规则")
	}
	if strings.Contains(valueRule, "text-overflow") || strings.Contains(valueRule, "max-width") {
		t.Errorf("数值不得被截断（text-overflow/max-width 会把 33.37k 剪成 3…）:\n%s", valueRule)
	}
	if !strings.Contains(valueRule, "white-space: nowrap") {
		t.Errorf("数值必须单行显示:\n%s", valueRule)
	}
	if !strings.Contains(valueRule, "justify-self: end") {
		t.Errorf("数值应右对齐，避免与左侧标签抢位置:\n%s", valueRule)
	}

	// 标签与提示让位：单行 + 省略号，任何宽度下都不会溢出卡片。
	for _, sel := range []string{".metric-card .label {", ".metric-card .hint {"} {
		rule := cssRule(page, sel)
		if rule == "" {
			t.Fatalf("找不到 %s 规则", sel)
		}
		for _, want := range []string{"white-space: nowrap", "text-overflow: ellipsis", "overflow: hidden"} {
			if !strings.Contains(rule, want) {
				t.Errorf("%s 缺少 %q：窄窗口下文字会溢出卡片边界\n%s", sel, want, rule)
			}
		}
	}

	// 窄窗口提前切单列（三列并排在 ~1000 CSS px 以下会挤到贴边）。
	if !strings.Contains(page, "@media (max-width: 1080px)") {
		t.Error("缺少 1080px 的窄屏单列规则")
	}
}

// cssRule 返回选择器 sel 对应的声明块（去掉花括号）；找不到返回空串。
func cssRule(source, sel string) string {
	i := strings.Index(source, sel)
	if i < 0 {
		return ""
	}
	rest := source[i+len(sel):]
	if j := strings.Index(rest, "}"); j >= 0 {
		return rest[:j]
	}
	return ""
}

// 页面留白：内容不应被硬卡在 1040px（旧实现 max(30px, calc(50% - 520px))），
// 宽窗口下两侧各浪费近 200px。这里锁住「按比例留白 + 更宽上限」的方案。
func TestPageGuttersUseProportionalPadding(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	rule := cssRule(page, ".page {")
	if rule == "" {
		t.Fatal("找不到 .page 规则")
	}
	if strings.Contains(rule, "calc(50% - 520px)") {
		t.Error(".page 仍在使用硬编码 1040px 内容宽度：宽窗口下留白过多")
	}
	for _, want := range []string{"clamp(16px", "max-width: 1180px", "margin-inline: auto"} {
		if !strings.Contains(rule, want) {
			t.Errorf(".page 缺少 %q:\n%s", want, rule)
		}
	}
}
