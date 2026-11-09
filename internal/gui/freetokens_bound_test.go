package gui

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"qka/internal/freetokens"
)

// FreeTokensGet 必须把 force 透传给后端：页面「刷新」按钮靠它绕过 10 分钟缓存。
func TestFreeTokensGetWiresForce(t *testing.T) {
	var gotForce bool
	service := &BoundService{deps: Deps{
		FreeTokens: func(_ context.Context, force bool) (freetokens.View, error) {
			gotForce = force
			return freetokens.View{
				Deals: []freetokens.DealView{{
					Deal: freetokens.Deal{ID: "minimax", Provider: "MiniMax"},
					Tier: freetokens.TierReady,
				}},
				Summary: freetokens.Summary{Ready: 1},
			}, nil
		},
	}}

	data, err := service.FreeTokensGet(false)
	if err != nil {
		t.Fatalf("FreeTokensGet(false): %v", err)
	}
	if gotForce {
		t.Fatal("force=false 不应强制刷新")
	}
	var view freetokens.View
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatalf("解析视图: %v", err)
	}
	if len(view.Deals) != 1 || view.Deals[0].Tier != freetokens.TierReady || view.Summary.Ready != 1 {
		t.Fatalf("视图异常: %+v", view)
	}

	if _, err := service.FreeTokensGet(true); err != nil {
		t.Fatalf("FreeTokensGet(true): %v", err)
	}
	if !gotForce {
		t.Fatal("force=true 应透传给后端")
	}
}

func TestFreeTokensGetUnwired(t *testing.T) {
	if _, err := (&BoundService{}).FreeTokensGet(false); err == nil {
		t.Fatal("未接线时应返回错误")
	}
}

func TestOpenURLWiringAndError(t *testing.T) {
	var opened string
	service := &BoundService{deps: Deps{OpenURL: func(url string) error {
		opened = url
		return nil
	}}}
	if err := service.OpenURL("https://aihot.news/free-tokens"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if opened != "https://aihot.news/free-tokens" {
		t.Fatalf("打开的链接 = %q", opened)
	}

	failing := &BoundService{deps: Deps{OpenURL: func(string) error { return errors.New("blocked") }}}
	if err := failing.OpenURL("https://example.com"); err == nil {
		t.Fatal("OpenURL 应透传错误")
	}
	if err := (&BoundService{}).OpenURL("https://example.com"); err == nil {
		t.Fatal("未接线时应返回错误")
	}
}
