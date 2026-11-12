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

// applicationTrayIcon 返回托盘用的多尺寸 ICO（16/20/24/28/32/48/64，每档
// 都从 1024px 母版高质量预缩放）。Wails 对 PNG 源会把它原样塞进
// CreateIconFromResourceEx，由 GDI 粗暴缩到 SM_CXSMICON——150% DPI 下
// 64px→24px 无平滑缩放，托盘图标像素感明显；ICO 让 shell 直接选中
// 预缩放的精确档位，不再有缩放损失。
func applicationTrayIcon() []byte {
	return brandicon.ApplicationICO()
}

// applyTrayIcon 按平台装托盘图标。macOS 用模板图标，系统会按菜单栏深浅色
// 自动着色；Windows 等平台不认 template 语义，会把单色 glyph 原样画出来，
// 深色任务栏上就是漆黑一片，所以必须换成彩色应用图标。
func applyTrayIcon(tray *application.SystemTray, platform string) {
	applyTrayIconForPet(tray, platform, "")
}

// applyTrayIconForPet 与 applyTrayIcon 相同，但 macOS 上优先使用当前宠物的
// 专属模板字形；没有对应宠物（或非 macOS）时退回原来的行为。
func applyTrayIconForPet(tray *application.SystemTray, platform, petID string) {
	applyTrayIconForPetState(tray, platform, petID, false)
}

// applyTrayIconForPetState 在宠物「已捕获」时把 macOS 菜单栏换成精灵球模板，
// 否则用伙伴剪影；Windows 仍保持彩色应用图标。
func applyTrayIconForPetState(tray *application.SystemTray, platform, petID string, caught bool) {
	icon, template := trayIconForPetState(platform, petID, caught)
	if template {
		tray.SetTemplateIcon(icon)
		return
	}
	tray.SetIcon(icon)
}

// trayIconForPetState 返回平台/伙伴/捕获状态对应的托盘图标。
func trayIconForPetState(platform, petID string, caught bool) (icon []byte, template bool) {
	if platform == "darwin" {
		if caught {
			return brandicon.BallTemplate(), true
		}
		return trayIconForPet(platform, petID)
	}
	return applicationTrayIcon(), false
}

// trayIconForPet 返回平台与宠物对应的托盘图标。macOS：宠物模板 > Q 模板；
// 其它平台保持彩色应用图标。
func trayIconForPet(platform, petID string) (icon []byte, template bool) {
	if platform == "darwin" {
		if petIcon, ok := brandicon.PetTemplate(petID); ok {
			return petIcon, true
		}
		return templateIcon(), true
	}
	return applicationTrayIcon(), false
}

// trayIcon 返回平台对应的托盘图标，以及它是需要系统着色的模板图标（仅 macOS）。
// Windows 侧是多尺寸 ICO：shell 按 SM_CXSMICON 选中预缩放的精确档位。
func trayIcon(platform string) (icon []byte, template bool) {
	if platform == "darwin" {
		return templateIcon(), true
	}
	return applicationTrayIcon(), false
}
