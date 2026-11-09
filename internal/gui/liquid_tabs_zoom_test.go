package gui

import (
	"strings"
	"testing"
)

// 回归保护：滑动胶囊用 getBoundingClientRect() 取几何，而 rect 的值已包含
// CSS zoom；直接写回 style 会被缩放两次。在 150% 缩放的笔记本 + 界面缩放
// 0.85 时，胶囊会缩成 0.85² ≈ 72%，表现为「字体和按钮没对齐」（实测胶囊
// 30px、按钮 35.7px）。本测试确认换算函数存在且被几何与高光两处调用。
func TestLiquidTabsCompensateZoom(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/assets/liquid-ui.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)

	if !strings.Contains(src, "function zoomScale(") {
		t.Fatal("liquid-ui.js 缺少 zoomScale 换算函数：rect 值含 zoom，写回会被二次缩放")
	}
	// 几何换算：tabGeometry 必须除以 scale。
	geometry := section(src, "function tabGeometry(", "function sameGeometry(")
	for _, want := range []string{"/ scale", "zoomScale(root)"} {
		if !strings.Contains(geometry, want) {
			t.Errorf("tabGeometry 未做 zoom 换算: 缺少 %q", want)
		}
	}
	// 高光位置同样需要换算。
	pointer := section(src, "function applyPointer(", "surface.addEventListener")
	if !strings.Contains(pointer, "zoomScale(surface)") {
		t.Error("applyPointer 未做 zoom 换算：高光会偏离指针")
	}
}

// 回归保护：6 个页签后导航变宽，曾经用绝对定位居中压住左侧品牌文字。
// 顶栏必须是「品牌 | 居中导航 | 右侧图标」三栏 grid，导航在中间列居中，
// 左右两栏为品牌和图标保留空间，窗口变窄时品牌先截断/隐藏而不是被遮挡。
func TestTopbarLayoutReservesSpaceAroundCenteredNav(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		"grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);",
		"grid-column: 1;",
		"grid-column: 2;",
		"justify-self: center;",
		"grid-column: 3;",
		"justify-self: end;",
		"@media (max-width: 1000px)",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("顶栏三栏布局缺少 %q", want)
		}
	}
	if strings.Contains(page, "position: absolute; top: 50%; left: 50%;") {
		t.Error("顶栏导航不应再用绝对定位居中：会压住品牌文字")
	}
}

// section 返回 src 中 start 到 end 之间的片段；找不到时返回空串。
func section(src, start, end string) string {
	i := strings.Index(src, start)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	j := strings.Index(rest, end)
	if j < 0 {
		return rest
	}
	return rest[:j]
}
