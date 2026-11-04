package gui

import "testing"

// 大屏上理想尺寸原样保留。
func TestFitWindowSizeKeepsIdealWhenRoomEnough(t *testing.T) {
	got := fitWindowSize(mainWindowSize(), 2560, 1400, windowFitMargin)
	if got != mainWindowSize() {
		t.Fatalf("大屏不应缩小窗口: got=%+v want=%+v", got, mainWindowSize())
	}
}

// 小屏（典型 1280×720、工作区 1280×680）：窗口与最小尺寸都要收敛，
// 否则标题栏按钮被挤出屏幕、用户也无法把窗口拖进可见范围。
func TestFitWindowSizeShrinksForSmallScreen(t *testing.T) {
	work := mainWindowSize()
	got := fitWindowSize(work, 1280, 680, windowFitMargin)

	if got.Width > 1280-2*windowFitMargin {
		t.Fatalf("宽度应不超过工作区减余量: %+v", got)
	}
	if got.Height > 680-2*windowFitMargin {
		t.Fatalf("高度应不超过工作区减余量: %+v", got)
	}
	if got.Height != 680-2*windowFitMargin {
		t.Fatalf("高度应收敛到可用高度 %d，实际 %d", 680-2*windowFitMargin, got.Height)
	}
	// 最小尺寸不能大于收敛后的尺寸，否则用户拖不动也缩不小。
	if got.MinWidth > got.Width || got.MinHeight > got.Height {
		t.Fatalf("最小尺寸应随之下调: %+v", got)
	}
	// 默认最小高度仍放得下时应保留，用户才有余地继续缩小窗口。
	if got.MinHeight != mainWindowSize().MinHeight {
		t.Fatalf("最小高度仍放得下时应保留 %d，实际 %d", mainWindowSize().MinHeight, got.MinHeight)
	}
}

// 默认窗口刻意小于工作区：不铺满屏幕，卡片也就不会被拉成一条。
func TestMainWindowIsDeliberatelySmallerThanLargeScreen(t *testing.T) {
	size := mainWindowSize()
	// 典型 1080p 屏（工作区约 1920×1040 DIP）上，默认窗口应只占 70% 左右。
	if size.Width > 1920*7/10 {
		t.Fatalf("默认宽度 %d 超过工作区的 70%%：会在大屏上铺满，卡片被拉长", size.Width)
	}
	if size.Height > 1040*7/10 {
		t.Fatalf("默认高度 %d 超过工作区的 70%%：应留出桌面空间", size.Height)
	}
	if size.MinWidth >= size.Width || size.MinHeight >= size.Height {
		t.Fatalf("最小尺寸应小于默认尺寸: min=%dx%d ideal=%dx%d",
			size.MinWidth, size.MinHeight, size.Width, size.Height)
	}
}

// 极小屏：连最小尺寸都放不下时，最小高度继续收敛到窗口高度。
func TestFitWindowSizeShrinksMinSizeWhenTiny(t *testing.T) {
	got := fitWindowSize(mainWindowSize(), 800, 500, windowFitMargin)
	if got.Width != 800-2*windowFitMargin || got.Height != 500-2*windowFitMargin {
		t.Fatalf("极小屏应收敛到工作区: %+v", got)
	}
	if got.MinWidth > got.Width || got.MinHeight > got.Height {
		t.Fatalf("最小尺寸不得超过窗口尺寸: %+v", got)
	}
}

// 工作区不可得时原样返回（探测失败不能缩小窗口）。
func TestFitWindowSizeUnknownWorkArea(t *testing.T) {
	for _, work := range [][2]int{{0, 0}, {-1, 500}, {500, -1}} {
		got := fitWindowSize(mainWindowSize(), work[0], work[1], windowFitMargin)
		if got != mainWindowSize() {
			t.Fatalf("工作区不可得(%v)时应保留理想尺寸: %+v", work, got)
		}
	}
}

// 余量比工作区还大时退回工作区本身，而不是算出负数或 0。
func TestFitWindowSizeHugeMargin(t *testing.T) {
	ideal := mainWindowSize()
	got := fitWindowSize(ideal, 900, 700, 5000)
	wantWidth, wantHeight := 900, 700
	if ideal.Width < wantWidth {
		wantWidth = ideal.Width
	}
	if ideal.Height < wantHeight {
		wantHeight = ideal.Height
	}
	if got.Width != wantWidth || got.Height != wantHeight {
		t.Fatalf("余量过大时应退回工作区与理想尺寸的较小者: got=%dx%d want=%dx%d",
			got.Width, got.Height, wantWidth, wantHeight)
	}
	if got.MinWidth > got.Width || got.MinHeight > got.Height {
		t.Fatalf("最小尺寸不得超过窗口尺寸: %+v", got)
	}
}

// 居中位置：放得下时居中，放不下时贴工作区左上角（不出现负坐标）。
func TestCenteredOrigin(t *testing.T) {
	if x, y := centeredOrigin(1000, 600, 0, 0, 1280, 680); x != 140 || y != 40 {
		t.Fatalf("居中位置 = (%d,%d)，want (140,40)", x, y)
	}
	// 多显示器：工作区原点不在 (0,0) 时也要相对工作区居中。
	if x, y := centeredOrigin(1000, 600, 1920, 0, 1280, 680); x != 2060 || y != 40 {
		t.Fatalf("副屏居中位置 = (%d,%d)，want (2060,40)", x, y)
	}
	if x, y := centeredOrigin(2000, 900, 0, 0, 1280, 680); x != 0 || y != 0 {
		t.Fatalf("放不下时不应产生负坐标 = (%d,%d)", x, y)
	}
	if x, y := centeredOrigin(1000, 600, 0, 0, 0, 0); x != 0 || y != 0 {
		t.Fatalf("工作区不可得时应返回 (0,0)，实际 (%d,%d)", x, y)
	}
}

// 面板高度：矮屏上收敛到工作区底边，高屏上保持理想高度。
func TestFitPanelHeight(t *testing.T) {
	if got := fitPanelHeight(40, 1080, panelIdealHeight); got != panelIdealHeight {
		t.Fatalf("高屏应保持理想高度，实际 %d", got)
	}
	// 1280×720 屏：刘海底边约 46，工作区底边 680。
	got := fitPanelHeight(46, 680, panelIdealHeight)
	if want := 680 - windowFitMargin - 46; got != want {
		t.Fatalf("矮屏应收敛到 %d，实际 %d", want, got)
	}
	// 极端矮屏：不低于最小高度。
	if got := fitPanelHeight(400, 500, panelIdealHeight); got != panelMinFittedHeight {
		t.Fatalf("极矮屏应退到最小高度 %d，实际 %d", panelMinFittedHeight, got)
	}
	// 位置未知时保持理想高度。
	if got := fitPanelHeight(-1, 680, panelIdealHeight); got != panelIdealHeight {
		t.Fatalf("位置未知时应保持理想高度，实际 %d", got)
	}
	if got := fitPanelHeight(46, 0, panelIdealHeight); got != panelIdealHeight {
		t.Fatalf("工作区未知时应保持理想高度，实际 %d", got)
	}
}

// 主窗口选项按收敛后的尺寸生成（含最小尺寸），避免再按硬编码 1240×820 创建。
func TestMainOptionsSizedUsesFittedSize(t *testing.T) {
	size := fitWindowSize(mainWindowSize(), 1280, 680, windowFitMargin)
	opts := mainOptionsSized("system", size)
	if opts.Width != size.Width || opts.Height != size.Height ||
		opts.MinWidth != size.MinWidth || opts.MinHeight != size.MinHeight {
		t.Fatalf("选项尺寸与收敛结果不一致: opts=%dx%d min=%dx%d size=%+v",
			opts.Width, opts.Height, opts.MinWidth, opts.MinHeight, size)
	}
}

// Windows 面板放宽最小高度，才能在小屏上收敛。
func TestPanelChromeRelaxesMinHeightOnWindows(t *testing.T) {
	if goos != "windows" {
		t.Skip("仅 Windows 的 applyPanelChrome 会放宽最小高度")
	}
	opts := panelOptions("system")
	if opts.MinHeight != panelMinFittedHeight {
		t.Fatalf("Windows 面板最小高度应放宽到 %d，实际 %d", panelMinFittedHeight, opts.MinHeight)
	}
	if opts.Width != panelIdealWidth {
		t.Fatalf("面板宽度应保持固定 %d，实际 %d", panelIdealWidth, opts.Width)
	}
}
