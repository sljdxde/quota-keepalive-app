//go:build !windows

package gui

import (
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// browseInstallDir 在非 Windows 平台不可用：安装向导只随 Windows 单文件发行物
// 出现，这里保留同一签名让跨平台构建与单测可用。
func browseInstallDir(_ *application.App, _ *application.WebviewWindow, _ string) (string, error) {
	return "", errors.New("当前平台不支持目录选择，请直接输入安装路径")
}
