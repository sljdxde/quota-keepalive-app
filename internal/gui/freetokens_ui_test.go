package gui

import (
	"testing"
)

// 免费额度页（AIHOT · Token 羊毛）是这一版新增的第六个页签：确认入口、
// 三档筛选、列表挂载点、绑定调用与纯函数模块都在产物里。
func TestFreeTokensPageShipped(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	requireStrings(t, page, "免费额度页",
		`data-tab="freetokens"`,
		`id="freetokens"`,
		`id="freetokens-tiers"`,
		`id="freetokens-list"`,
		`id="freetokens-hero"`,
		`id="freetokens-search"`,
		`id="freetokens-refresh"`,
		`Bound.FreeTokensGet(`,
		`Bound.OpenURL(`,
		`from "./assets/free-tokens.js"`,
		`function renderFreeTokens(`,
		`function loadFreeTokens(`,
		`function freeTokenAvatar(`,
		`providerIconSpec(providerId)`,
		`deal.qka_provider_icon`,
		`.ft-avatar.has-icon`,
		`.ft-avatar .provider-glyph`,
		`<h1>免费额度</h1>`,
		`AIHOT`,
	)

	script, err := frontendFS.ReadFile("frontend/assets/free-tokens.js")
	if err != nil {
		t.Fatal(err)
	}
	requireStrings(t, string(script), "免费额度纯函数",
		`export const TIERS`,
		`export function tierLabel(`,
		`export function kindLabel(`,
		`export function actionLabel(`,
		`export function formatYuan(`,
		`export function summaryText(`,
		`export function dealMeta(`,
		`export function filterDeals(`,
		`export function heroText(`,
		`export function findDealProvider(`,
	)

	bindings, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	requireStrings(t, string(bindings), "免费额度绑定",
		`export function FreeTokensGet(force)`,
		`$Call.ByID(3050685063, force)`,
		`export function OpenURL(raw)`,
		`$Call.ByID(3465478630, raw)`,
	)
}
