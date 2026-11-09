package freetokens

import "strings"

// Mapping 把 AIHOT 的厂商名映射到 QKA 已内置的 provider 插件。
//
// 只登记「QKA 已 vendor 的插件」：没有插件的厂商在页面里显示「暂无插件」，
// 不提供无法执行的安装/登录入口。IDs 是 provider 运行时的 target id（可多个），
// Match 是名称兜底关键字（插件升级改 id 时仍能匹配）。
type Mapping struct {
	// Provider 是 AIHOT 数据里的厂商名（映射键）。
	Provider string
	// Name 是 QKA 侧的展示名。
	Name string
	// IDs 是 provider runtime 的 target id 列表。
	IDs []string
	// Match 是 provider 展示名里应包含的关键字（大小写不敏感）。
	Match string
	// NPM 是插件的完整 npm 包名（用于与 provider-plugins 目录核对）。
	NPM string
	// Checkin 表示该插件支持签到。
	Checkin bool
}

// Mappings 与 provider-plugins/ 下已内置的插件保持一致；新增插件时同步登记。
var Mappings = []Mapping{
	{Provider: "豆包", Name: "豆包工作", IDs: []string{"doubao-work"}, Match: "doubao", NPM: "@magpie-community/opencode-doubao-work-auth"},
	{Provider: "Cloudflare", Name: "Cloudflare Workers AI", IDs: []string{"cloudflare-workers-ai"}, Match: "cloudflare", NPM: "@magpie-community/opencode-cloudflare-workers-ai-auth"},
	{Provider: "MiniMax", Name: "MiniMax Code", IDs: []string{"minimax-code"}, Match: "minimax", NPM: "@magpie-community/opencode-minimax-auth", Checkin: true},
	{Provider: "阿里云 Qoder", Name: "Qoder", IDs: []string{"qoder", "qoder-cn"}, Match: "qoder", NPM: "@magpie-community/opencode-qoder-auth", Checkin: true},
	{Provider: "字节 TRAE", Name: "Trae", IDs: []string{"trae-cn", "trae"}, Match: "trae", NPM: "@magpie-community/opencode-trae-auth", Checkin: true},
	{Provider: "腾讯 WorkBuddy", Name: "WorkBuddy", IDs: []string{"workbuddy"}, Match: "workbuddy", NPM: "@magpie-community/opencode-workbuddy-auth", Checkin: true},
	{Provider: "书生·端砚", Name: "书生·端砚", IDs: []string{"intern-inkstone"}, Match: "inkstone", NPM: "@magpie-community/opencode-inkstone-auth", Checkin: true},
	{Provider: "Zed", Name: "Zed", IDs: []string{"zed"}, Match: "zed", NPM: "@magpie-community/opencode-zed-auth"},
	{Provider: "华为云码道", Name: "CodeArts", IDs: []string{"codearts"}, Match: "codearts", NPM: "@magpie-community/opencode-codearts-auth", Checkin: true},
	{Provider: "智谱 AutoClaw", Name: "AutoClaw", IDs: []string{"autoclaw-cn", "autoclaw"}, Match: "autoclaw", NPM: "@magpie-community/opencode-autoclaw-auth", Checkin: true},
	{Provider: "智谱 ZCode", Name: "ZCode", IDs: []string{"zcode"}, Match: "zcode", NPM: "@magpie-community/opencode-zcode-auth", Checkin: true},
	{Provider: "商汤日日新", Name: "SenseNova", IDs: []string{"sensenova-token-plan", "sensenova"}, Match: "sensenova", NPM: "@magpie-community/opencode-sensenova-auth"},
	{Provider: "OpenCode", Name: "OpenCode Zen Free", IDs: []string{"opencode-zen-free"}, Match: "opencode", NPM: "@magpie-community/opencode-zen-free-auth"},
}

// mappingByProvider 按 trim 后的 AIHOT 厂商名索引。
var mappingByProvider = func() map[string]Mapping {
	index := make(map[string]Mapping, len(Mappings))
	for _, mapping := range Mappings {
		index[strings.TrimSpace(mapping.Provider)] = mapping
	}
	return index
}()

// LookupProvider 查 AIHOT 厂商名对应的映射；厂商名做 trim，未知厂商返回 false。
func LookupProvider(provider string) (Mapping, bool) {
	mapping, ok := mappingByProvider[strings.TrimSpace(provider)]
	return mapping, ok
}

// MatchesProvider 报告某个 QKA provider 视图是否对应这条映射：先比 target id，
// 再比展示名关键字（插件升级改 id 时兜底）。
func (m Mapping) MatchesProvider(id, name string) bool {
	for _, candidate := range m.IDs {
		if id == candidate {
			return true
		}
	}
	if m.Match == "" {
		return false
	}
	return strings.Contains(strings.ToLower(name), m.Match)
}
