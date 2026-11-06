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
	// ballSize 是浮动态直径，与原型 .ball 一致。
	ballSize = 64
	// ballDockSize 是贴边收起后的直径：一半在屏幕外，只露出 22px。
	ballDockSize = 44
	// ballDockThreshold 是拖放结束时离屏幕左右边缘多近就吸附。
	ballDockThreshold = 12
	// ballEdgeMargin 是位置钳制/面板选址时与工作区边界保留的间距。
	ballEdgeMargin = 8
	// ballPanelGap 是悬浮球与状态面板之间的间距。
	ballPanelGap = 12
	// ballPanelWidth/Height 是精简状态面板的固定尺寸（原型 244px 卡片；
	// 高度取 182，让 32px 数字 + 四行内容 + 底部按钮刚好不触底）。
	ballPanelWidth  = 244
	ballPanelHeight = 182
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

// ballDockSideFor 判断拖放结束的位置是否贴边：离任一边缘 ballDockThreshold
// 以内就吸附到那一边（角落时按先命中的边返回）。否则返回空串。
func ballDockSideFor(area application.Rect, x, y, size int) string {
	if area.Width <= 0 || area.Height <= 0 {
		return ""
	}
	if y-area.Y <= ballDockThreshold {
		return ballDockTop
	}
	if area.Y+area.Height-(y+size) <= ballDockThreshold {
		return ballDockBottom
	}
	if x-area.X <= ballDockThreshold {
		return ballDockLeft
	}
	if area.X+area.Width-(x+size) <= ballDockThreshold {
		return ballDockRight
	}
	return ""
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
