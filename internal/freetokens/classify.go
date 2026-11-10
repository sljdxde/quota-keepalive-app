package freetokens

import (
	"math"
	"strings"
)

// 用户视角的三档：已配置可用 / 有插件待配置 / 暂无插件。
const (
	// TierReady 已内置插件且已登录（可用）。
	TierReady = "ready"
	// TierSetup 已内置插件但还没配置账号。
	TierSetup = "setup"
	// TierNone 没有 QKA 插件可接。
	TierNone = "none"
)

// ProviderState 是分类时需要的 QKA provider 现场状态（由 GUI 层从
// providers.View 投影，保持本包不依赖 providers/runtime）。
type ProviderState struct {
	ID   string
	Name string
	// Icon 是插件声明的图标（data: URI 或 https URL）；没有时留空。
	Icon     string
	Enabled  bool
	SignedIn bool
	Accounts int
	Checkin  bool
}

// DealView 是一条带分档与行动建议的 deal。
type DealView struct {
	Deal
	Tier            string `json:"tier"`
	Action          string `json:"action,omitempty"`
	QKAProviderID   string `json:"qka_provider_id,omitempty"`
	QKAProvider     string `json:"qka_provider,omitempty"`
	QKAProviderIcon string `json:"qka_provider_icon,omitempty"`
	Supported       bool   `json:"supported"`
	SignedIn        bool   `json:"signed_in"`
	Enabled         bool   `json:"enabled"`
	Checkin         bool   `json:"checkin"`
}

// Summary 是三档计数与今日折算。
type Summary struct {
	Ready     int     `json:"ready"`
	Setup     int     `json:"setup"`
	None      int     `json:"none"`
	Counted   int     `json:"counted"`
	TodayYuan float64 `json:"today_yuan"`
}

// View 是提供给前端的完整投影。
type View struct {
	Deals     []DealView `json:"deals"`
	Summary   Summary    `json:"summary"`
	Basis     Basis      `json:"basis"`
	ServerNow string     `json:"server_now"`
	FetchedAt string     `json:"fetched_at"`
	Stale     bool       `json:"stale,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// Classify 把 AIHOT 快照与 QKA provider 现场状态合成为三档视图。
// fetchErr 非空表示本次拉取失败（可能返回旧快照，stale=true）。
func Classify(snapshot Snapshot, states []ProviderState, fetchErr error) View {
	view := View{
		Basis: snapshot.Basis,
		Stale: fetchErr != nil && !snapshot.FetchedAt.IsZero(),
	}
	if fetchErr != nil {
		view.Error = fetchErr.Error()
	}
	if !snapshot.FetchedAt.IsZero() {
		view.FetchedAt = snapshot.FetchedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	view.ServerNow = snapshot.ServerNow
	for _, deal := range snapshot.Deals {
		item := DealView{Deal: deal}
		mapping, ok := LookupProvider(deal.Provider)
		if !ok {
			item.Tier = TierNone
			view.Summary.None++
		} else {
			item.Supported = true
			item.QKAProvider = mapping.Name
			item.Checkin = mapping.Checkin
			state, found := findState(mapping, states)
			switch {
			case !found:
				// 插件已内置但运行时没注册出这个 provider（版本差异/加载失败）。
				item.Tier = TierSetup
				item.Action = "open-apps"
				view.Summary.Setup++
			case !state.Enabled:
				// 有账号但「应用管理」里没启用：页面口径是「启用且已登录」才算
				// 已配置可用，否则会把一堆没启用的插件误报成可用；这里归到待
				// 配置并引导去应用管理启用。
				item.Tier = TierSetup
				item.Action = "open-apps"
				item.QKAProviderID = state.ID
				item.QKAProviderIcon = state.Icon
				item.Enabled = false
				item.SignedIn = state.SignedIn || state.Accounts > 0
				view.Summary.Setup++
			case state.SignedIn || state.Accounts > 0:
				item.Tier = TierReady
				item.SignedIn = true
				item.Enabled = true
				item.QKAProviderID = state.ID
				item.QKAProviderIcon = state.Icon
				if mapping.Checkin {
					item.Action = "checkin"
				} else {
					item.Action = "open-apps"
				}
				view.Summary.Ready++
			default:
				item.Tier = TierSetup
				item.Action = "login"
				item.QKAProviderID = state.ID
				item.QKAProviderIcon = state.Icon
				item.Enabled = state.Enabled
				view.Summary.Setup++
			}
		}
		if deal.Counted {
			view.Summary.Counted++
			if deal.Yuan != nil {
				view.Summary.TodayYuan += *deal.Yuan
			}
		}
		view.Deals = append(view.Deals, item)
	}
	view.Summary.TodayYuan = math.Round(view.Summary.TodayYuan)
	return view
}

// findState 在 QKA provider 现场状态里找映射对应的那条。
func findState(mapping Mapping, states []ProviderState) (ProviderState, bool) {
	for _, state := range states {
		if mapping.MatchesProvider(state.ID, state.Name) {
			return state, true
		}
	}
	return ProviderState{}, false
}

// NormalizeKind 把数据里的 kind 收敛成 UI 用的小写标签；未知 kind 返回原值。
func NormalizeKind(kind string) string {
	value := strings.ToLower(strings.TrimSpace(kind))
	switch value {
	case "daily", "monthly", "once", "limited", "free":
		return value
	}
	return value
}
