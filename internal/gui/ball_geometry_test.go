package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 悬浮球的几何计算与位置持久化都是纯函数/文件操作，不开窗口即可验证。

func primaryArea() application.Rect {
	return application.Rect{X: 0, Y: 0, Width: 1920, Height: 1040}
}

func TestBallDefaultOriginCentersOnWorkArea(t *testing.T) {
	x, y := ballDefaultOrigin(primaryArea())
	if want := (1920 - ballSize) / 2; x != want {
		t.Fatalf("默认 X 应为主屏居中 %d，实际 %d", want, x)
	}
	if y != ballTopGap {
		t.Fatalf("默认 Y 应为 %d，实际 %d", ballTopGap, y)
	}
	// 工作区原点不在 (0,0)（多屏副屏）时也要跟着偏移。
	x, y = ballDefaultOrigin(application.Rect{X: 1920, Y: -200, Width: 1280, Height: 1024})
	if want := 1920 + (1280-ballSize)/2; x != want {
		t.Fatalf("副屏默认 X 应为 %d，实际 %d", want, x)
	}
	if y != -200+ballTopGap {
		t.Fatalf("副屏默认 Y 应为 %d，实际 %d", -200+ballTopGap, y)
	}
}

func TestClampBallPositionKeepsBallInsideWorkArea(t *testing.T) {
	area := primaryArea()
	cases := []struct {
		x, y         int
		wantX, wantY int
		description  string
	}{
		{-50, -50, 0, 0, "左上越界钳回原点"},
		{5000, 5000, 1920 - ballSize, 1040 - ballSize, "右下越界钳回右下角"},
		{100, 200, 100, 200, "工作区内不动"},
	}
	for _, test := range cases {
		x, y := clampBallPosition(area, test.x, test.y, ballSize)
		if x != test.wantX || y != test.wantY {
			t.Errorf("%s：得到 (%d,%d)，期望 (%d,%d)", test.description, x, y, test.wantX, test.wantY)
		}
	}
	// 工作区比球还小时保留左上角，不产生负坐标。
	x, y := clampBallPosition(application.Rect{Width: 20, Height: 20}, 50, 50, ballSize)
	if x != 0 || y != 0 {
		t.Fatalf("超小工作区应钳到原点，实际 (%d,%d)", x, y)
	}
}

func TestBallDockSideForDetectsScreenEdges(t *testing.T) {
	area := primaryArea()
	cases := []struct {
		x    int
		want string
	}{
		{area.X, ballDockLeft},
		{ballDockThreshold, ballDockLeft},
		{ballDockThreshold + 1, ""},
		{600, ""},
		{area.Width - ballSize - ballDockThreshold, ballDockRight},
		{area.Width - ballSize, ballDockRight},
	}
	for _, test := range cases {
		if got := ballDockSideFor(area, test.x, 300, ballSize); got != test.want {
			t.Errorf("x=%d 贴边侧应为 %q，实际 %q", test.x, test.want, got)
		}
	}
	if got := ballDockSideFor(application.Rect{}, 0, 0, ballSize); got != "" {
		t.Fatalf("未知工作区不应贴边，实际 %q", got)
	}
}

func TestBallDockedPositionHidesHalfOutside(t *testing.T) {
	area := primaryArea()
	x, y := ballDockedPosition(area, ballDockLeft, 40, 300)
	if x != -ballDockSize/2 {
		t.Fatalf("左贴边 X 应为 %d，实际 %d", -ballDockSize/2, x)
	}
	if y != 300 {
		t.Fatalf("Y 应保持 300，实际 %d", y)
	}
	x, _ = ballDockedPosition(area, ballDockRight, 40, 300)
	if want := area.Width - ballDockSize/2; x != want {
		t.Fatalf("右贴边 X 应为 %d，实际 %d", want, x)
	}
	// 纵向越界仍要钳进工作区。
	_, y = ballDockedPosition(area, ballDockLeft, 40, area.Height+100)
	if want := area.Height - ballDockSize; y != want {
		t.Fatalf("贴边 Y 应钳到 %d，实际 %d", want, y)
	}
	// 上贴边：Y 半出屏，X 钳进工作区。
	x, y = ballDockedPosition(area, ballDockTop, 5000, 40)
	if want := area.Width - ballDockSize; x != want {
		t.Fatalf("上贴边 X 应钳到 %d，实际 %d", want, x)
	}
	if y != -ballDockSize/2 {
		t.Fatalf("上贴边 Y 应为 %d，实际 %d", -ballDockSize/2, y)
	}
	// 下贴边。
	x, y = ballDockedPosition(area, ballDockBottom, 100, 40)
	if x != 100 {
		t.Fatalf("下贴边 X 应保持 100，实际 %d", x)
	}
	if want := area.Height - ballDockSize/2; y != want {
		t.Fatalf("下贴边 Y 应为 %d，实际 %d", want, y)
	}
}

func TestBallDockSideForDetectsAllEdges(t *testing.T) {
	area := primaryArea()
	cases := []struct {
		x, y int
		want string
	}{
		{600, area.Y, ballDockTop},
		{600, area.Height - ballSize, ballDockBottom},
		{area.X, 300, ballDockLeft},
		{area.Width - ballSize, 300, ballDockRight},
		{600, 300, ""},
	}
	for _, test := range cases {
		if got := ballDockSideFor(area, test.x, test.y, ballSize); got != test.want {
			t.Errorf("(%d,%d) 贴边侧应为 %q，实际 %q", test.x, test.y, test.want, got)
		}
	}
}

func TestBallPanelOriginPrefersRightThenLeft(t *testing.T) {
	area := primaryArea()
	// 中间位置：面板在球右侧。
	x, y := ballPanelOrigin(area, 900, 200, ballSize, ballPanelWidth, ballPanelHeight)
	if want := 900 + ballSize + ballPanelGap; x != want {
		t.Fatalf("面板应在球右侧 %d，实际 %d", want, x)
	}
	if y != 200-8 {
		t.Fatalf("面板顶应比球顶高 8px，实际 %d", y)
	}
	// 靠右边缘：右侧放不下，改到左侧。
	x, _ = ballPanelOrigin(area, area.Width-ballSize-4, 200, ballSize, ballPanelWidth, ballPanelHeight)
	if want := area.Width - ballSize - 4 - ballPanelGap - ballPanelWidth; x != want {
		t.Fatalf("右侧放不下时面板应在左侧 %d，实际 %d", want, x)
	}
	// 靠下边缘：纵向钳进工作区。
	_, y = ballPanelOrigin(area, 900, area.Height-10, ballSize, ballPanelWidth, ballPanelHeight)
	if want := area.Height - ballPanelHeight - ballEdgeMargin; y != want {
		t.Fatalf("面板应钳到工作区内 %d，实际 %d", want, y)
	}
	// 工作区较窄：两侧都放不下时贴左边界（panel 244 时贴 ballEdgeMargin）。
	x, _ = ballPanelOrigin(application.Rect{Width: 300, Height: 600}, 100, 100, ballSize, 244, ballPanelHeight)
	if want := ballEdgeMargin; x != want {
		t.Fatalf("窄屏面板应贴左边界 %d，实际 %d", want, x)
	}
	// 面板比工作区还宽（392 > 300）：8px 边距没有意义，钳到左边界 0，
	// 至少能多显示几列内容。
	x, _ = ballPanelOrigin(application.Rect{Width: 300, Height: 600}, 100, 100, ballSize, ballPanelWidth, ballPanelHeight)
	if x != 0 {
		t.Fatalf("超宽面板应钳到左边界 0，实际 %d", x)
	}
}

func TestBallMenuOriginPrefersLeftThenRight(t *testing.T) {
	area := primaryArea()
	// 中间位置：菜单在球左侧，垂直与球心对齐。
	x, y := ballMenuOrigin(area, 900, 200, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if want := 900 - ballMenuGap - ballMenuWidth; x != want {
		t.Fatalf("菜单应在球左侧 %d，实际 %d", want, x)
	}
	if want := 200 + ballSize/2 - ballMenuHeight/2; y != want {
		t.Fatalf("菜单应与球心对齐 %d，实际 %d", want, y)
	}
	// 靠左边缘：左侧放不下，改到右侧。
	x, _ = ballMenuOrigin(area, 20, 200, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if want := 20 + ballSize + ballMenuGap; x != want {
		t.Fatalf("左侧放不下时菜单应在右侧 %d，实际 %d", want, x)
	}
	// 靠上边缘：纵向钳进工作区（窗口 122 后 ballY=2 已不再触发钳制，
	// 用负坐标制造确实越界的情况）。
	_, y = ballMenuOrigin(area, 900, -200, ballSize, ballSize, ballMenuWidth, ballMenuHeight)
	if want := ballEdgeMargin; y != want {
		t.Fatalf("菜单应钳到工作区内 %d，实际 %d", want, y)
	}
}

func TestBarStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), barStateFileName)
	state := barWindowState{X: 120, Y: 240, Docked: ballDockRight, RestoreX: 800, RestoreY: 300}
	if err := saveBarState(path, state); err != nil {
		t.Fatalf("保存状态失败：%v", err)
	}
	loaded, ok := loadBarState(path)
	if !ok {
		t.Fatal("应能读回刚保存的状态")
	}
	if loaded.X != state.X || loaded.Y != state.Y || loaded.Docked != state.Docked ||
		loaded.RestoreX != state.RestoreX || loaded.RestoreY != state.RestoreY {
		t.Fatalf("状态往返不一致：%+v", loaded)
	}
}

func TestLoadBarStateRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"空对象":    "{}",
		"坏 JSON": "{",
		"版本不符":   `{"version":99,"x":1,"y":2}`,
	}
	for name, body := range cases {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := loadBarState(path); ok {
			t.Errorf("%s 不应被当作有效状态", name)
		}
	}
	if _, ok := loadBarState(filepath.Join(dir, "missing.json")); ok {
		t.Error("缺失文件不应被当作有效状态")
	}
	if _, ok := loadBarState(""); ok {
		t.Error("空路径不应被当作有效状态")
	}
	// 非法贴边侧被清空，但坐标仍然可用（top 现在是合法方向）。
	path := filepath.Join(dir, "dock.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"x":10,"y":20,"docked":"sideways"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, ok := loadBarState(path)
	if !ok || state.Docked != "" {
		t.Fatalf("非法贴边侧应清空，实际 %+v ok=%v", state, ok)
	}
}
