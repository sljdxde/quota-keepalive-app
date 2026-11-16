//go:build !darwin || ios

package gui

import (
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// applyMenuBarLayout 只在 macOS 上需要：其它平台的托盘标签宽度由系统自己
// 决定（Windows 托盘本来就不显示 label 文本），这里原样返回标签。
func applyMenuBarLayout(_ *application.SystemTray, label, _ string, _ time.Time) string {
	return label
}
