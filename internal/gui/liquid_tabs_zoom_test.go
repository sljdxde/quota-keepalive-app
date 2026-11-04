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

// 顶栏导航必须显式垂直居中：只写 left 的绝对定位元素依赖 flex 静态位置推导，
// zoom 下容易差半像素，导致导航文字与胶囊/右侧图标不齐。
func TestTopbarNavIsExplicitlyCentered(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "position: absolute; top: 50%; left: 50%;") {
		t.Error("顶栏导航缺少显式 top:50% 居中")
	}
	if !strings.Contains(page, "transform: translate(-50%, -50%);") {
		t.Error("顶栏导航应使用 translate(-50%,-50%) 双向居中")
	}
	if strings.Contains(page, "position: absolute; left: 50%; transform: translateX(-50%);") {
		t.Error("仍有只做水平居中的绝对定位导航（纵向位置不确定）")
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
