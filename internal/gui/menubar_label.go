package gui

import (
	"fmt"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/settings"
	"qka/internal/tokenspeed"
)

// 菜单栏速度标签的三种样式。macOS 的 NSStatusItem 只能显示纯文本或模板图，
// 所以「数字滚动」在菜单栏不可行；能改的是文案与分段颜色。
//
// 颜色靠 Wails 的 SystemTrayLabelParser 机制实现：label 里出现 ANSI 序列时，
// darwin 后端会把 label 交给这个解析函数拆成带颜色的分段。默认实现只是剥掉
// 转义码，因此必须在启动时替换成 parseMenuBarLabel，颜色才会真正生效。

// menuBarActiveColor 是「有速度」时数字的颜色（深浅色菜单栏都能看清的绿）。
const menuBarActiveColor = "#5fd394"

// ansiSequence 是自用的最小 ANSI 形式：ESC [ <hex 颜色> m。
// 只用这一种形式，解析器不需要完整 ANSI 状态机。
const ansiSequence = "\033["

// menuBarLabel 按样式与速度返回菜单栏文案。
//
//   - compact：只显示数字（最省横向空间，单位靠 tooltip 补）
//   - tinted：数字 + tok/s，有速度时数字染绿
//   - label（含未知值）：数字 + tok/s 的纯文本，即现有行为
func menuBarLabel(style string, speed float64) string {
	digits := tokenspeed.FormatSpeed(speed)
	switch style {
	case settings.MenuBarSpeedCompact:
		return digits
	case settings.MenuBarSpeedTinted:
		if speed <= 0 {
			return digits + " tok/s"
		}
		return ansiSequence + menuBarActiveColor + "m" + digits + ansiSequence + "m" + " tok/s"
	default:
		return digits + " tok/s"
	}
}

// visibleMenuBarLabel 返回标签里用户能看到的文字（去掉 ANSI 颜色序列）。
// 菜单栏补空白时只能按可见文字量宽，否则颜色序列会被当成字符算进去。
func visibleMenuBarLabel(label string) string {
	parts, err := parseMenuBarLabel(label)
	if err != nil {
		return label
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(part.Text)
	}
	return builder.String()
}

// menuBarTooltip 是菜单栏图标的悬停说明。compact 样式不显示单位，
// 靠这里把完整含义补回来。
func menuBarTooltip(style string, speed float64) string {
	digits := tokenspeed.FormatSpeed(speed)
	if style == settings.MenuBarSpeedCompact {
		return fmt.Sprintf("Quota Keepalive · %s tok/s", digits)
	}
	return "Quota Keepalive"
}

// parseMenuBarLabel 把 label 拆成 Wails 需要的带色分段。
// 支持的形式只有 ESC [ <#rrggbb|空> m，其余字符原样保留。
func parseMenuBarLabel(label string) ([]application.SystemTrayLabelPart, error) {
	if !strings.Contains(label, ansiSequence) {
		return []application.SystemTrayLabelPart{{Text: label}}, nil
	}
	parts := make([]application.SystemTrayLabelPart, 0, 4)
	current := application.SystemTrayLabelPart{}
	for len(label) > 0 {
		start := strings.Index(label, ansiSequence)
		if start < 0 {
			current.Text += label
			break
		}
		if start > 0 {
			current.Text += label[:start]
		}
		rest := label[start+len(ansiSequence):]
		end := strings.IndexByte(rest, 'm')
		if end < 0 {
			// 未闭合的序列按字面量处理，不要吞掉用户能看到的字符。
			current.Text += label[start:]
			break
		}
		if current.Text != "" {
			parts = append(parts, current)
		}
		current = application.SystemTrayLabelPart{FgColor: rest[:end]}
		label = rest[end+1:]
	}
	if current.Text != "" {
		parts = append(parts, current)
	}
	if len(parts) == 0 {
		return []application.SystemTrayLabelPart{{Text: ""}}, nil
	}
	return parts, nil
}

// installMenuBarLabelParser 把默认的「剥掉转义码」换成能上色的解析器。
// 只在 macOS 上有意义；其它平台的 SetLabel 只用来更新 tooltip。
func installMenuBarLabelParser() {
	application.SystemTrayLabelParser = parseMenuBarLabel
}
