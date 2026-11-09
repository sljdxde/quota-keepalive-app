package providers

import (
	"context"
	"testing"

	"qka/internal/features"
)

// TestActivityIsAvailableFromProviderSurfaces 验证「每日活跃」已经恢复为可用
// 功能：provider 视图暴露 activity 能力，自动任务能同时产出 checkin 与
// activity，单独 action=activity 只产出活动心跳。
func TestActivityIsAvailableFromProviderSurfaces(t *testing.T) {
	provider := ProviderView{ID: "fixture", Checkin: true, Activity: true}
	account := AccountView{
		Key:     "a1",
		Enabled: true,
		TaskPreferences: map[string]bool{
			"checkin": true, "keepalive": true,
		},
	}
	for _, action := range []string{"all", "activity", "keepalive"} {
		ops := providerOperations(provider, account, action)
		found := false
		for _, operation := range ops {
			if operation == "keepalive" {
				found = true
			}
		}
		if !found {
			t.Fatalf("action %q 应执行每日活跃，实际 %+v", action, ops)
		}
	}
	if ops := providerOperations(provider, account, "checkin"); len(ops) != 1 || ops[0] != "checkin" {
		t.Fatalf("checkin 不应触发每日活跃: %+v", ops)
	}

	f := newFixture(t)
	ctx := context.Background()
	view, err := listFixtureEnabled(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Providers[0].Activity {
		t.Fatal("provider 视图应暴露每日活跃能力")
	}
	if !view.Providers[0].Accounts[0].TaskPreferences["keepalive"] {
		t.Fatal("每日活跃任务默认应开启")
	}
	summary, err := f.service.RunAll(ctx, "activity", nil)
	if err != nil {
		t.Fatalf("RunAll(activity) = %v", err)
	}
	if summary.Attempted != 2 || summary.Succeeded != 2 {
		t.Fatalf("activity summary = %+v", summary)
	}
}

// TestOperationViewsOnlyExposeCheckin 保持签到行的口径：账号卡片的
// operations 仍只列 checkin，每日活跃由独立视图提供，避免「活跃成功」
// 被误读成「签到完成」。
func TestOperationViewsOnlyExposeCheckin(t *testing.T) {
	f := newFixture(t)
	ops, err := f.service.operationViews(context.Background(), "fixture", "a1",
		"2026-10-30", map[string]bool{"checkin": true, "keepalive": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Operation != "checkin" {
		t.Fatalf("operation views = %+v, want only checkin", ops)
	}
}

func TestActivityActionAccepted(t *testing.T) {
	if !features.ValidAction("activity") || !features.ValidAction("all") {
		t.Fatal("all/activity must be valid actions")
	}
}
