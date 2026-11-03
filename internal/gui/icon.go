package gui

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/brandicon"
)

// goos 是当前运行平台；测试替换它来覆盖托盘图标的分支。
var goos = runtime.GOOS

// templateIcon 返回单色 Q 模板图标，系统在深浅色菜单栏自动按 alpha 着色。
func templateIcon() []byte {
	return brandicon.Template()
}

// applicationIcon 返回深青底、象牙白 Q 环与暖金尾笔的应用图标，供 Wails
// 写入 Dock/任务栏窗口图标；.app 与 Windows PE 的安装图标仍由发布脚本生成。
func applicationIcon(size int) []byte {
	return brandicon.Application(size)
}

// applyTrayIcon 按平台装托盘图标。macOS 用模板图标，系统会按菜单栏深浅色
// 自动着色；Windows 等平台不认 template 语义，会把单色 glyph 原样画出来，
// 深色任务栏上就是漆黑一片，所以必须换成彩色应用图标。
func applyTrayIcon(tray *application.SystemTray, platform string) {
	icon, template := trayIcon(platform)
	if template {
		tray.SetTemplateIcon(icon)
		return
	}
	tray.SetIcon(icon)
}

// trayIcon 返回平台对应的托盘图标，以及它是需要系统着色的模板图标（仅 macOS）。
// Windows 的小图标会由 Wails 缩放到系统 SM_CXSMICON，给 64px 源图留足高 DPI 余量。
func trayIcon(platform string) (icon []byte, template bool) {
	if platform == "darwin" {
		return templateIcon(), true
	}
	return applicationIcon(64), false
}
