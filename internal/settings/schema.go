// Package settings 提供声明式应用设置：由配置 schema 生成表单，
// 原子写入、备份、schema 校验，面板与 MCP 呈现同一状态。
package settings

import (
	"strconv"
	"time"
)

// FieldType 是设置项类型。
type FieldType string

const (
	// FieldBool 是布尔开关。
	FieldBool FieldType = "bool"
	// FieldSelect 是有限选项。
	FieldSelect FieldType = "select"
	// FieldText 是自由文本（单行）。用于地址、路径一类需要手填的值。
	FieldText FieldType = "text"
)

// Option 是 select 设置项的可选值。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field 是一个声明式设置项。新增设置项只需在 Schema 中追加一条，
// 无需手写表单界面。
type Field struct {
	Key         string    `json:"key"`
	Type        FieldType `json:"type"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	// Default 是默认值。bool 项为 bool，select 项为选项值。
	Default any      `json:"default"`
	Options []Option `json:"options,omitempty"`
	// Platform 限定该设置项只在指定平台展示（"" 表示全平台）。取值同 runtime.GOOS：
	// "windows" / "darwin" / "linux"。设置值本身仍全平台读写，只是表单不展示。
	Platform string `json:"platform,omitempty"`
	// Hidden 表示不进入通用设置表单，由专门界面（如软件更新卡片）呈现。
	// 设置值仍参与 schema 校验与读写。
	Hidden bool `json:"hidden,omitempty"`
}

// DefaultBool 返回布尔项的默认值；非布尔项返回 false。
func (f Field) DefaultBool() bool {
	value, _ := f.Default.(bool)
	return value
}

// DefaultString 返回 select 项的默认值；非 select 项返回空字符串。
func (f Field) DefaultString() string {
	value, _ := f.Default.(string)
	return value
}

// Schema 是应用设置的声明式 schema。
var Schema = []Field{
	{
		Key:         "theme",
		Type:        FieldSelect,
		Label:       "外观",
		Description: "跟随系统，或固定使用浅色、深色外观。",
		Default:     "system",
		Options: []Option{
			{Value: "system", Label: "跟随系统"},
			{Value: "light", Label: "浅色"},
			{Value: "dark", Label: "深色"},
		},
	},
	{
		Key:         "title_theme",
		Type:        FieldSelect,
		Label:       "称号主题",
		Description: "Token 速度页与菜单栏面板使用；默认交通工具。",
		Default:     "vehicles",
		Options: []Option{
			{Value: "vehicles", Label: "交通工具"},
			{Value: "animals", Label: "动物速度"},
			{Value: "athletics", Label: "田径赛场"},
			{Value: "music", Label: "音乐速度"},
			{Value: "network", Label: "网络世代"},
			{Value: "sound", Label: "音速时代"},
			{Value: "student", Label: "学生生涯"},
			{Value: "career", Label: "职场晋升"},
			{Value: "space", Label: "宇宙速度"},
		},
	},
	{
		Key:         "background",
		Type:        FieldSelect,
		Label:       "背景",
		Description: "选择内置桌面背景；自定义图片保存在本机。",
		Default:     "mist",
		Options: []Option{
			{Value: "mist", Label: "薄雾"},
			{Value: "coast", Label: "海岸"},
			{Value: "ember", Label: "余烬"},
			{Value: "glacier", Label: "冰川"},
			{Value: "night", Label: "夜空"},
			{Value: "custom", Label: "自定义图片"},
		},
	},
	{
		Key:         "glass",
		Type:        FieldSelect,
		Label:       "毛玻璃",
		Description: "控制窗口与卡片的透明度和背景模糊强度。",
		Default:     "standard",
		Options: []Option{
			{Value: "clear", Label: "通透"},
			{Value: "standard", Label: "标准"},
			{Value: "strong", Label: "磨砂"},
		},
	},
	{
		Key:         "performance_mode",
		Type:        FieldSelect,
		Label:       "性能模式",
		Description: "默认保留全部毛玻璃与 3D 特效（与 macOS 一致）。在无显卡加速或虚拟机里觉得卡时，再切到「自动」或「低配优先」。",
		Default:     "off",
		Options: []Option{
			{Value: "auto", Label: "自动"},
			{Value: "on", Label: "低配优先"},
			{Value: "off", Label: "关闭"},
		},
	},
	{
		Key:         "ui_scale",
		Type:        FieldSelect,
		Label:       "界面缩放",
		Description: "「自动」按窗口可用高度收缩整个界面，适合 150% 缩放的笔记本（内容区常常只有 600 像素高，字显得大、内容要被裁掉）。觉得字太小或想更紧凑可手动固定。",
		Default:     "auto",
		Options: []Option{
			{Value: "auto", Label: "自动（推荐）"},
			{Value: "1", Label: "100%"},
			{Value: "0.9", Label: "90%"},
			{Value: "0.85", Label: "85%"},
			{Value: "0.8", Label: "80%"},
		},
	},
	{
		Key:         "token_speed_poll",
		Type:        FieldSelect,
		Label:       "速度采样频率",
		Description: "本机 Agent 日志的扫描间隔。越快数字越跟手，但磁盘与 CPU 开销也越大；省电或低配机器建议放慢。",
		Default:     "500",
		Options: []Option{
			{Value: "200", Label: "灵敏（200ms）"},
			{Value: "300", Label: "较快（300ms）"},
			{Value: "500", Label: "平衡（500ms）"},
			{Value: "750", Label: "省电（750ms）"},
		},
	},
	{
		Key:         "token_speed_display",
		Type:        FieldSelect,
		Label:       "速度数字显示",
		Description: "数字变化时的动效：逐位滚轮最像仪表，平滑缓动最安静，直接替换最省事。",
		Default:     "roll",
		Options: []Option{
			{Value: "roll", Label: "逐位滚轮"},
			{Value: "fade", Label: "平滑缓动"},
			{Value: "direct", Label: "直接替换"},
		},
	},
	{
		Key:         "token_speed_menubar",
		Type:        FieldSelect,
		Label:       "菜单栏速度样式",
		Description: "菜单栏那一格怎么显示 tok/s。分段彩色会在有速度时把数字染成绿色；只显示数字最省横向空间。",
		Default:     "label",
		Platform:    "darwin",
		Options: []Option{
			{Value: "label", Label: "纯文本"},
			{Value: "tinted", Label: "分段彩色"},
			{Value: "compact", Label: "只显示数字"},
		},
	},
	{
		Key:         "show_dock_icon",
		Type:        FieldBool,
		Label:       "在 Dock 中显示图标",
		Description: "主窗口打开时显示 Quota Keepalive 图标；关闭主窗口后自动隐藏并仅驻留菜单栏（Windows 任务栏始终显示）。",
		Default:     true,
	},
	{
		Key:         "show_ball",
		Type:        FieldBool,
		Label:       "显示悬浮球",
		Description: "屏幕边缘的常驻速度球（仅 Windows）；不需要时可关掉，状态面板与托盘不受影响。",
		Default:     true,
		Platform:    "windows",
	},
	{
		Key:         "launch_at_login",
		Type:        FieldBool,
		Label:       "开机自动启动",
		Description: "登录后自动启动 Quota Keepalive。",
		Default:     false,
	},
	{
		Key:         "enable_notifications",
		Type:        FieldBool,
		Label:       "系统通知",
		Description: "执行完成后发送系统通知；默认关闭，不打扰。",
		Default:     false,
	},
	{
		Key:         "update_domestic_mirror",
		Type:        FieldBool,
		Label:       "国内镜像源",
		Description: "默认开启：更新检查与安装包下载走 ghfast.top 代理；版本与 SHA-256 仍按官方 feed 校验。",
		Default:     true,
		Hidden:      true,
	},
}

// FieldsForPlatform 返回指定平台上应展示的设置项：Platform 为空表示全平台。
// 取值与 runtime.GOOS 一致（windows / darwin / linux）。设置值本身仍全平台读写，
// 这里只决定表单展示，避免 macOS 上出现「显示悬浮球」这类无效开关。
func FieldsForPlatform(goos string) []Field {
	fields := make([]Field, 0, len(Schema))
	for _, field := range Schema {
		if field.Platform == "" || field.Platform == goos {
			fields = append(fields, field)
		}
	}
	return fields
}

// Token 速度相关设置的 key 与可选值。前端与后端都从这里取，避免字符串散落。
const (
	// KeyTokenSpeedPoll 是本机 Agent 日志的扫描间隔（毫秒，字符串形式）。
	KeyTokenSpeedPoll = "token_speed_poll"
	// KeyTokenSpeedDisplay 是速度数字的显示方式。
	KeyTokenSpeedDisplay = "token_speed_display"
	// KeyTokenSpeedMenuBar 是 macOS 菜单栏速度样式。
	KeyTokenSpeedMenuBar = "token_speed_menubar"
)

// 菜单栏速度样式。
const (
	// MenuBarSpeedLabel 是纯文本 label（含 tok/s）。
	MenuBarSpeedLabel = "label"
	// MenuBarSpeedTinted 是分段彩色（有速度时数字染色）。
	MenuBarSpeedTinted = "tinted"
	// MenuBarSpeedCompact 是只显示数字。
	MenuBarSpeedCompact = "compact"
)

// SpeedPollInterval 返回速度采样间隔；值非法时回退到 500ms。
// 允许范围 100ms-5s：更快没有实际收益（数据源本身按响应落盘），
// 更慢会让界面明显滞后。
func (s *Settings) SpeedPollInterval() time.Duration {
	millis, err := strconv.Atoi(s.GetString(KeyTokenSpeedPoll))
	if err != nil || millis < 100 || millis > 5000 {
		millis = 500
	}
	return time.Duration(millis) * time.Millisecond
}

// MenuBarSpeedStyle 返回菜单栏速度样式；未知值回退到纯文本。
func (s *Settings) MenuBarSpeedStyle() string {
	return normalizedChoice(s.GetString(KeyTokenSpeedMenuBar), []string{
		MenuBarSpeedLabel, MenuBarSpeedTinted, MenuBarSpeedCompact,
	}, MenuBarSpeedLabel)
}

// normalizedChoice 返回 allowed 中的 value，不在其中时返回 fallback。
func normalizedChoice(value string, allowed []string, fallback string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return fallback
}

// IsTokenSpeedKey 报告 key 是否属于「速度显示」相关设置。前端与 GUI 用它在
// 保存后触发即时刷新，避免为一堆无关设置项也广播速度事件。
func IsTokenSpeedKey(key string) bool {
	switch key {
	case KeyTokenSpeedPoll, KeyTokenSpeedDisplay, KeyTokenSpeedMenuBar:
		return true
	default:
		return false
	}
}
