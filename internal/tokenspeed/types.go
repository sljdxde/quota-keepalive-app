// Package tokenspeed measures local coding-agent output throughput from append-only
// transcript files. It never exposes prompts, responses, credentials, or raw headers.
package tokenspeed

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// liveWindow 是「最近一次已完成响应」的实测速率在仪表上保留多久。
	// 3 秒过于苛刻：一次回答刚结束、用户抬头看表就已经归零，界面上表现为
	// 「明明刚跑完却没有速度」。UI 文案本来就写着「显示最近一次可靠响应速度」，
	// 因此保留 15 秒，既不会把陈旧数据当实时，也不会一闪就没。
	liveWindow       = 15 * time.Second
	recentReference  = 10 * time.Minute
	recentCount      = 3
	fallback         = 30 * time.Minute
	fallbackCount    = 10
	staleAfter       = 5 * time.Minute
	maxRecordAge     = 35 * time.Minute
	maxReadChunk     = 16 << 20
	maxTranscriptAge = 45 * time.Minute
	maxTokenValue    = int64(1_000_000_000_000)
	deduperSize      = 65536
	sessionVisible   = 10 * time.Minute
	maxSessions      = 12
)

// 初始读取只保留会话文件尾部，避免启动时解析超大历史；尾部之前的历史字节
// 由后台分片补读，用于还原当日 token 总量。变量形式便于测试注入小阈值。
var (
	// maxInitialTail 是首次打开会话文件时实时读取的最大尾部字节数。
	maxInitialTail = int64(8 << 20)
	// backfillChunk 是每次 Poll 为单个文件补读的最大历史字节数。
	backfillChunk = int64(4 << 20)
	// backfillBudget 是每次 Poll 全部文件补读的字节预算。
	backfillBudget = int64(16 << 20)
)

// Root is one transcript root monitored by the service.
type Root struct {
	Source string
	Path   string
}

// Config controls local transcript discovery and deterministic test clocks.
type Config struct {
	Roots        []Root
	PollInterval time.Duration
	// IdlePollInterval 是「没有会话在生成」时的轮询间隔。空闲时无需 750ms 那么
	// 频繁：整棵目录树扫描在 Windows（NTFS / 虚拟机）上比 APFS 贵得多，放宽
	// 到 2s 能显著降低空闲 CPU，又不会让新会话出现得太迟。为 0 时沿用它
	// PollInterval（测试默认）。
	IdlePollInterval time.Duration
	// WalkInterval 是完整重启目录扫描的最小间隔；两次扫描之间只对已知文件做
	// 增量 stat/读取。为 0 时每轮都重扫（测试默认，保证新建文件立刻可见）。
	WalkInterval time.Duration
	// Redline 是红线速率（tok/s）：热谱、档位与称号阈值按它缩放。为 0 时用
	// DefaultRedline（与 agent-speed 的默认红线 2000 一致）。
	Redline float64
	Now     func() time.Time
}

// DefaultRedline 是红线默认值。agent-speed 默认 2000 tok/s，但那是给「单会话
// 峰值上千」的机器标定的；QKA 实测多数会话落在 10–60 tok/s，2000 会让仪表
// 长期只有 1–5% 热度（球几乎不亮、称号不变色）。400 是原来的口径，500 在
// 它之上留了一点余量：30 tok/s 约 25% 热度、200 tok/s 就过半环。
const DefaultRedline = 500.0

// Built-in speed-title themes used by the QKA title icon library.
const (
	TitleThemeVehicles  = "vehicles"
	TitleThemeAnimals   = "animals"
	TitleThemeAthletics = "athletics"
	TitleThemeMusic     = "music"
	TitleThemeNetwork   = "network"
	TitleThemeSound     = "sound"
	TitleThemeStudent   = "student"
	TitleThemeCareer    = "career"
	TitleThemeSpace     = "space"
)

// TitleThemeOption is a selectable built-in title theme.
type TitleThemeOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// TitleThemeDefinition is the complete, credential-free catalog consumed by
// the title picker. It keeps display rules out of the frontend.
type TitleThemeDefinition struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Idle       string    `json:"idle"`
	Thresholds []float64 `json:"thresholds"`
	Titles     []string  `json:"titles"`
}

var titleThemes = map[string]TitleThemeOption{
	TitleThemeVehicles:  {ID: TitleThemeVehicles, Label: "交通工具"},
	TitleThemeAnimals:   {ID: TitleThemeAnimals, Label: "动物速度"},
	TitleThemeAthletics: {ID: TitleThemeAthletics, Label: "田径赛场"},
	TitleThemeMusic:     {ID: TitleThemeMusic, Label: "音乐速度"},
	TitleThemeNetwork:   {ID: TitleThemeNetwork, Label: "网络世代"},
	TitleThemeSound:     {ID: TitleThemeSound, Label: "音速时代"},
	TitleThemeStudent:   {ID: TitleThemeStudent, Label: "学生生涯"},
	TitleThemeCareer:    {ID: TitleThemeCareer, Label: "职场晋升"},
	TitleThemeSpace:     {ID: TitleThemeSpace, Label: "宇宙速度"},
}

// TitleThemeOptions returns the picker order used by the UI.
func TitleThemeOptions() []TitleThemeOption {
	return []TitleThemeOption{
		titleThemes[TitleThemeVehicles],
		titleThemes[TitleThemeAnimals],
		titleThemes[TitleThemeAthletics],
		titleThemes[TitleThemeMusic],
		titleThemes[TitleThemeNetwork],
		titleThemes[TitleThemeSound],
		titleThemes[TitleThemeStudent],
		titleThemes[TitleThemeCareer],
		titleThemes[TitleThemeSpace],
	}
}

// TitleThemeCatalog returns every built-in theme with its full title ladder.
func TitleThemeCatalog() []TitleThemeDefinition {
	return titleCatalogFor(0)
}

// titleCatalogFor 返回按红线缩放的称号目录：界面直接用阈值画档位刻度。
func titleCatalogFor(redline float64) []TitleThemeDefinition {
	thresholds := TitleThresholdsFor(redline)
	options := TitleThemeOptions()
	out := make([]TitleThemeDefinition, 0, len(options))
	for _, option := range options {
		spec := builtInTitles[option.ID]
		out = append(out, TitleThemeDefinition{
			ID:         option.ID,
			Label:      option.Label,
			Idle:       spec.idle,
			Thresholds: append([]float64(nil), thresholds[:]...),
			Titles:     append([]string(nil), spec.titles[:]...),
		})
	}
	return out
}

// Snapshot is the credential-free live speed view exposed to the UI.
type Snapshot struct {
	OutputTokensPerSecond float64                `json:"output_tokens_per_second"`
	InputTokensPerSecond  float64                `json:"input_tokens_per_second"`
	TotalTokensPerSecond  float64                `json:"total_tokens_per_second"`
	TodayTotalTokens      int64                  `json:"today_total_tokens"`
	TodayTokenDate        string                 `json:"today_token_date"`
	Active                bool                   `json:"active"`
	GeneratingAgents      int                    `json:"generating_agents"`
	Agent                 string                 `json:"agent,omitempty"`
	Title                 string                 `json:"title"`
	TitleTheme            string                 `json:"title_theme"`
	TitleThemeID          string                 `json:"title_theme_id"`
	TitleCatalog          []TitleThemeDefinition `json:"title_catalog"`
	Sources               []SourceSnapshot       `json:"sources"`
	Sessions              []SessionSnapshot      `json:"sessions"`
	// IdleSourceCount 是「已配置但今天完全没用过」的数据源数量；界面只用它
	// 提示「另有 N 个未使用」，不再为它们各占一行空白卡片。
	IdleSourceCount int    `json:"idle_source_count"`
	UpdatedAt       string `json:"updated_at"`

	// PeakTodayTokensPerSecond 是今日实时速率的峰值（agent-speed 口径：运行
	// 期间观测到的最大值），供速度页的「今日峰值」标记与破纪录庆祝使用。
	PeakTodayTokensPerSecond float64 `json:"peak_today_tokens_per_second"`
	// EstimatedShare 是当前速率里来自「在飞估算」的比例 0-1；界面据此提示
	// 数字中有多少是估算（真实数据到齐后会自动被实测替换）。
	EstimatedShare float64 `json:"estimated_share"`
	// ResponseSeq 每完成一条响应就递增；界面比对序号变化来播放「响应完成」
	// 冲击波（轮询快照没有事件通道，用序号充当轻量事件）。
	ResponseSeq int64 `json:"response_seq"`
	// PeakSeq 每庆祝一次新峰值就递增（已按冷却去抖）。
	PeakSeq int64 `json:"peak_seq"`
	// Redline 是当前红线速率（tok/s）：热谱、档位与称号阈值都按它缩放。
	Redline float64 `json:"redline_tokens_per_second"`
	// 今日汇总（速度页「今日」网格）：输出/输入 token、请求数与平均实测速度。
	TodayOutputTokens  int64   `json:"today_output_tokens"`
	TodayInputTokens   int64   `json:"today_input_tokens"`
	TodayRequests      int64   `json:"today_requests"`
	TodayAverageTokens float64 `json:"today_average_tokens_per_second"`
}

// SourceSnapshot is one agent family's contribution to the live window.
type SourceSnapshot struct {
	Source                string  `json:"source"`
	Label                 string  `json:"label"`
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
	InputTokensPerSecond  float64 `json:"input_tokens_per_second"`
	TotalTokensPerSecond  float64 `json:"total_tokens_per_second"`
	GeneratingAgents      int     `json:"generating_agents"`
	TodayInputTokens      int64   `json:"today_input_tokens"`
	TodayOutputTokens     int64   `json:"today_output_tokens"`
	TodayTotalTokens      int64   `json:"today_total_tokens"`
}

// SessionSnapshot is one local agent transcript's safe, credential-free view.
// The opaque ID is derived from the local file identity and never contains a path.
type SessionSnapshot struct {
	ID string `json:"id"`
	// Label 是给人看的会话名（项目/工作区）。同一次会话可能横跨多个项目，
	// 因此 Label 只从路径或会话元数据推导，绝不使用提示词内容。
	Label string `json:"label"`
	// Project 是推导出的项目名；为空时界面回退到模型名或短 ID。
	Project               string  `json:"project,omitempty"`
	Source                string  `json:"source"`
	SourceLabel           string  `json:"source_label"`
	Model                 string  `json:"model,omitempty"`
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
	InputTokensPerSecond  float64 `json:"input_tokens_per_second"`
	TotalTokensPerSecond  float64 `json:"total_tokens_per_second"`
	Generating            bool    `json:"generating"`
	LastActiveAt          string  `json:"last_active_at,omitempty"`
}

// DefaultConfig discovers local coding-agent transcript roots.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	roots := make([]Root, 0, 8)
	seen := make(map[string]struct{})
	// 调用方只会传入非空 Join 结果；唯一需要防御的是同一路径被两个来源
	// 注册（例如 QODER_CONFIG_DIR 与 QODERCN_CONFIG_DIR 指向同一目录）。
	add := func(source, path string) {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		roots = append(roots, Root{Source: source, Path: path})
	}
	if root := os.Getenv("CODEX_HOME"); root != "" {
		add("codex", filepath.Join(root, "sessions"))
	} else if home != "" {
		add("codex", filepath.Join(home, ".codex", "sessions"))
	}
	if root := os.Getenv("CLAUDE_CONFIG_DIR"); root != "" {
		add("claude", filepath.Join(root, "projects"))
	} else if home != "" {
		add("claude", filepath.Join(home, ".claude", "projects"))
	}
	if root := os.Getenv("QODER_CONFIG_DIR"); root != "" {
		add("qoder", filepath.Join(root, "projects"))
	} else if home != "" {
		add("qoder", filepath.Join(home, ".qoder", "projects"))
	}
	if root := os.Getenv("QODERCN_CONFIG_DIR"); root != "" {
		add("qoder-cn", filepath.Join(root, "projects"))
	} else if home != "" {
		add("qoder-cn", filepath.Join(home, ".qoder-cn", "projects"))
	}
	if root := os.Getenv("WORKBUDDY_CONFIG_DIR"); root != "" {
		add("workbuddy", filepath.Join(root, "projects"))
	} else if home != "" {
		add("workbuddy", filepath.Join(home, ".workbuddy", "projects"))
	}
	if root := os.Getenv("CODEBUDDY_CONFIG_DIR"); root != "" {
		add("codebuddy", filepath.Join(root, "projects"))
	} else if home != "" {
		add("codebuddy", filepath.Join(home, ".codebuddy", "projects"))
	}
	if root := os.Getenv("DSH_HOME"); root != "" {
		add("dsh", filepath.Join(root, "sessions"))
	} else if home != "" {
		add("dsh", filepath.Join(home, ".dsh", "sessions"))
	}
	if home != "" {
		add("zcode", filepath.Join(home, ".zcode", "cli", "db", "db.sqlite"))
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if dataHome != "" {
		add("opencode", filepath.Join(dataHome, "opencode", "opencode.db"))
	}
	return Config{
		Roots:            roots,
		PollInterval:     750 * time.Millisecond,
		IdlePollInterval: 2 * time.Second,
		WalkInterval:     3 * time.Second,
		Now:              time.Now,
	}
}

// FormatSpeed mirrors Agent Speed's compact menu-bar number formatting.
func FormatSpeed(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return "0"
	}
	if value < 10 {
		rounded := math.Round(value*10) / 10
		if rounded >= 10 {
			return "10"
		}
		return strconv.FormatFloat(rounded, 'f', 1, 64)
	}
	if value < 1000 {
		rounded := math.Round(value)
		if rounded >= 1000 {
			return "1.00k"
		}
		return strconv.FormatInt(int64(rounded), 10)
	}
	return compact(value)
}

type themeTitles struct {
	idle string
	// The 13 active rungs share Agent Speed's redline-relative thresholds.
	titles [13]string
}

var builtInTitles = map[string]themeTitles{
	TitleThemeVehicles: {
		idle: "待机",
		titles: [13]string{
			"步行", "自行车", "电动车", "摩托车", "汽车", "跑车", "高铁",
			"赛车", "磁悬浮列车", "喷气客机", "战斗机", "高超音速飞机", "火箭",
		},
	},
	TitleThemeAnimals: {
		idle: "打盹",
		titles: [13]string{
			"蜗牛", "乌龟", "刺猬", "老鼠", "松鼠", "猫", "兔子",
			"袋鼠", "马", "猎豹", "蝙蝠", "金雕", "游隼",
		},
	},
	TitleThemeAthletics: {
		idle: "热身",
		titles: [13]string{
			"慢走", "快走", "慢跑", "配速跑", "冲刺", "接力", "校纪录",
			"市纪录", "省纪录", "全国纪录", "亚洲纪录", "世界纪录", "人类极限",
		},
	},
	TitleThemeMusic: {
		idle: "静音",
		titles: [13]string{
			"极缓板", "庄板", "广板", "小广板", "柔板", "行板", "中板",
			"小快板", "快板", "活泼板", "急板", "最急板", "超频爆速",
		},
	},
	TitleThemeNetwork: {
		idle: "离线",
		titles: [13]string{
			"拨号", "ISDN", "ADSL", "3G", "HSPA+", "4G", "4G+",
			"千兆光纤", "5G", "5G-A", "万兆光纤", "6G", "数据中心互联",
		},
	},
	TitleThemeSound: {
		idle: "静止",
		titles: [13]string{
			"自行车", "汽车", "高铁", "客机", "亚音速", "跨音速", "音速",
			"超音速", "2 马赫", "3 马赫", "5 马赫", "高超音速", "再入大气层",
		},
	},
	TitleThemeStudent: {
		idle: "放假",
		titles: [13]string{
			"幼儿园", "小学", "初中", "高中", "本科", "硕士", "博士",
			"博士后", "讲师", "副教授", "教授", "院士", "诺奖级",
		},
	},
	TitleThemeCareer: {
		idle: "下班",
		titles: [13]string{
			"实习生", "助理", "专员", "资深专员", "主管", "经理", "高级经理",
			"总监", "副总裁", "总裁", "董事长", "商业传奇", "行业缔造者",
		},
	},
	TitleThemeSpace: {
		idle: "休眠",
		titles: [13]string{
			"星尘", "流星", "彗星", "月球", "行星", "恒星", "红巨星",
			"超新星", "中子星", "黑洞", "星系", "类星体", "大爆炸",
		},
	},
}

var titleThresholds = [13]float64{
	0.5, 8, 12, 16, 24, 32, 48, 68, 96, 136, 200, 280, 400,
}

// titleRatios 是 13 档称号相对红线的比例（取自 agent-speed 的 DESIGN §1），
// 基准红线 400 tok/s 时正好等于 titleThresholds。
var titleRatios = [13]float64{
	0, 0.02, 0.03, 0.04, 0.06, 0.08, 0.12, 0.17, 0.24, 0.34, 0.5, 0.7, 1.0,
}

// TitleThresholdsFor 返回按红线缩放的 13 档阈值：称号阶梯随红线整体缩放，
// 改红线不会改变「第几档」的含义。
func TitleThresholdsFor(redline float64) [13]float64 {
	out := titleThresholds
	if redline <= 0 {
		redline = DefaultRedline
	}
	out[0] = 0.5
	for i := 1; i < len(out); i++ {
		out[i] = math.Round(titleRatios[i]*redline*100) / 100
	}
	return out
}

// NormalizeTitleTheme maps unknown or empty IDs to the default theme.
func NormalizeTitleTheme(theme string) string {
	if _, ok := builtInTitles[theme]; ok {
		return theme
	}
	switch theme {
	case "wind", "cultivation", "wuxia", "mage", "gameRank", "engineer":
		return TitleThemeVehicles
	}
	return TitleThemeVehicles
}

// TitleThemeLabel returns the user-facing name of a built-in theme.
func TitleThemeLabel(theme string) string {
	return titleThemes[NormalizeTitleTheme(theme)].Label
}

// ThemeTitle returns the title for the selected built-in Agent Speed theme,
// using the baseline 400 tok/s ladder.
func ThemeTitle(theme string, value float64, active bool) string {
	return ThemeTitleAt(theme, value, active, 400)
}

// ThemeTitleAt returns the title for the given redline: the ladder scales with
// it, so the same level names stay meaningful at any redline.
func ThemeTitleAt(theme string, value float64, active bool, redline float64) string {
	spec := builtInTitles[NormalizeTitleTheme(theme)]
	if !active || value < 0.5 {
		return spec.idle
	}
	thresholds := TitleThresholdsFor(redline)
	for index := len(thresholds) - 1; index >= 0; index-- {
		if value >= thresholds[index] {
			return spec.titles[index]
		}
	}
	return spec.titles[0]
}

// TransportTitle returns the default traffic-vehicle title.
func TransportTitle(value float64, active bool) string {
	return ThemeTitle(TitleThemeVehicles, value, active)
}

func sessionID(agentID string) string {
	sum := sha256.Sum256([]byte(agentID))
	return hex.EncodeToString(sum[:])[:8]
}

func compact(value float64) string {
	thousands := value / 1000
	if round(thousands, 2) < 10 {
		return fixed(thousands, 2) + "k"
	}
	if round(thousands, 1) < 100 {
		return fixed(thousands, 1) + "k"
	}
	if math.Round(thousands) < 1000 {
		return strconv.FormatInt(int64(math.Round(thousands)), 10) + "k"
	}
	millions := value / 1_000_000
	if round(millions, 2) < 10 {
		return fixed(millions, 2) + "M"
	}
	if round(millions, 1) < 100 {
		return fixed(millions, 1) + "M"
	}
	if math.Round(millions) < 1000 {
		return strconv.FormatInt(int64(math.Round(millions)), 10) + "M"
	}
	billions := value / 1_000_000_000
	if round(billions, 2) < 10 {
		return fixed(billions, 2) + "B"
	}
	if round(billions, 1) < 100 {
		return fixed(billions, 1) + "B"
	}
	return strconv.FormatInt(int64(math.Round(billions)), 10) + "B"
}

func round(value float64, digits int) float64 {
	scale := math.Pow(10, float64(digits))
	return math.Round(value*scale) / scale
}

func fixed(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

func sourceLabel(source string) string {
	switch source {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "qoder":
		return "Qoder"
	case "qoder-cn":
		return "Qoder CN"
	case "workbuddy":
		return "WorkBuddy"
	case "codebuddy":
		return "CodeBuddy"
	case "opencode":
		return "OpenCode"
	case "zcode":
		return "ZCode"
	case "dsh":
		return "DeepSeek Harness"
	default:
		return source
	}
}

func clampTokens(value float64) int64 {
	if math.IsNaN(value) || value <= 0 {
		return 0
	}
	if math.IsInf(value, 1) || value >= float64(maxTokenValue) {
		return maxTokenValue
	}
	return int64(value)
}
