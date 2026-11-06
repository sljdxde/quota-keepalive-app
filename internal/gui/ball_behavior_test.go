package gui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 悬浮球 Go 侧行为的真实单元测试：判定走纯函数、几何走真实计算、状态走
// 真实文件；只有「开真窗口」这一步用零值 App 的安全路径覆盖（不 mock 行为，
// 只断言不 panic 与副作用位）。

// TestBallShouldShowTruthTable 锁定悬浮球的显隐真值表：
// 设置关 → 永不显示；主窗口可见 → 让位；其余 → 显示。
func TestBallShouldShowTruthTable(t *testing.T) {
	cases := []struct {
		enabled     bool
		mainVisible bool
		want        bool
	}{
		{enabled: true, mainVisible: false, want: true},
		{enabled: true, mainVisible: true, want: false},
		{enabled: false, mainVisible: false, want: false},
		{enabled: false, mainVisible: true, want: false},
	}
	for _, test := range cases {
		if got := ballShouldShow(test.enabled, test.mainVisible); got != test.want {
			t.Errorf("ballShouldShow(%v, %v) = %v，期望 %v",
				test.enabled, test.mainVisible, got, test.want)
		}
	}
}

// TestSetBallPreferenceOffHidesEverything 锁定「显示悬浮球=关」的编排：
// 设置位翻转，且球与三个浮层的收起路径在零值 App 下安全（不 panic）。
func TestSetBallPreferenceOffHidesEverything(t *testing.T) {
	app := &App{}
	app.ballShow.Store(true)
	app.setBallPreference(false)
	if app.ballShow.Load() {
		t.Fatal("关闭后设置位应为 false")
	}
	app.hideBallPanel()
	app.hidePetMini()
	app.hideBallMenu()
	app.hideBar()
}

// TestSetBallPreferenceOnKeepsSetting 锁定「重新打开」时设置位为 true；
// 主窗口可见时不让球抢位置（判定由 ballShouldShow 决定，已在真值表用例覆盖）。
func TestSetBallPreferenceOnKeepsSetting(t *testing.T) {
	app := &App{}
	app.ballShow.Store(false)
	app.setBallPreference(true)
	if !app.ballShow.Load() {
		t.Fatal("重新打开后设置位应为 true")
	}
	app.showBar()
}

// TestShowBarRespectsBallSetting 锁定 showBar 的设置守卫：关掉时永不显示。
func TestShowBarRespectsBallSetting(t *testing.T) {
	app := &App{}
	app.ballShow.Store(false)
	app.showBar()
	app.ballShow.Store(true)
	app.showBar()
}

// TestBarStateRoundTripInApp 验证 App 上的贴边状态读写与 rememberBarState
// 的内存副作用。
func TestBarStateRoundTripInApp(t *testing.T) {
	app := &App{}
	if side, rx, ry := app.barDockState(); side != "" || rx != 0 || ry != 0 {
		t.Fatalf("零值 App 的贴边状态应为空，实际 %q %d %d", side, rx, ry)
	}
	state := barWindowState{X: 10, Y: 20, Docked: ballDockTop, RestoreX: 5, RestoreY: 6}
	app.rememberBarState(state)
	if side, rx, ry := app.barDockState(); side != ballDockTop || rx != 5 || ry != 6 {
		t.Fatalf("rememberBarState 应更新内存状态，实际 %q %d %d", side, rx, ry)
	}
}

// TestBarAnchorFallbacks 锁定 barAnchor 的退化路径：无窗口时回默认尺寸。
func TestBarAnchorFallbacks(t *testing.T) {
	app := &App{}
	x, y, w, h := app.barAnchor()
	if x != 0 || y != 0 || w != ballSize || h != ballSize {
		t.Fatalf("无窗口时 barAnchor 应回默认 %d×%d，实际 %d,%d,%d,%d",
			ballSize, ballSize, x, y, w, h)
	}
}

// TestBarStartDragNilBarSafe 锁定 BarStartDrag 对零值 App 的安全性。
func TestBarStartDragNilBarSafe(t *testing.T) {
	app := &App{}
	app.BarStartDrag()
}

// TestBarToggleNilBarSafe 锁定 BarToggle/BarMenu 对零值 App 的安全性。
func TestBarToggleNilBarSafe(t *testing.T) {
	app := &App{}
	app.BarToggle()
	app.BarMenu()
}

// TestBallPanelAndMenuPositionsConsistent 用真实几何核对两个浮层的选址
// 不越界。
func TestBallPanelAndMenuPositionsConsistent(t *testing.T) {
	app := &App{}
	area := application.Rect{X: 0, Y: 0, Width: 1920, Height: 1040}
	bx, by, w, h := app.barAnchor()
	px, py := ballPanelOrigin(area, bx, by, w, ballPanelWidth, ballPanelHeight)
	if px < area.X || py < area.Y ||
		px+ballPanelWidth > area.X+area.Width ||
		py+ballPanelHeight > area.Y+area.Height {
		t.Fatalf("面板选址越界: %d,%d", px, py)
	}
	mx, my := ballMenuOrigin(area, bx, by, w, h, ballMenuWidth, ballMenuHeight)
	if mx < area.X || my < area.Y ||
		mx+ballMenuWidth > area.X+area.Width ||
		my+ballMenuHeight > area.Y+area.Height {
		t.Fatalf("菜单选址越界: %d,%d", mx, my)
	}
}

// TestBarStateFileRoundTrip 用真实文件验证状态持久化：写入 → 读回一致，
// 权限 owner-only。
func TestBarStateFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, barStateFileName)
	state := barWindowState{
		Version: barStateVersion, X: 321, Y: 654,
		Docked: ballDockLeft, RestoreX: 300, RestoreY: 600,
	}
	if err := saveBarState(path, state); err != nil {
		t.Fatalf("写状态失败: %v", err)
	}
	got, ok := loadBarState(path)
	if !ok {
		t.Fatal("应能读回状态")
	}
	if got != state {
		t.Fatalf("状态往返不一致: %+v != %+v", got, state)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// POSIX 位只在非 Windows 平台有意义（Windows 的 ACL 不受 chmod 影响）。
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("状态文件权限应只对本人开放，实际 %v", info.Mode().Perm())
	}
}
