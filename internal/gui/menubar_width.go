package gui

import (
	"math"
	"sync"
	"time"
)

const (
	// menuBarWidthStep 是状态项宽度的量化步长（点）：数字位数变化只在跨档时
	// 挪动相邻状态项，档内抖动被吃掉，右侧补白最多一个步长。
	menuBarWidthStep = 6.0
	// menuBarShrinkWait 是变窄前的观察期：速度数字频繁跳变时不要来回推挤
	// 相邻状态项，连续这么久都更窄才收紧。
	menuBarShrinkWait = 2 * time.Second
)

// menuBarWidthState 记录当前生效的宽度与最近一次布局的输入/产物。
// macOS 的菜单栏状态项每次 Auto Layout 都有空闲 CPU 成本，所以只有在
// 文案变化或变窄观察期到期时才重新读 AppKit；其余刷新直接复用补白标签。
var menuBarWidthState struct {
	mu          sync.Mutex
	width       float64
	target      float64
	narrowSince time.Time

	ready     bool
	visible   string
	label     string
	padded    string
	textWidth float64
	overhead  float64
}

// menuBarQuantizedWidth 把实测宽度向上取整到步长档位。
func menuBarQuantizedWidth(required float64) float64 {
	if required <= 0 {
		return menuBarWidthStep
	}
	width := math.Ceil(required/menuBarWidthStep) * menuBarWidthStep
	if width < menuBarWidthStep {
		return menuBarWidthStep
	}
	return width
}

// menuBarDecideWidth 是纯逻辑，便于在没有 AppKit 的测试里覆盖：
// 首次测量或变宽立即生效；变窄要连续观察一段时间，避免数字跳变时来回推挤。
func menuBarDecideWidth(current, target float64, narrowSince, now time.Time) (float64, time.Time) {
	switch {
	case current <= 0 || target > current:
		return target, time.Time{}
	case target < current:
		if narrowSince.IsZero() {
			return current, now
		}
		if now.Sub(narrowSince) >= menuBarShrinkWait {
			return target, time.Time{}
		}
		return current, narrowSince
	default:
		return current, time.Time{}
	}
}
