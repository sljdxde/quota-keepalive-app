package gui

import (
	"io/fs"
	"strings"
	"testing"
)

// TestBundledFontIsEmbeddedAndReferenced 是 Windows 字体可读性的回归用例。
//
// 背景：Windows 系统中文只有 Microsoft YaHei 的 400/700 两档，界面里写的
// 560/620/650 会被浏览器向上取整成 700，整屏「中等强调」文字全部偏粗——
// 这是 Windows 版字难读的根因。修复靠内嵌一份 Noto Sans SC 可变字体，
// 让任意字重都能精确渲染。这里守住三件事：
//  1. 字体文件确实进了 embed FS（否则运行时 404，界面静默回退到系统字体）；
//  2. qka-ui.css 确实声明了 @font-face 且指向该文件；
//  3. 字体栈第一位就是内置字体（否则系统字体会抢先命中，等于没生效）。
func TestBundledFontIsEmbeddedAndReferenced(t *testing.T) {
	const fontPath = "frontend/assets/fonts/noto-sc-vf-subset.woff2"

	info, err := fs.Stat(frontendFS, fontPath)
	if err != nil {
		t.Fatalf("内置字体未进入 embed FS：%v", err)
	}
	if info.Size() < 100*1024 {
		t.Fatalf("内置字体体积异常：%d 字节，疑似子集生成失败", info.Size())
	}

	data, err := fs.ReadFile(frontendFS, "frontend/assets/qka-ui.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)

	if !strings.Contains(css, "@font-face") {
		t.Fatal("qka-ui.css 缺少 @font-face：内置字体不会加载")
	}
	if !strings.Contains(css, `url("fonts/noto-sc-vf-subset.woff2")`) {
		t.Fatal("@font-face 未指向内置字体文件")
	}
	// 可变字重区间必须覆盖 400-700，否则中间字重仍会被取整。
	if !strings.Contains(css, "font-weight: 100 900") {
		t.Fatal("@font-face 未声明可变字重区间 100 900")
	}

	// 字体栈第一位必须是内置字体：写成 var(--font) 的组件才真正用上它。
	if !strings.Contains(css, `--font: "QKA Sans SC"`) {
		t.Fatal("--font 的首位不是内置字体，系统字体会抢先命中")
	}
}

// TestFontWeightsStayWithinFourTiers 守住字重只用 400/500/600/700 四档。
// 可变字体虽能渲染任意字重，但四档足够表达层级；回到 560/620 那种半像素
// 档位会让「哪个更重」变得不可辨，也让非可变字体环境重新出现取整问题。
func TestFontWeightsStayWithinFourTiers(t *testing.T) {
	allowed := map[string]bool{"400": true, "500": true, "600": true, "700": true}
	files := []string{
		"frontend/assets/qka-ui.css",
		"frontend/main.html",
		"frontend/panel.html",
		"frontend/settings.html",
	}
	for _, name := range files {
		data, err := fs.ReadFile(frontendFS, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			idx := strings.Index(line, "font-weight:")
			if idx < 0 {
				continue
			}
			rest := strings.TrimSpace(line[idx+len("font-weight:"):])
			// 取第一个数字 token；@font-face 的 "100 900" 会取到 100，单独放过。
			num := ""
			for _, r := range rest {
				if r >= '0' && r <= '9' {
					num += string(r)
				} else {
					break
				}
			}
			if num == "" || num == "100" {
				continue // 可变轴声明
			}
			if !allowed[num] {
				t.Errorf("%s 使用了非标准字重 %s：%s", name, num, strings.TrimSpace(line))
			}
		}
	}
}
