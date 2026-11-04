package gui

// 主窗口与吸附面板的目标尺寸（DIP）。这些是「理想尺寸」：实际创建时还要按
// 屏幕工作区收敛（见 fitWindowSize），否则在 125%/150% 缩放的笔记本或小屏上，
// 窗口会超出工作区——标题栏的最小化/最大化/关闭按钮被挤到屏幕外，用户既关不
// 掉也缩不小，只能看到界面被裁掉一截。
const (
	// 主窗口默认尺寸：刻意小于工作区（约 70%），不铺满屏幕。窗口小，内容区
	// 自然收窄，卡片不会被拉成又长又扁的一条；四周也留出桌面呼吸感。
	// 小屏/高缩放时由 fitWindowSize 再往下收敛。
	mainIdealWidth   = 920
	mainIdealHeight  = 640
	mainMinWidth     = 700
	mainMinHeight    = 520
	panelIdealWidth  = 392
	panelIdealHeight = 620
	panelMinWidth    = 392
	panelMinHeight   = 620
	// panelMinFittedHeight 是吸附面板在高度受限时允许收缩到的最小高度
	// （Windows 上通过 applyPanelChrome 放宽 MinHeight）：小屏下 620 会顶到
	// 屏幕底边之外，底部内容被裁掉。
	panelMinFittedHeight = 360

	// windowFitMargin 是窗口与工作区边缘之间保留的余量：既给任务栏/标题栏
	// 留视觉呼吸，也避免正好等于工作区时被系统判成「最大化」而隐藏边框。
	windowFitMargin = 20
)

// windowSize 是一组窗口尺寸约束（DIP）。
type windowSize struct {
	Width  int
	Height int
	// MinWidth/MinHeight 是用户能拖到的最小尺寸。
	MinWidth  int
	MinHeight int
}

// mainWindowSize 是主窗口的理想尺寸。
func mainWindowSize() windowSize {
	return windowSize{
		Width:     mainIdealWidth,
		Height:    mainIdealHeight,
		MinWidth:  mainMinWidth,
		MinHeight: mainMinHeight,
	}
}

// fitWindowSize 把理想尺寸收敛到屏幕工作区内（全部为 DIP）。
//
// 规则：
//   - 工作区不可得（<=0）时原样返回，不因探测失败而缩小窗口；
//   - 宽高不超过「工作区 - 2*margin」，余量不足时退回工作区本身；
//   - 最小尺寸同步下调到不超过收敛后的尺寸：否则小屏上用户既看不到标题栏
//     按钮，也没法把窗口拖进可见范围（OS 会拒绝小于 MinWidth 的尺寸）。
func fitWindowSize(desired windowSize, workWidth, workHeight, margin int) windowSize {
	if workWidth <= 0 || workHeight <= 0 {
		return desired
	}
	if margin < 0 {
		margin = 0
	}
	maxWidth := workWidth - 2*margin
	maxHeight := workHeight - 2*margin
	if maxWidth <= 0 {
		maxWidth = workWidth
	}
	if maxHeight <= 0 {
		maxHeight = workHeight
	}

	fitted := desired
	fitted.Width = minInt(desired.Width, maxWidth)
	fitted.Height = minInt(desired.Height, maxHeight)
	fitted.MinWidth = minInt(desired.MinWidth, fitted.Width)
	fitted.MinHeight = minInt(desired.MinHeight, fitted.Height)
	if fitted.MinWidth <= 0 {
		fitted.MinWidth = fitted.Width
	}
	if fitted.MinHeight <= 0 {
		fitted.MinHeight = fitted.Height
	}
	return fitted
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// centeredOrigin 计算把 size 居中的窗口放在工作区内的左上角坐标（DIP）。
// 工作区不可得时返回 (0,0)，交给平台默认摆放。
func centeredOrigin(width, height, workX, workY, workWidth, workHeight int) (int, int) {
	if workWidth <= 0 || workHeight <= 0 {
		return 0, 0
	}
	x := workX + (workWidth-width)/2
	y := workY + (workHeight-height)/2
	if x < workX {
		x = workX
	}
	if y < workY {
		y = workY
	}
	return x, y
}

// fitPanelHeight 返回吸附面板在给定顶部位置下可用的高度（DIP）：
// 顶到工作区底边之前的最大值，不低于 panelMinFittedHeight。
// workBottom 是工作区底边坐标；不可得（<=0）时返回理想高度。
func fitPanelHeight(top, workBottom, ideal int) int {
	if workBottom <= 0 || top < 0 {
		return ideal
	}
	available := workBottom - windowFitMargin - top
	if available >= ideal {
		return ideal
	}
	if available < panelMinFittedHeight {
		return panelMinFittedHeight
	}
	return available
}
