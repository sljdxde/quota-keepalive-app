package gui

import (
	"testing"
	"time"
)

// 宽度按步长向上取整，空闲「0 tok/s」的实测宽度（约 86pt）只补到 90pt，
// 不再像旧实现那样永久预留 101pt 并按最宽文案补满。
func TestMenuBarQuantizedWidth(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "非正数回退最小档", in: 0, want: menuBarWidthStep},
		{name: "空闲标签", in: 85.94, want: 90},
		{name: "三位数标签", in: 94.1, want: 96},
		{name: "恰好整档", in: 90, want: 90},
		{name: "极小值", in: 0.4, want: menuBarWidthStep},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := menuBarQuantizedWidth(tc.in); got != tc.want {
				t.Fatalf("menuBarQuantizedWidth(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMenuBarDecideWidthGrowsImmediately(t *testing.T) {
	now := time.Now()

	got, since := menuBarDecideWidth(0, 90, time.Time{}, now)
	if got != 90 || !since.IsZero() {
		t.Fatalf("首次测量应立即采用目标宽度：got=%v since=%v", got, since)
	}

	got, since = menuBarDecideWidth(90, 96, time.Time{}, now)
	if got != 96 || !since.IsZero() {
		t.Fatalf("变宽应立即生效：got=%v since=%v", got, since)
	}
}

// 变窄要连续观察一段时间，避免速度数字上下跳变时把相邻状态项来回推。
func TestMenuBarDecideWidthDebouncesShrink(t *testing.T) {
	start := time.Now()

	width, since := menuBarDecideWidth(96, 90, time.Time{}, start)
	if width != 96 || !since.Equal(start) {
		t.Fatalf("刚变窄时保持原宽并记时：width=%v since=%v", width, since)
	}

	width, since = menuBarDecideWidth(96, 90, since, start.Add(menuBarShrinkWait-time.Nanosecond))
	if width != 96 || !since.Equal(start) {
		t.Fatalf("观察期未满不应收紧：width=%v since=%v", width, since)
	}

	width, since = menuBarDecideWidth(96, 90, since, start.Add(menuBarShrinkWait))
	if width != 90 || !since.IsZero() {
		t.Fatalf("观察期满应收紧并清计时：width=%v since=%v", width, since)
	}
}

// 变宽会打断变窄计时；宽度不变也要清计时，防止下一轮误判为「持续变窄」。
func TestMenuBarDecideWidthResetsTimer(t *testing.T) {
	start := time.Now()
	narrow := start.Add(-time.Second)

	width, since := menuBarDecideWidth(96, 102, narrow, start)
	if width != 102 || !since.IsZero() {
		t.Fatalf("变宽应清掉变窄计时：width=%v since=%v", width, since)
	}

	width, since = menuBarDecideWidth(96, 96, narrow, start)
	if width != 96 || !since.IsZero() {
		t.Fatalf("宽度不变应清掉变窄计时：width=%v since=%v", width, since)
	}
}
