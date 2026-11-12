package gui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/applog"
	"qka/internal/atomicfile"
)

// Windows 悬浮球（菜单栏状态项的 Windows 形态）的几何与位置持久化。
// 这些函数只算数、不碰窗口，便于单测；真正 SetPosition/SetSize 在
// bar_windows.go / app.go 里。
const (
	// ballSize 是浮动态直径。真机反馈 64px 太大，收到 52px：仍是完整圆，
	// 但不抢桌面。CSS/HTML 的 SVG viewBox 保持 64 坐标系，由 100% 宽高缩放。
	ballSize = 52
	// ballDockSize 是贴边收起后的直径：一半在屏幕外，只露出 18px。
	ballDockSize = 36
	// ballDockThreshold 是拖放结束时离屏幕左右边缘多近就吸附。
	ballDockThreshold = 12
	// ballEdgeMargin 是位置钳制/面板选址时与工作区边界保留的间距。
	ballEdgeMargin = 8
	// ballPanelGap 是悬浮球与状态面板之间的间距。
	ballPanelGap = 12
	// ballPanelCompactScale 是 Windows 弹出层的等比缩放系数：panel.html 在
	// 非 macOS 上给根元素加 platform-compact，CSS zoom 用同一系数（0.86）。
	// 逻辑视口保持 mac 的 392×620 排版，实际窗口按系数折算——字号、内边距、
	// 留白一起收，布局不重排。改这里必须同步改 panel.html 的 zoom 值。
	ballPanelCompactScale = 0.86
	// ballPanelWidth/Height 是悬浮球左键弹出的固定尺寸：加载 panel.html
	//（签到 / 速度 / 宠物三 tab），逻辑版面 392×620，再按 COMPACT_SCALE 折算。
	ballPanelWidth  = 337 // round(392 × 0.86)
	ballPanelHeight = 533 // round(620 × 0.86)
	// petMiniWidth/Height 是左键弹出的迷你宠物窗口：只放伙伴、等级和当日
	// Token，不承载名单/进度/签到。
	petMiniWidth  = 220
	petMiniHeight = 260
	// ballMenuWidth/Height 是右键自绘菜单：三行（打开主界面 / 设置 / 退出）。
	ballMenuWidth  = 196
	ballMenuHeight = 104
	// ballMenuGap 是菜单与悬浮球的间距。
	ballMenuGap = 10
	// ballTopGap 是默认位置距工作区顶边的距离。
	ballTopGap = 16
)

// 贴边方向：四边都可吸附。
const (
	ballDockLeft   = "left"
	ballDockRight  = "right"
	ballDockTop    = "top"
	ballDockBottom = "bottom"
)

// ballDockValid 报告 side 是否是合法贴边方向。
func ballDockValid(side string) bool {
	switch side {
	case ballDockLeft, ballDockRight, ballDockTop, ballDockBottom:
		return true
	}
	return false
}

// ballDefaultOrigin 返回悬浮球的默认位置：主屏工作区顶部居中。
func ballDefaultOrigin(area application.Rect) (int, int) {
	x := area.X + (area.Width-ballSize)/2
	y := area.Y + ballTopGap
	return clampBallPosition(area, x, y, ballSize)
}

// clampBallPosition 把悬浮球窗口整颗限制在工作区内。工作区比球还小时
// 优先保留左上角，避免算出负尺寸位置。
func clampBallPosition(area application.Rect, x, y, size int) (int, int) {
	maxX := area.X + area.Width - size
	if maxX < area.X {
		maxX = area.X
	}
	if x < area.X {
		x = area.X
	}
	if x > maxX {
		x = maxX
	}
	return x, clampBallY(area, y, size)
}

// clampBallY 只钳制纵向，贴边态的 X 允许半出屏，不能复用 clampBallPosition。
func clampBallY(area application.Rect, y, size int) int {
	maxY := area.Y + area.Height - size
	if maxY < area.Y {
		maxY = area.Y
	}
	if y < area.Y {
		return area.Y
	}
	if y > maxY {
		return maxY
	}
	return y
}

// clampBallX 只钳制横向，上下贴边时使用（此时 Y 允许半出屏）。
func clampBallX(area application.Rect, x, size int) int {
	maxX := area.X + area.Width - size
	if maxX < area.X {
		maxX = area.X
	}
	if x < area.X {
		return area.X
	}
	if x > maxX {
		return maxX
	}
	return x
}

// ballDockSideFor 判断拖放结束的位置是否贴边：取四条边里距离最近的一条，
// 且距离必须在阈值内。这样拖到哪条边就贴哪条边——不会因为先判断上下边而
// 「跑到最上面」。按 left → right → bottom → top 的顺序比较，平手时水平边
// 优先（贴顶必须是顶部真的最近）。
func ballDockSideFor(area application.Rect, x, y, size int) string {
	if area.Width <= 0 || area.Height <= 0 {
		return ""
	}
	dist := func(v int) int {
		if v < 0 {
			return 0 // 已经拖出边界，按「贴着这条边」算
		}
		return v
	}
	candidates := []struct {
		side string
		dist int
	}{
		{ballDockLeft, dist(x - area.X)},
		{ballDockRight, dist(area.X + area.Width - (x + size))},
		{ballDockBottom, dist(area.Y + area.Height - (y + size))},
		{ballDockTop, dist(y - area.Y)},
	}
	best := ""
	bestDist := ballDockThreshold + 1
	for _, candidate := range candidates {
		if candidate.dist <= ballDockThreshold && candidate.dist < bestDist {
			best = candidate.side
			bestDist = candidate.dist
		}
	}
	return best
}

// ballDockedPosition 返回贴边态窗口的位置：窗口沿贴边方向一半探出工作区，
// 只露一半；另一个方向钳进工作区。side 非法时钳回工作区左上角。
func ballDockedPosition(area application.Rect, side string, x, y int) (int, int) {
	switch side {
	case ballDockLeft:
		return area.X - ballDockSize/2, clampBallY(area, y, ballDockSize)
	case ballDockRight:
		return area.X + area.Width - ballDockSize/2, clampBallY(area, y, ballDockSize)
	case ballDockTop:
		return clampBallX(area, x, ballDockSize), area.Y - ballDockSize/2
	case ballDockBottom:
		return clampBallX(area, x, ballDockSize), area.Y + area.Height - ballDockSize/2
	default:
		return clampBallPosition(area, x, y, ballDockSize)
	}
}

// ballDrop 描述一次拖放结束后悬浮球该落到哪：Action 为 "dock"（贴边）或
// "float"（浮动态），Size 是落位后的窗口边长。
type ballDrop struct {
	Action   string
	X        int
	Y        int
	Size     int
	Docked   string
	RestoreX int
	RestoreY int
}

// resolveBarDrop 计算拖放结束后的落点：
//   - 已在贴边态：拖到另一条边 → 换边继续贴；拖离边缘 → 恢复浮动态大小；
//   - 未贴边：拖到任一边缘 → 吸附成半圆；否则按工作区钳制。
//
// 纯函数：真实拖放坐标进、真实落点出，便于表驱动单测覆盖全部分支。
func resolveBarDrop(
	area application.Rect, x, y, size int,
	docked string, restoreX, restoreY int,
) ballDrop {
	if docked != "" {
		if side := ballDockSideFor(area, x, y, ballDockSize); side != "" {
			nx, ny := ballDockedPosition(area, side, x, y)
			return ballDrop{
				Action: "dock", X: nx, Y: ny, Size: ballDockSize,
				Docked: side, RestoreX: restoreX, RestoreY: restoreY,
			}
		}
		// 贴边窗口较小，放大回浮动态时把多出来的一圈平均分到两侧，让球心
		// 保持在用户松手的位置。
		delta := (ballSize - ballDockSize) / 2
		nx, ny := clampBallPosition(area, x-delta, y-delta, ballSize)
		return ballDrop{Action: "float", X: nx, Y: ny, Size: ballSize}
	}
	if side := ballDockSideFor(area, x, y, size); side != "" {
		nx, ny := ballDockedPosition(area, side, x, y)
		return ballDrop{
			Action: "dock", X: nx, Y: ny, Size: ballDockSize,
			Docked: side, RestoreX: x, RestoreY: y,
		}
	}
	nx, ny := clampBallPosition(area, x, y, size)
	return ballDrop{Action: "float", X: nx, Y: ny, Size: size}
}

// ballPanelOrigin 把精简面板摆在球旁边：优先右侧，右侧放不下放左侧；
// 两侧都不够时钳进工作区。垂直方向与球顶对齐（略高于球 8px），并限制在
// 工作区内。
func ballPanelOrigin(area application.Rect, ballX, ballY, ballW, panelW, panelH int) (int, int) {
	gap := ballPanelGap
	x := ballX + ballW + gap
	if x+panelW > area.X+area.Width-ballEdgeMargin {
		x = ballX - gap - panelW
	}
	if x < area.X+ballEdgeMargin {
		x = area.X + ballEdgeMargin
	}
	if maxX := area.X + area.Width - panelW - ballEdgeMargin; x > maxX {
		x = maxX
	}
	if x < area.X {
		x = area.X
	}

	y := ballY - 8
	if y < area.Y+ballEdgeMargin {
		y = area.Y + ballEdgeMargin
	}
	if maxY := area.Y + area.Height - panelH - ballEdgeMargin; y > maxY {
		y = maxY
	}
	if y < area.Y {
		y = area.Y
	}
	return x, y
}

// ballMenuOrigin 把右键菜单摆在球的左侧（原型：菜单与面板左右分置，互不
// 遮挡）；左侧放不下时改到右侧。垂直方向与球心对齐并钳进工作区。
func ballMenuOrigin(area application.Rect, ballX, ballY, ballW, ballH, menuW, menuH int) (int, int) {
	x := ballX - ballMenuGap - menuW
	if x < area.X+ballEdgeMargin {
		x = ballX + ballW + ballMenuGap
	}
	if minX := area.X + ballEdgeMargin; x < minX {
		x = minX
	}
	if maxX := area.X + area.Width - menuW - ballEdgeMargin; x > maxX {
		x = maxX
	}
	if x < area.X {
		x = area.X
	}

	y := ballY + ballH/2 - menuH/2
	if y < area.Y+ballEdgeMargin {
		y = area.Y + ballEdgeMargin
	}
	if maxY := area.Y + area.Height - menuH - ballEdgeMargin; y > maxY {
		y = maxY
	}
	if y < area.Y {
		y = area.Y
	}
	return x, y
}

// barWindowState 是悬浮球的位置记忆：坐标、贴边侧、以及贴边前的恢复坐标。
// Version 用来区分坏文件/零值（坐标 0,0 本身是合法位置）。
type barWindowState struct {
	Version  int    `json:"version"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Docked   string `json:"docked,omitempty"`
	RestoreX int    `json:"restore_x,omitempty"`
	RestoreY int    `json:"restore_y,omitempty"`
}

const (
	barStateVersion  = 1
	barStateFileName = "bar-state.json"
)

// barStatePath 返回状态文件路径；applog 未初始化（配置目录未知）时返回空串，
// 调用方据此跳过读写。
func barStatePath() string {
	dir := applog.ConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, barStateFileName)
}

// loadBarState 读取状态文件。文件缺失、坏 JSON、版本不符或贴边侧非法都视为
// 「没有可用状态」，由调用方回落到默认位置。
func loadBarState(path string) (barWindowState, bool) {
	if path == "" {
		return barWindowState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return barWindowState{}, false
	}
	var state barWindowState
	if err := json.Unmarshal(data, &state); err != nil {
		return barWindowState{}, false
	}
	if state.Version != barStateVersion {
		return barWindowState{}, false
	}
	if !ballDockValid(state.Docked) {
		state.Docked = ""
	}
	return state, true
}

// saveBarState 原子写入状态文件；路径为空（未初始化配置目录）时静默跳过。
func saveBarState(path string, state barWindowState) error {
	if path == "" {
		return nil
	}
	state.Version = barStateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.EnsureDir(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, 0o600)
}
