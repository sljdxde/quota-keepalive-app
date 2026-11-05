//go:build windows

package gui

import (
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// installPlatformWindowBehavior 处理 Windows 与 macOS 的差异：macOS 有 Dock
// 图标可以再次点开，Windows 没有，双击 exe（无参数 tray 形态）如果只进托盘、
// 不显示主窗口，用户会以为程序没启动成功。所以在事件循环起来后主动弹出主
// 窗口；用 `QKA.exe tray` 显式启动（登录自启也走这一形态）时仍然安静驻留。
func (a *App) installPlatformWindowBehavior() {
	// 常驻悬浮球在任何启动形态下都要创建（等价于 macOS 的菜单栏图标），但只在
	// 主窗口不可见时显示：它是独立置顶窗口，浮在主窗口上会挡内容。
	showMainOnStart := len(os.Args) <= 1
	a.wails.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// 必须异步：ApplicationStarted 是在窗口消息处理器里回调的，直接在里面
		// 创建/显示窗口会对同一个线程同步发窗口消息（Show/Focus 内部走
		// InvokeSync + SendMessage），造成经典的自锁——界面停在加载态并显示
		// 「未响应」。放到独立 goroutine 后，消息循环已经空闲，不会互等。
		go func() {
			// 先创建悬浮球窗口（只创建不显示）：面板要以它的位置为锚点，关闭
			// 主窗口时也要能立刻显示回来。
			a.ensureBar()
			// 双击启动（无参数）时直接把主窗口打开；显式 `QKA.exe tray`
			// （登录自启走这一形态）只驻留托盘，不打扰用户。
			if showMainOnStart {
				// 主窗口可见 → 悬浮球收起（置顶球会挡住主窗口内容）。
				a.showMain()
				return
			}
			a.showBar()
		}()
	})
}
