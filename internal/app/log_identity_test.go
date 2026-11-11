package app

import (
	"testing"

	"qka/internal/gui"
	"qka/internal/providers"
)

// 日志里存的是 codearts#u90nyn 这类 key；展示时要换成真实账号标识与供应商名。
func TestEnrichLogIdentitiesUsesProviderView(t *testing.T) {
	view := providers.View{Providers: []providers.ProviderView{{
		ID: "codearts", Name: "CodeArts",
		Accounts: []providers.AccountView{
			{Key: "codearts#u90nyn", Identity: "hw063442711284743", MaskedIdentity: "****4743"},
			{Key: "codearts", Identity: "hw083749283", MaskedIdentity: "****9283"},
		},
	}}}
	entries := []gui.LogEntry{
		{ID: 1, Target: "codearts", AccountID: "codearts#u90nyn", Message: "签到"},
		{ID: 2, Target: "codearts", AccountID: "", Message: "定时任务"},
		{ID: 3, Target: "unknown", AccountID: "unknown#1", Message: "未知供应商"},
	}
	got := enrichLogIdentities(entries, view)
	if got[0].AccountIdentity != "hw063442711284743" || got[0].AccountMasked != "****4743" {
		t.Fatalf("账号标识未替换: %+v", got[0])
	}
	if got[0].TargetName != "CodeArts" {
		t.Fatalf("供应商名未替换: %+v", got[0])
	}
	if got[1].AccountIdentity != "" || got[1].AccountMasked != "" {
		t.Fatalf("无账号的行不应写入标识: %+v", got[1])
	}
	if got[1].TargetName != "CodeArts" {
		t.Fatalf("无账号的行仍应有供应商名: %+v", got[1])
	}
	if got[2].AccountIdentity != "" || got[2].TargetName != "" {
		t.Fatalf("未知供应商应保持原值: %+v", got[2])
	}
}
