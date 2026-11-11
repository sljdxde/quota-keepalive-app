package gui

import (
	"strings"
	"testing"

	"qka/internal/settings"
)

// 菜单栏标签是纯文本（macOS 的 NSStatusItem 不能显示滚动数字），
// 所以这里只验证三种样式的文案与分段颜色。
func TestMenuBarLabelStyles(t *testing.T) {
	tests := []struct {
		name  string
		style string
		speed float64
		want  string
	}{
		{name: "纯文本带单位", style: settings.MenuBarSpeedLabel, speed: 128, want: "128 tok/s"},
		{name: "只显示数字", style: settings.MenuBarSpeedCompact, speed: 128, want: "128"},
		{name: "未知样式回退纯文本", style: "future", speed: 42, want: "42 tok/s"},
		{name: "空闲时纯文本", style: settings.MenuBarSpeedLabel, speed: 0, want: "0 tok/s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := menuBarLabel(tc.style, tc.speed); got != tc.want {
				t.Fatalf("menuBarLabel(%q, %v) = %q, want %q", tc.style, tc.speed, got, tc.want)
			}
		})
	}
}

// 有速度时分段彩色会给数字加上颜色序列，空闲时不染色（避免「0 也是绿的」）。
func TestMenuBarLabelTintedOnlyWhenActive(t *testing.T) {
	active := menuBarLabel(settings.MenuBarSpeedTinted, 128)
	if !strings.Contains(active, menuBarActiveColor) {
		t.Fatalf("有速度时应带颜色序列，实际 %q", active)
	}
	if !strings.HasSuffix(active, " tok/s") {
		t.Fatalf("分段彩色仍要带单位，实际 %q", active)
	}
	idle := menuBarLabel(settings.MenuBarSpeedTinted, 0)
	if strings.Contains(idle, menuBarActiveColor) {
		t.Fatalf("空闲时不应染色，实际 %q", idle)
	}
	if idle != "0 tok/s" {
		t.Fatalf("空闲时文案 = %q，want %q", idle, "0 tok/s")
	}
}

// 解析器把颜色序列拆成 Wails 需要的分段；没有序列时退化成单段。
func TestParseMenuBarLabelSplitsColoredSegments(t *testing.T) {
	plain, err := parseMenuBarLabel("128 tok/s")
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 1 || plain[0].Text != "128 tok/s" || plain[0].FgColor != "" {
		t.Fatalf("纯文本应解析成单段无色，实际 %+v", plain)
	}

	colored, err := parseMenuBarLabel(menuBarLabel(settings.MenuBarSpeedTinted, 128))
	if err != nil {
		t.Fatal(err)
	}
	if len(colored) != 2 {
		t.Fatalf("彩色标签应拆成两段，实际 %+v", colored)
	}
	if colored[0].Text != "128" || colored[0].FgColor != menuBarActiveColor {
		t.Fatalf("第一段应是染色数字，实际 %+v", colored[0])
	}
	if colored[1].Text != " tok/s" || colored[1].FgColor != "" {
		t.Fatalf("第二段应是原色单位，实际 %+v", colored[1])
	}
}

// 未闭合的转义序列不能吞字符：宁可原样显示，也不要让数字消失。
func TestParseMenuBarLabelKeepsUnterminatedSequence(t *testing.T) {
	parts, err := parseMenuBarLabel("12\033[#fff")
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, part := range parts {
		joined.WriteString(part.Text)
	}
	if !strings.Contains(joined.String(), "12") {
		t.Fatalf("未闭合序列不应吞掉数字，实际 %q", joined.String())
	}

	empty, err := parseMenuBarLabel("\033[#fff" + "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 1 || empty[0].Text != "" {
		t.Fatalf("只有颜色序列时应返回空文本段，实际 %+v", empty)
	}
}

// compact 样式不显示单位，tooltip 必须把含义补回来。
func TestMenuBarTooltipExplainsCompactStyle(t *testing.T) {
	if got := menuBarTooltip(settings.MenuBarSpeedCompact, 128); !strings.Contains(got, "128 tok/s") {
		t.Fatalf("compact 的 tooltip 应含完整单位，实际 %q", got)
	}
	if got := menuBarTooltip(settings.MenuBarSpeedLabel, 128); got != "Quota Keepalive" {
		t.Fatalf("其它样式的 tooltip = %q", got)
	}
}
