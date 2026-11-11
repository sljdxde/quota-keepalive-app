package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// accountLabelOf 从视图里取一个账号的备注，找不到账号时返回 ok=false。
func accountLabelOf(view View, provider, account string) (label string, ok bool) {
	for _, p := range view.Providers {
		if p.ID != provider {
			continue
		}
		for _, a := range p.Accounts {
			if a.Key == account {
				return a.Label, true
			}
		}
	}
	return "", false
}

func TestAccountLabelRoundTripShowsInViewAndCanBeCleared(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}

	// 备注是用户自己填的显示名（例如上游脱敏手机号的真实号码）；写入时收敛空白。
	if err := f.service.SetAccountLabel(ctx, "fixture", "a2", "  189 0719\n7198  "); err != nil {
		t.Fatal(err)
	}
	label, err := f.service.AccountLabel(ctx, "fixture", "a2")
	if err != nil {
		t.Fatal(err)
	}
	if label != "189 0719 7198" {
		t.Fatalf("备注应去掉换行与首尾空白，实际 %q", label)
	}

	view, err := f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := accountLabelOf(view, "fixture", "a2")
	if !ok || got != label {
		t.Fatalf("视图应带上备注 %q，实际 %q (found=%v)", label, got, ok)
	}
	if other, _ := accountLabelOf(view, "fixture", "a1"); other != "" {
		t.Fatalf("没设备注的账号不应有值，实际 %q", other)
	}

	// 空备注表示清除：视图回到上游标识。
	if err := f.service.SetAccountLabel(ctx, "fixture", "a2", "   "); err != nil {
		t.Fatal(err)
	}
	if label, err := f.service.AccountLabel(ctx, "fixture", "a2"); err != nil || label != "" {
		t.Fatalf("清除后应为空，实际 %q, err=%v", label, err)
	}
	view, err = f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := accountLabelOf(view, "fixture", "a2"); got != "" {
		t.Fatalf("清除后视图不应再带备注，实际 %q", got)
	}
}

func TestAccountLabelValidatesTruncatesAndRejectsMissingSlot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for _, tc := range []struct{ provider, account string }{
		{"", "a1"},
		{"fixture", " "},
	} {
		if err := f.service.SetAccountLabel(ctx, tc.provider, tc.account, "x"); !errors.Is(err, errAccountLabel) {
			t.Fatalf("缺少供应商/账号应报错，实际 %v", err)
		}
	}

	long := strings.Repeat("号", accountLabelMaxRunes+10)
	if err := f.service.SetAccountLabel(ctx, "fixture", "a1", long); err != nil {
		t.Fatal(err)
	}
	label, err := f.service.AccountLabel(ctx, "fixture", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if runes := []rune(label); len(runes) != accountLabelMaxRunes {
		t.Fatalf("备注应按 rune 截到 %d，实际 %d", accountLabelMaxRunes, len(runes))
	}
}

func TestAccountLabelGoesAwayWithItsAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetAccountLabel(ctx, "fixture", "a1", "工作号"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteAccount(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	if label, err := f.service.AccountLabel(ctx, "fixture", "a1"); err != nil || label != "" {
		t.Fatalf("删除账号后备注应一并删除，实际 %q, err=%v", label, err)
	}
}

// 同一个插槽换了账号（上游标识变化）时，QKA 会清空该插槽的历史；备注属于
// 旧账号，也必须一起清掉，否则新账号会顶着旧名字。
func TestAccountLabelClearedWhenSlotIdentityChanges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetAccountLabel(ctx, "fixture", "a2", "旧账号"); err != nil {
		t.Fatal(err)
	}
	f.runtime.providers[0].Accounts[1].AccountID = "13900139000"
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if label, err := f.service.AccountLabel(ctx, "fixture", "a2"); err != nil || label != "" {
		t.Fatalf("插槽换账号后备注应清空，实际 %q, err=%v", label, err)
	}
}
