package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/applog"
)

// 悬浮球几何与状态的分支覆盖：每个分支都有真实输入驱动，不用 mock。

func TestClampBallXKeepsInsideWorkArea(t *testing.T) {
	area := application.Rect{X: 100, Y: 0, Width: 500, Height: 400}
	cases := []struct {
		x, size int
		want    int
	}{
		{x: 50, size: 64, want: 100},  // 左越界
		{x: 700, size: 64, want: 536}, // 右越界
		{x: 300, size: 64, want: 300}, // 区间内
		{x: 0, size: 500, want: 100},  // 窗口与工作区同宽 → 贴左边
		{x: 0, size: 2000, want: 100}, // 窗口远宽于工作区 → 仍贴左边
	}
	for _, test := range cases {
		if got := clampBallX(area, test.x, test.size); got != test.want {
			t.Errorf("clampBallX(%d, size=%d) = %d，期望 %d", test.x, test.size, got, test.want)
		}
	}
}

func TestBallDockedPositionCoversAllSides(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	// 左：窗口一半出左边界。
	x, y := ballDockedPosition(area, ballDockLeft, 200, 300)
	if x != -ballDockSize/2 || y != 300 {
		t.Fatalf("左贴边应为 %d,%d，实际 %d,%d", -ballDockSize/2, 300, x, y)
	}
	// 右。
	x, _ = ballDockedPosition(area, ballDockRight, 200, 300)
	if want := area.Width - ballDockSize/2; x != want {
		t.Fatalf("右贴边 X 应为 %d，实际 %d", want, x)
	}
	// 上：Y 半出上边界，X 钳进工作区。
	x, y = ballDockedPosition(area, ballDockTop, 5000, 20)
	if want := area.Width - ballDockSize; x != want {
		t.Fatalf("上贴边 X 应钳到 %d，实际 %d", want, x)
	}
	if y != -ballDockSize/2 {
		t.Fatalf("上贴边 Y 应为 %d，实际 %d", -ballDockSize/2, y)
	}
	// 下。
	_, y = ballDockedPosition(area, ballDockBottom, 200, 20)
	if want := area.Height - ballDockSize/2; y != want {
		t.Fatalf("下贴边 Y 应为 %d，实际 %d", want, y)
	}
	// 非法方向：整颗钳回工作区左上角。
	x, y = ballDockedPosition(area, "diagonal", 200, 300)
	if x != 200 || y != 300 {
		t.Fatalf("非法贴边侧应回退到原位，实际 %d,%d", x, y)
	}
}

func TestBallPanelOriginFlipsAndClamps(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	// 球靠左：面板放右侧。
	x, y := ballPanelOrigin(area, 100, 100, ballSize, ballPanelWidth, ballPanelHeight)
	if want := 100 + ballSize + ballPanelGap; x != want {
		t.Fatalf("球靠左时面板应放右侧 %d，实际 %d", want, x)
	}
	if y != 92 {
		t.Fatalf("面板顶应比球顶高 8px，实际 %d", y)
	}
	// 球靠右：右侧放不下，翻到左侧。
	x, _ = ballPanelOrigin(area, area.Width-ballSize-4, 100, ballSize, ballPanelWidth, ballPanelHeight)
	if want := area.Width - ballSize - 4 - ballPanelGap - ballPanelWidth; x != want {
		t.Fatalf("球靠右时面板应翻到左侧 %d，实际 %d", want, x)
	}
	// 球贴底：面板顶被钳进工作区。
	_, y = ballPanelOrigin(area, 100, area.Height-10, ballSize, ballPanelWidth, ballPanelHeight)
	if want := area.Height - ballPanelHeight - ballEdgeMargin; y != want {
		t.Fatalf("面板顶应钳到 %d，实际 %d", want, y)
	}
	// 工作区比面板还窄：钳到左边界且不越界。
	narrow := application.Rect{X: 0, Y: 0, Width: 200, Height: 300}
	x, _ = ballPanelOrigin(narrow, 50, 50, ballSize, ballPanelWidth, ballPanelHeight)
	if x < narrow.X {
		t.Fatalf("窄工作区面板不应越界，实际 %d", x)
	}
	// 工作区比面板还矮：纵向同样钳回工作区内。
	flat := application.Rect{X: 0, Y: 0, Width: 800, Height: 100}
	_, y = ballPanelOrigin(flat, 50, 50, ballSize, ballPanelWidth, ballPanelHeight)
	if y < flat.Y {
		t.Fatalf("扁工作区面板不应越界，实际 %d", y)
	}
}

func TestBallMenuOriginPrefersLeftAndClamps(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	// 中间：菜单在球左侧。
	x, y := ballMenuOrigin(area, 500, 300, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if want := 500 - ballMenuGap - ballMenuWidth; x != want {
		t.Fatalf("菜单应在球左侧 %d，实际 %d", want, x)
	}
	if want := 300 + ballSize/2 - ballMenuHeight/2; y != want {
		t.Fatalf("菜单应与球心对齐 %d，实际 %d", want, y)
	}
	// 球贴左：左侧放不下，翻到右侧。
	x, _ = ballMenuOrigin(area, 10, 300, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if want := 10 + ballSize + ballMenuGap; x != want {
		t.Fatalf("球贴左时菜单应翻到右侧 %d，实际 %d", want, x)
	}
	// 贴顶：菜单顶被钳进工作区（窗口 122 后 ballY=2 已不越界）。
	_, y = ballMenuOrigin(area, 500, -200, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if y != ballEdgeMargin {
		t.Fatalf("菜单顶应钳到 %d，实际 %d", ballEdgeMargin, y)
	}
	// 贴底：菜单底被钳进工作区。
	_, y = ballMenuOrigin(area, 500, area.Height-2, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if want := area.Height - ballMenuHeight - ballEdgeMargin; y != want {
		t.Fatalf("菜单顶应钳到 %d，实际 %d", want, y)
	}
	// 球贴右：菜单左侧放不下 → 先翻到右侧，再被工作区右边界钳回。
	x, _ = ballMenuOrigin(area, area.Width-ballSize-2, 300, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if x < area.X || x+ballMenuWidth > area.X+area.Width {
		t.Fatalf("球贴右时菜单不应越界，实际 %d", x)
	}
	// 工作区比菜单还窄：钳到左边界且不越界。
	tiny := application.Rect{X: 0, Y: 0, Width: 100, Height: 300}
	x, _ = ballMenuOrigin(tiny, 50, 100, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if x < tiny.X {
		t.Fatalf("窄工作区菜单不应越界，实际 %d", x)
	}
	// 球被拖到屏幕左侧之外：菜单翻到右侧后仍要钳回工作区内。
	x, _ = ballMenuOrigin(area, -100, 300, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if x < area.X+ballEdgeMargin {
		t.Fatalf("越界球的菜单应钳回工作区，实际 %d", x)
	}
	// 工作区比菜单还矮：纵向钳回。
	short := application.Rect{X: 0, Y: 0, Width: 800, Height: 60}
	_, y = ballMenuOrigin(short, 400, 30, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if y < short.Y {
		t.Fatalf("扁工作区菜单不应越界，实际 %d", y)
	}
}

func TestSaveBarStateHandlesMissingDirAndFailure(t *testing.T) {
	// 空路径（未初始化配置目录）：静默跳过，不报错。
	if err := saveBarState("", barWindowState{X: 1}); err != nil {
		t.Fatalf("空路径应静默跳过，实际 %v", err)
	}
	// 目标目录其实是个文件：写入必须报错，而不是静默吞掉。
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveBarState(filepath.Join(blocker, "state.json"), barWindowState{X: 1}); err == nil {
		t.Fatal("写入不存在的目录应报错")
	}
}

func TestBarStatePathEmptyWithoutApplog(t *testing.T) {
	// applog 未初始化时配置目录未知：路径为空，调用方据此跳过读写。
	if path := barStatePath(); path != "" {
		t.Fatalf("未初始化日志时状态路径应为空，实际 %q", path)
	}
}

func TestShapeBarWindowWithoutNativeWindowIsSafe(t *testing.T) {
	// 没有原生窗口（零值 App / 非 Windows）时裁剪必须安全返回 false。
	if shapeBarWindowSide(nil, ballDockLeft) {
		t.Fatal("无原生窗口时裁剪应返回 false")
	}
	shapeBarWindow(nil)
}

// TestBarStatePathUsesConfiguredDir 验证 applog 初始化后状态路径落在配置目录里
// （真实初始化真实目录，不 mock）。
func TestBarStatePathUsesConfiguredDir(t *testing.T) {
	// 用自建临时目录：applog 会一直持有日志文件句柄，t.TempDir 的清理会失败。
	dir, err := os.MkdirTemp("", "qka-applog-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	closeLogs, err := applog.Init(dir, "test")
	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	// 先关日志再删目录（defer 后注册者先执行）。
	defer closeLogs()
	path := barStatePath()
	want := filepath.Join(dir, barStateFileName)
	if path != want {
		t.Fatalf("状态路径应为 %q，实际 %q", want, path)
	}
}
