package gui

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/events"
)

// 主窗口可见时收起常驻刘海、关闭主窗口后恢复：刘海是独立置顶窗口，钉在
// 屏幕顶部正中，和主窗口顶栏居中的导航会精确重合（「0 tok/s」胶囊压住
// 「宠物设置」页签）。收起/恢复用 barShow/barHide 接缝观测，不创建真窗口。
func TestMainWindowVisibilityDrivesBar(t *testing.T) {
	app := New(Deps{})

	var shows, hides atomic.Int32
	app.barShow = func() { shows.Add(1) }
	app.barHide = func() { hides.Add(1) }

	// 打开主窗口：必须收起刘海（不能反过来显示）。
	app.showMain()
	if got := hides.Load(); got != 1 {
		t.Fatalf("showMain 应收起刘海 %d 次，实际 %d", 1, got)
	}
	if got := shows.Load(); got != 0 {
		t.Fatalf("showMain 不应显示刘海，实际显示 %d 次", got)
	}

	// 关闭主窗口（只隐藏、不销毁）：刘海必须回来。
	app.ensureMainWindow().HandleWindowEvent(uint(events.Common.WindowClosing))
	deadline := time.Now().Add(2 * time.Second)
	for shows.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := shows.Load(); got != 1 {
		t.Fatalf("关闭主窗口后应恢复刘海 %d 次，实际 %d", 1, got)
	}
}

// 没有注入接缝时（barShow/barHide 为 nil）也不应 panic：其它装配路径可能
// 构造出不带接缝的 App。
func TestHideRestoredWindowsWithoutBarSeamsIsSafe(t *testing.T) {
	app := &App{}
	app.hideRestoredWindows()
}
