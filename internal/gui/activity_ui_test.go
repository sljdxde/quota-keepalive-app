package gui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"qka/internal/providers"
)

func TestAppsPageExposesDailyActivity(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`id="apps-activity"`,
		`assets/activity.js`,
		`refreshActivityView`,
		`activityForAccount`,
		`activityTaskRow`,
		`Bound.ActivityView()`,
		`Bound.ActivityRun(`,
		`Bound.ActivitySetEnabled(`,
		`每日活跃`,
		`立即活跃`,
		`今日未活跃`,
		`activityStateText`,
		`activitySummaryText`,
		// 每日活跃行的操作按钮必须比开关小一圈：只放大文字按钮，不能把
		// .switch 一起撑高（旧规则写的是 .task-actions button）。
		`.task-actions .mini-action {`,
		`.task-actions .switch { min-height: 0; }`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("应用管理页缺少每日活跃 UI: %q", want)
		}
	}
	for _, forbidden := range []string{`保活`, `data-tab="activity"`} {
		if strings.Contains(page, forbidden) {
			t.Errorf("应用管理页不应包含 %q", forbidden)
		}
	}
}

func TestActivityBindingsMatchWailsIDs(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	for _, want := range []string{
		`export function ActivityRun(provider, account)`,
		`$Call.ByID(919320725, provider, account)`,
		`export function ActivitySetEnabled(provider, account, enabled)`,
		`$Call.ByID(2942169009, provider, account, enabled)`,
		`export function ActivityView()`,
		`$Call.ByID(1504037297)`,
		// 现有绑定自检：hash 规则必须能复现这个已知值。
		`$Call.ByID(2607732007, target)`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("Wails JS 绑定缺少 %q", want)
		}
	}
}

func TestBoundActivityMethods(t *testing.T) {
	var ranProvider, ranAccount string
	var toggled bool
	view := providers.ActivityView{
		Accounts: []providers.ActivityAccountView{{
			Provider: "workbuddy", ProviderName: "WorkBuddy", Account: "a1",
			Masked: "****1234", Enabled: true, TaskEnabled: true,
			TodayState: "completed", SentToday: true, Streak: 3,
		}},
		Summary: providers.ActivitySummaryView{Total: 1, Done: 1, StreakMax: 3},
	}
	svc := NewBoundService(Deps{
		ActivityView: func(context.Context) (providers.ActivityView, error) {
			return view, nil
		},
		ActivityRun: func(_ context.Context, provider, account string) error {
			ranProvider, ranAccount = provider, account
			return nil
		},
		ActivitySetEnabled: func(_ context.Context, _ string, _ string, enabled bool) error {
			toggled = enabled
			return nil
		},
	})
	raw, err := svc.ActivityView()
	if err != nil {
		t.Fatal(err)
	}
	var decoded providers.ActivityView
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Total != 1 || decoded.Summary.Done != 1 ||
		decoded.Accounts[0].TodayState != "completed" {
		t.Fatalf("activity view = %+v", decoded)
	}
	if err := svc.ActivityRun("workbuddy", "a1"); err != nil {
		t.Fatal(err)
	}
	if ranProvider != "workbuddy" || ranAccount != "a1" {
		t.Fatalf("ActivityRun callback = %q/%q", ranProvider, ranAccount)
	}
	if err := svc.ActivitySetEnabled("workbuddy", "a1", true); err != nil {
		t.Fatal(err)
	}
	if !toggled {
		t.Fatal("ActivitySetEnabled callback was not called")
	}
}

func TestBoundActivityMethodsNotWired(t *testing.T) {
	svc := NewBoundService(Deps{})
	if _, err := svc.ActivityView(); err != errNotWired {
		t.Fatalf("ActivityView = %v", err)
	}
	if err := svc.ActivityRun("p", "a"); err != errNotWired {
		t.Fatalf("ActivityRun = %v", err)
	}
	if err := svc.ActivitySetEnabled("p", "a", true); err != errNotWired {
		t.Fatalf("ActivitySetEnabled = %v", err)
	}
}
