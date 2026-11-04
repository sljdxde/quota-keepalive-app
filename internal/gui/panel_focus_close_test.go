package gui

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/events"
)

// fakeFocusScheduler 收集 panelFocusGuard 登记的延迟回调，测试里手动触发，
// 不依赖真实时钟（参照 window_behavior_test.go 的不开窗口纯 Go 测试风格）。
type fakeFocusScheduler struct {
	timers []*fakeFocusTimer
}

type fakeFocusTimer struct {
	delay time.Duration
	fn    func()
	fired bool
}

func (s *fakeFocusScheduler) after(d time.Duration, fn func()) {
	s.timers = append(s.timers, &fakeFocusTimer{delay: d, fn: fn})
}

// fire 触发第 i 个定时器；越界或重复触发直接忽略。
func (s *fakeFocusScheduler) fire(i int) {
	if i < 0 || i >= len(s.timers) {
		return
	}
	timer := s.timers[i]
	if timer.fired {
		return
	}
	timer.fired = true
	timer.fn()
}

// newGuardForTest 构造接了假时钟的守卫，返回隐藏计数供断言。
func newGuardForTest(delay time.Duration, focused func() bool, sched *fakeFocusScheduler) (*panelFocusGuard, *int) {
	hidden := 0
	g := newPanelFocusGuard(delay, func() { hidden++ }, focused)
	g.after = sched.after
	return g, &hidden
}

func notFocused() bool { return false }
func focusedNow() bool { return true }

// 失焦后无任何干预：延迟到期、面板确实没有焦点，就收起，且只收一次。
func TestPanelFocusGuardHidesAfterDelayWithoutIntervention(t *testing.T) {
	sched := &fakeFocusScheduler{}
	guard, hidden := newGuardForTest(250*time.Millisecond, notFocused, sched)

	guard.focusLost()
	if len(sched.timers) != 1 {
		t.Fatalf("失焦应登记一个延迟收起，实际 %d 个", len(sched.timers))
	}
	if sched.timers[0].delay != 250*time.Millisecond {
		t.Errorf("登记的延迟应等于守卫延迟，实际 %v", sched.timers[0].delay)
	}
	// 重复触发放弃（模拟真实定时器只响一次）。
	sched.fire(0)
	sched.fire(0)
	if *hidden != 1 {
		t.Fatalf("无干预时到期应收起一次，实际 %d 次", *hidden)
	}
}

// 到期复核时面板实际持有焦点（Show+Focus 的焦点抖动、或焦点已经回来）：
// 不收起。这正是 Win10 上原生 HideOnFocusLost「面板弹出后立刻自己消失」
// 要防的场景。
func TestPanelFocusGuardSkipsHideWhenPanelRefocusedAtDeadline(t *testing.T) {
	sched := &fakeFocusScheduler{}
	guard, hidden := newGuardForTest(250*time.Millisecond, focusedNow, sched)

	guard.focusLost()
	sched.fire(0)
	if *hidden != 0 {
		t.Fatalf("到期时面板仍持有焦点不应收起，实际收起 %d 次", *hidden)
	}
}

// showPanel/togglePanel* 入口的 noteShowIntended 作废挂起的收起。
func TestPanelFocusGuardShowIntendedCancelsPendingHide(t *testing.T) {
	sched := &fakeFocusScheduler{}
	guard, hidden := newGuardForTest(250*time.Millisecond, notFocused, sched)

	guard.focusLost()
	guard.noteShowIntended()
	sched.fire(0)
	if *hidden != 0 {
		t.Fatalf("显示意图应作废挂起的收起，实际收起 %d 次", *hidden)
	}
}

// 面板已被其它路径收起（Esc/退出按钮/toggle 隐藏分支）：挂起的收起作废。
func TestPanelFocusGuardHiddenCancelsPendingHide(t *testing.T) {
	sched := &fakeFocusScheduler{}
	guard, hidden := newGuardForTest(250*time.Millisecond, notFocused, sched)

	guard.focusLost()
	guard.noteHidden()
	sched.fire(0)
	if *hidden != 0 {
		t.Fatalf("已收起应作废挂起的收起，实际又收起 %d 次", *hidden)
	}
}

// 焦点回到面板（WindowFocus 事件）：挂起的收起作废。
func TestPanelFocusGuardFocusRegainedCancelsPendingHide(t *testing.T) {
	sched := &fakeFocusScheduler{}
	guard, hidden := newGuardForTest(250*time.Millisecond, notFocused, sched)

	guard.focusLost()
	guard.noteFocusRegained()
	sched.fire(0)
	if *hidden != 0 {
		t.Fatalf("回焦应作废挂起的收起，实际收起 %d 次", *hidden)
	}
}

// 连续失焦只认最新一次登记：旧登记到期是过期代数，不得收起。
func TestPanelFocusGuardOnlyLatestRegistrationHides(t *testing.T) {
	sched := &fakeFocusScheduler{}
	guard, hidden := newGuardForTest(250*time.Millisecond, notFocused, sched)

	guard.focusLost()
	guard.focusLost()
	if len(sched.timers) != 2 {
		t.Fatalf("两次失焦应登记两次，实际 %d 次", len(sched.timers))
	}
	sched.fire(0)
	if *hidden != 0 {
		t.Fatalf("过期登记不得收起，实际收起 %d 次", *hidden)
	}
	sched.fire(1)
	if *hidden != 1 {
		t.Fatalf("最新登记到期应收起一次，实际收起 %d 次", *hidden)
	}
}

// 非 Windows 平台不装守卫，所有方法对 nil 接收者必须安全（调用点不判空）。
func TestPanelFocusGuardNilReceiverIsSafe(t *testing.T) {
	var guard *panelFocusGuard
	guard.focusLost()
	guard.noteShowIntended()
	guard.noteHidden()
	guard.noteFocusRegained()
	guard.fire(1)
}

// issue #5 要求失焦缓冲在 100-300ms：太短挡不住刘海点击竞态，太长「点外部
// 收起」会显得迟钝。
func TestPanelFocusHideDelayWithinIssueBounds(t *testing.T) {
	if panelFocusHideDelay < 100*time.Millisecond || panelFocusHideDelay > 300*time.Millisecond {
		t.Fatalf("失焦收起缓冲应在 100-300ms，实际 %v", panelFocusHideDelay)
	}
}

// waitForFocusTimer 轮询等待守卫登记延迟收起：HandleWindowEvent 的监听器
// 由 Wails 派发到独立 goroutine，到达有先后。
func waitForFocusTimer(t *testing.T, sched *fakeFocusScheduler, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sched.timers) >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待 %d 个失焦登记超时，实际 %d 个", want, len(sched.timers))
}

// waitForGeneration 轮询等待守卫换代（异步监听器已生效）。
func waitForGeneration(t *testing.T, guard *panelFocusGuard, want uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if guard.currentGeneration() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待守卫换代到 %d 超时，实际 %d", want, guard.currentGeneration())
}

// armPanelFocusGuardForTest 把生产守卫的三个注入点换成测试替身：hide 计数、
// 焦点状态、手动触发的假时钟。闭包捕获的是同一个 *panelFocusGuard，替换
// 字段即可生效。
func armPanelFocusGuardForTest(t *testing.T, app *App, focused func() bool) (*fakeFocusScheduler, *int32) {
	t.Helper()
	if app.panelFocus == nil {
		t.Fatal("Windows 上面板创建后应安装失焦收起守卫")
	}
	var hidden int32
	sched := &fakeFocusScheduler{}
	app.panelFocus.hide = func() { atomic.AddInt32(&hidden, 1) }
	app.panelFocus.isFocused = focused
	app.panelFocus.after = sched.after
	return sched, &hidden
}

// 窗口失焦事件（数值 1038）确实接到守卫：点桌面/其它应用 → 延迟到期 →
// 面板无焦点 → 收起。
func TestPanelLostFocusEventHidesViaGuard(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("失焦收起守卫仅 Windows 安装")
	}
	app := New(Deps{})
	panel := app.ensurePanel()
	sched, hidden := armPanelFocusGuardForTest(t, app, notFocused)

	panel.HandleWindowEvent(uint(events.Common.WindowLostFocus))
	waitForFocusTimer(t, sched, 1)
	sched.fire(0)
	if atomic.LoadInt32(hidden) != 1 {
		t.Fatalf("失焦无干预应在延迟到期后收起，实际 %d 次", atomic.LoadInt32(hidden))
	}
}

// 关键竞态（issue #5）：面板已展开，点击刘海——面板先失焦（守卫登记延迟
// 收起），刘海的点击后到 togglePanelAtNotch。toggle 入口作废登记后再按可见
// 性切换，延迟到期不得再补一刀，否则「点了刘海，面板闪关/关了又弹出」。
func TestTogglePanelAtNotchCancelsPendingFocusHide(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("失焦收起守卫仅 Windows 安装")
	}
	app := New(Deps{})
	panel := app.ensurePanel()
	sched, hidden := armPanelFocusGuardForTest(t, app, notFocused)

	// 竞态重放：先失焦……
	panel.HandleWindowEvent(uint(events.Common.WindowLostFocus))
	waitForFocusTimer(t, sched, 1)
	// ……随后刘海的点击才到达 toggle。
	app.togglePanelAtNotch(0, 0)
	sched.fire(0)
	if atomic.LoadInt32(hidden) != 0 {
		t.Fatalf("刘海点击路径应作废挂起的失焦收起，实际又收起 %d 次", atomic.LoadInt32(hidden))
	}
}

// showPanel（托盘/调试入口）同样先作废挂起的收起：面板弹出瞬间的失焦
// 登记不该反过来把面板收回去。
func TestShowPanelCancelsPendingFocusHide(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("失焦收起守卫仅 Windows 安装")
	}
	app := New(Deps{})
	panel := app.ensurePanel()
	sched, hidden := armPanelFocusGuardForTest(t, app, notFocused)

	panel.HandleWindowEvent(uint(events.Common.WindowLostFocus))
	waitForFocusTimer(t, sched, 1)
	app.showPanel()
	sched.fire(0)
	if atomic.LoadInt32(hidden) != 0 {
		t.Fatalf("显示面板应作废挂起的失焦收起，实际又收起 %d 次", atomic.LoadInt32(hidden))
	}
}

// hidePanel（面板退出按钮/collapse-panel 事件/showMain 前置收起）作废挂起
// 的收起，避免延迟到期后二次 Hide。
func TestHidePanelCancelsPendingFocusHide(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("失焦收起守卫仅 Windows 安装")
	}
	app := New(Deps{})
	panel := app.ensurePanel()
	sched, hidden := armPanelFocusGuardForTest(t, app, notFocused)

	panel.HandleWindowEvent(uint(events.Common.WindowLostFocus))
	waitForFocusTimer(t, sched, 1)
	app.hidePanel()
	sched.fire(0)
	if atomic.LoadInt32(hidden) != 0 {
		t.Fatalf("主动收起应作废挂起的失焦收起，实际又收起 %d 次", atomic.LoadInt32(hidden))
	}
}

// 焦点回到面板（WindowFocus，数值 1035）：失焦只是抖动，挂起的收起作废。
func TestPanelWindowFocusEventCancelsPendingHide(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("失焦收起守卫仅 Windows 安装")
	}
	app := New(Deps{})
	panel := app.ensurePanel()
	sched, hidden := armPanelFocusGuardForTest(t, app, notFocused)

	before := app.panelFocus.currentGeneration()
	panel.HandleWindowEvent(uint(events.Common.WindowLostFocus))
	waitForFocusTimer(t, sched, 1)
	panel.HandleWindowEvent(uint(events.Common.WindowFocus))
	// 失焦与回焦两个监听器各自异步派发，等回焦那次换代真正生效再触发定时器。
	waitForGeneration(t, app.panelFocus, before+2)
	sched.fire(0)
	if atomic.LoadInt32(hidden) != 0 {
		t.Fatalf("回焦事件应作废挂起的失焦收起，实际又收起 %d 次", atomic.LoadInt32(hidden))
	}
}

// 守卫只在 Windows 安装：macOS 继续走 NSPanel 原生 HideOnFocusLost。
func TestPanelFocusGuardOnlyInstalledOnWindows(t *testing.T) {
	app := New(Deps{})
	app.ensurePanel()
	if runtime.GOOS == "windows" && app.panelFocus == nil {
		t.Fatal("Windows 上面板创建后应安装失焦收起守卫")
	}
	if runtime.GOOS != "windows" && app.panelFocus != nil {
		t.Fatal("非 Windows 平台不应安装失焦收起守卫（macOS 走原生失焦即隐）")
	}
}
