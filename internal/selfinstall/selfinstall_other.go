//go:build !windows

package selfinstall

import "qka/internal/applog"

// Ensure 在非 Windows 平台什么都不做：这些平台没有「开始菜单快捷方式 +
// App Paths」这套自安装语义，安装与卸载另有渠道（macOS 是 DMG/skill）。
// 保留同一签名，调用方无需按平台分开接线。
func Ensure(_, _, _ string) error {
	applog.Debugf("selfinstall", "非 Windows 平台没有开始菜单，跳过自安装")
	return nil
}
