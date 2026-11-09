package freetokens

import (
	"errors"
	"testing"
	"time"
)

func yuan(value float64) *float64 { return &value }

func TestClassifyThreeTiers(t *testing.T) {
	snapshot := Snapshot{
		FetchedAt: time.Now(),
		Basis:     Basis{Mix: "一般用量", Ratio: "7 : 2 : 1", USDCNY: 6.6713},
		Deals: []Deal{
			{ID: "minimax", Provider: "MiniMax", Kind: "daily", Counted: true, Yuan: yuan(6.5)},
			{ID: "qoder", Provider: "阿里云 Qoder", Kind: "daily", Counted: true, Yuan: yuan(2)},
			{ID: "cloudflare", Provider: "Cloudflare", Kind: "daily"},
			{ID: "amd", Provider: "AMD Radeon Cloud", Kind: "daily", Counted: true, Yuan: yuan(1)},
		},
	}
	states := []ProviderState{
		{ID: "minimax-code", Name: "MiniMax Code", Enabled: true, SignedIn: true, Accounts: 2, Checkin: true},
		{ID: "qoder-cn", Name: "Qoder CN", Enabled: true},
	}
	view := Classify(snapshot, states, nil)
	if len(view.Deals) != 4 {
		t.Fatalf("deals = %d, want 4", len(view.Deals))
	}
	assertDeal(t, view.Deals[0], TierReady, "checkin", "minimax-code")
	assertDeal(t, view.Deals[1], TierSetup, "login", "qoder-cn")
	// Cloudflare 有映射（插件内置），但运行时没有注册出 provider → 待配置。
	assertDeal(t, view.Deals[2], TierSetup, "open-apps", "")
	// 没有映射的厂商 → 暂无插件。
	assertDeal(t, view.Deals[3], TierNone, "", "")
	if view.Summary.Ready != 1 || view.Summary.Setup != 2 || view.Summary.None != 1 {
		t.Fatalf("summary 异常: %+v", view.Summary)
	}
	// 只有 counted && yuan!=nil 的 deal 计入今日折算：6.5 + 2 + 1 = 9.5 → 10。
	if view.Summary.TodayYuan != 10 {
		t.Fatalf("today yuan = %v, want 10", view.Summary.TodayYuan)
	}
	if view.Summary.Counted != 3 {
		t.Fatalf("counted = %d, want 3", view.Summary.Counted)
	}
	if view.Basis.USDCNY != 6.6713 || view.ServerNow != snapshot.ServerNow {
		t.Fatalf("basis/serverNow 透传异常: %+v", view)
	}
}

func assertDeal(t *testing.T, deal DealView, tier, action, providerID string) {
	t.Helper()
	if deal.Tier != tier || deal.Action != action || deal.QKAProviderID != providerID {
		t.Fatalf("deal %s: tier=%q action=%q provider=%q，want %q/%q/%q",
			deal.ID, deal.Tier, deal.Action, deal.QKAProviderID, tier, action, providerID)
	}
}

func TestClassifyMarksStaleOnFetchError(t *testing.T) {
	snapshot := Snapshot{FetchedAt: time.Now(), Deals: []Deal{{ID: "x", Provider: "MiniMax"}}}
	view := Classify(snapshot, nil, errors.New("AIHOT 502"))
	if !view.Stale || view.Error == "" {
		t.Fatalf("stale/err 未设置: stale=%v err=%q", view.Stale, view.Error)
	}
}

func TestClassifyEmptyDealsIsSafe(t *testing.T) {
	view := Classify(Snapshot{}, nil, errors.New("down"))
	if view.Stale {
		t.Fatal("没有任何快照时不应标记 stale")
	}
	if view.Deals != nil {
		t.Fatal("空快照不应产出 deals")
	}
	if view.Summary != (Summary{}) {
		t.Fatalf("空快照 summary 应为零值: %+v", view.Summary)
	}
}

func TestNormalizeKind(t *testing.T) {
	for _, kind := range []string{"daily", "monthly", "once", "limited", "free"} {
		if got := NormalizeKind(" " + kind + " "); got != kind {
			t.Fatalf("NormalizeKind(%q) = %q", kind, got)
		}
	}
	if got := NormalizeKind("weekend"); got != "weekend" {
		t.Fatalf("未知 kind 应原样返回: %q", got)
	}
}
