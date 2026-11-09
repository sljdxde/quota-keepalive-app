package providers

import (
	"context"
	"strings"
	"testing"
	"time"

	"qka/internal/engine"
	"qka/internal/providerruntime"
)

func TestActivitySenderUsesFreeModelAndHiPrompt(t *testing.T) {
	f := newFixture(t)
	f.runtime.providers[0].Models = []providerruntime.Model{
		{ID: "paid-fast", Free: false, Rate: 0, APIURL: "https://paid.example/v1"},
		{ID: "free-slow", Free: true, Rate: 1.0, Context: 128000, APIURL: "https://free.example/v1"},
		{ID: "free-cheap", Free: true, Rate: 0.2, Context: 32000, APIURL: "https://free2.example/v1"},
	}
	f.runtime.providers[0].Accounts[0].Models = nil
	result, outcome := f.service.activity(context.Background(), f.runtime, "fixture", "a1")
	if result.Kind != engine.RSuccess || outcome.State != "completed" ||
		outcome.Model != "free-cheap" || outcome.Cost != 0 {
		t.Fatalf("activity result = %+v outcome=%+v", result, outcome)
	}
	request := f.runtime.fetchRequest
	if !strings.Contains(request.Body, `"content":"hi"`) ||
		!strings.Contains(request.Body, `"max_tokens":16`) ||
		!strings.Contains(request.Body, `"stream":false`) {
		t.Fatalf("activity request body = %s", request.Body)
	}
}

func TestActivitySenderFailureClassification(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*fakeRuntime)
		wantState string
		wantKind  engine.ResultKind
		wantCalls int
	}{
		{"success", func(*fakeRuntime) {}, "completed", engine.RSuccess, 1},
		{"unauthorized 401", func(f *fakeRuntime) { f.fetch.Status = 401 }, "unauthorized", engine.RFinal, 1},
		{"rate limited 429", func(f *fakeRuntime) { f.fetch.Status = 429 }, "rate_limited", engine.RRetryable, 1},
		{"server error 500", func(f *fakeRuntime) { f.fetch.Status = 500 }, "failed_retryable", engine.RRetryable, 1},
		{"no reply", func(f *fakeRuntime) { f.fetch.Body = []byte(`{}`) }, "no_reply", engine.RRetryable, 1},
		{"no free model on metered account", func(f *fakeRuntime) {
			f.providers[0].Accounts[0].Type = "api"
			f.providers[0].Models = []providerruntime.Model{{ID: "paid", APIURL: "https://paid.example/v1"}}
		}, "no_free_model", engine.RRetryable, 0},
		{"no model at all", func(f *fakeRuntime) { f.providers[0].Models = nil }, "no_free_model", engine.RRetryable, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.configure(f.runtime)
			result, outcome := f.service.activity(context.Background(), f.runtime, "fixture", "a1")
			if result.Kind != tc.wantKind || outcome.State != tc.wantState {
				t.Fatalf("result=%+v outcome=%+v", result, outcome)
			}
			if f.runtime.fetchCalls != tc.wantCalls {
				t.Fatalf("fetches = %d, want %d", f.runtime.fetchCalls, tc.wantCalls)
			}
		})
	}
}

// 订阅制账号没有 Free 模型时允许退到最低价模型（用量包含在订阅里）；
// 按量计费账号绝不自动花钱。两条规则分别覆盖。
func TestActivitySenderFallsBackToCheapestModelForSubscriptionAccount(t *testing.T) {
	f := newFixture(t)
	f.runtime.providers[0].Models = []providerruntime.Model{
		{ID: "paid-expensive", Rate: 1.2, Context: 64000, APIURL: "https://paid.example/v1"},
		{ID: "paid-cheap", Rate: 0.1, Context: 32000, APIURL: "https://cheap.example/v1"},
	}
	result, outcome := f.service.activity(context.Background(), f.runtime, "fixture", "a1")
	if result.Kind != engine.RSuccess || outcome.State != "completed" {
		t.Fatalf("fallback result = %+v outcome=%+v", result, outcome)
	}
	if outcome.Model != "paid-cheap" {
		t.Fatalf("应选择最低价模型，实际 %q", outcome.Model)
	}
	if !strings.Contains(outcome.Message, "订阅") {
		t.Fatalf("订阅额度发送应标注，message=%q", outcome.Message)
	}
	if f.runtime.fetchCalls != 1 {
		t.Fatalf("fetches = %d, want 1", f.runtime.fetchCalls)
	}
}

func TestActivitySenderSkipsMeteredAccountWithoutFreeModel(t *testing.T) {
	f := newFixture(t)
	f.runtime.providers[0].Accounts[0].Type = "api"
	f.runtime.providers[0].Models = []providerruntime.Model{
		{ID: "paid-cheap", Rate: 0.1, APIURL: "https://cheap.example/v1"},
	}
	result, outcome := f.service.activity(context.Background(), f.runtime, "fixture", "a1")
	if result.Kind != engine.RRetryable || outcome.State != "no_free_model" {
		t.Fatalf("metered skip result = %+v outcome=%+v", result, outcome)
	}
	if !strings.Contains(outcome.Message, "按量计费") {
		t.Fatalf("按量计费跳过应给出原因，message=%q", outcome.Message)
	}
	if f.runtime.fetchCalls != 0 {
		t.Fatalf("按量计费账号不应发请求，fetches=%d", f.runtime.fetchCalls)
	}
}

func TestActivitySenderRetriesWithStreamingWhenRequired(t *testing.T) {
	f := newFixture(t)
	f.runtime.fetchQueue = []providerruntime.FetchResponse{
		{Status: 400, Body: []byte(`{"msg":"Non-stream chat request is currently not supported"}`)},
		{Status: 200, Body: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n")},
	}
	result, outcome := f.service.activity(context.Background(), f.runtime, "fixture", "a1")
	if result.Kind != engine.RSuccess || outcome.State != "completed" {
		t.Fatalf("stream fallback = %+v %+v", result, outcome)
	}
	if f.runtime.fetchCalls != 2 || !strings.Contains(f.runtime.fetchRequest.Body, `"stream":true`) {
		t.Fatalf("stream fallback calls=%d body=%s", f.runtime.fetchCalls, f.runtime.fetchRequest.Body)
	}
}

func TestRunActivityIsIdempotentPerBeijingDay(t *testing.T) {
	f := newFixture(t)
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.service.RunActivity(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	if f.runtime.fetchCalls != 1 {
		t.Fatalf("first activity fetches = %d", f.runtime.fetchCalls)
	}
	err := f.service.RunActivity(ctx, "fixture", "a1")
	if err == nil || !strings.Contains(err.Error(), "今日已活跃") {
		t.Fatalf("second activity = %v, want readable already-active error", err)
	}
	if f.runtime.fetchCalls != 1 {
		t.Fatalf("second activity must not send, fetches = %d", f.runtime.fetchCalls)
	}
}

func TestRunActivityRetryableFailureEntersCooldown(t *testing.T) {
	f := newFixture(t)
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	f.runtime.fetch.Status = 500
	ctx := context.Background()
	err := f.service.RunActivity(ctx, "fixture", "a1")
	if err == nil || !strings.Contains(err.Error(), "稍后自动重试") {
		t.Fatalf("first activity failure = %v, want readable retryable error", err)
	}
	err = f.service.RunActivity(ctx, "fixture", "a1")
	if err == nil || !strings.Contains(err.Error(), "冷却中") {
		t.Fatalf("cooldown run = %v, want readable cooldown error", err)
	}
	if f.runtime.fetchCalls != 1 {
		t.Fatalf("cooldown must not send, fetches = %d", f.runtime.fetchCalls)
	}
	var state string
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT state FROM daily_ops
WHERE target='fixture' AND account_id='a1' AND operation='keepalive'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(engine.FailedRetryable) {
		t.Fatalf("activity state = %s", state)
	}
	var records int
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM activity_runs WHERE provider='fixture' AND account_id='a1'`,
	).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 1 {
		t.Fatalf("activity_runs records = %d, want 1", records)
	}
}

func TestRunAllAllRunsCheckinAndActivity(t *testing.T) {
	f := newFixture(t)
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	summary, err := f.service.RunAll(context.Background(), "all", nil)
	if err != nil {
		t.Fatalf("RunAll(all) = %v", err)
	}
	if summary.Attempted != 4 || summary.Succeeded != 4 {
		t.Fatalf("RunAll(all) summary = %+v", summary)
	}
	if f.runtime.checkinCalls != 2 || f.runtime.fetchCalls != 2 {
		t.Fatalf("checkin=%d activity=%d", f.runtime.checkinCalls, f.runtime.fetchCalls)
	}
}

func TestBuildActivityViewStreaksAndSummary(t *testing.T) {
	accounts := []ActivityAccountInput{
		{Provider: "workbuddy", ProviderName: "WorkBuddy", Account: "a1", Masked: "****1234", Enabled: true, TaskEnabled: true},
		{Provider: "qoder", ProviderName: "Qoder", Account: "b1", Masked: "****5678", Enabled: true, TaskEnabled: true},
	}
	runs := []ActivityRecord{
		{Provider: "workbuddy", Account: "a1", Day: "2026-11-09", State: "completed", TS: "2026-11-09T01:00:00Z"},
		{Provider: "workbuddy", Account: "a1", Day: "2026-11-08", State: "completed", TS: "2026-11-08T01:00:00Z"},
		{Provider: "workbuddy", Account: "a1", Day: "2026-11-07", State: "completed", TS: "2026-11-07T01:00:00Z"},
		{Provider: "workbuddy", Account: "a1", Day: "2026-11-05", State: "completed", TS: "2026-11-05T01:00:00Z"},
		// 今天未成功，但从昨天起连续 2 天；断档后不再计数。
		{Provider: "qoder", Account: "b1", Day: "2026-11-08", State: "completed", TS: "2026-11-08T02:00:00Z"},
		{Provider: "qoder", Account: "b1", Day: "2026-11-07", State: "completed", TS: "2026-11-07T02:00:00Z"},
		{Provider: "qoder", Account: "b1", Day: "2026-11-06", State: "completed", TS: "2026-11-06T02:00:00Z"},
	}
	view := BuildActivityView(accounts, runs, "2026-11-09",
		time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC))
	if view.Summary.Total != 2 || view.Summary.Done != 1 || view.Summary.StreakMax != 3 {
		t.Fatalf("summary = %+v", view.Summary)
	}
	if view.Accounts[0].Streak != 3 || !view.Accounts[0].SentToday {
		t.Fatalf("workbuddy projection = %+v", view.Accounts[0])
	}
	if view.Accounts[1].Streak != 3 || view.Accounts[1].SentToday {
		t.Fatalf("qoder projection = %+v", view.Accounts[1])
	}
	if view.Accounts[0].LastAt != "2026-11-09T01:00:00Z" {
		t.Fatalf("last_at = %q", view.Accounts[0].LastAt)
	}
}

func TestBuildActivityViewTodayFailureReason(t *testing.T) {
	accounts := []ActivityAccountInput{{
		Provider: "workbuddy", ProviderName: "WorkBuddy", Account: "a1",
		Masked: "****1234", Enabled: true, TaskEnabled: true,
		TodayState: string(engine.FailedRetryable), TodayMessage: "旧状态",
	}}
	runs := []ActivityRecord{{
		Provider: "workbuddy", Account: "a1", Day: "2026-11-09",
		State: "no_free_model", Message: "没有可用的免费模型", TS: "2026-11-09T01:00:00Z",
	}}
	view := BuildActivityView(accounts, runs, "2026-11-09", time.Now())
	item := view.Accounts[0]
	if item.TodayState != "no_free_model" || item.SentToday ||
		item.TodayMessage != "没有可用的免费模型" || view.Summary.Failed != 1 {
		t.Fatalf("failure projection = %+v summary=%+v", item, view.Summary)
	}
}

func TestBuildActivityViewStreakIgnoresTodayWhenNotSent(t *testing.T) {
	accounts := []ActivityAccountInput{{
		Provider: "p", ProviderName: "P", Account: "a", Enabled: true, TaskEnabled: true,
	}}
	runs := []ActivityRecord{
		{Provider: "p", Account: "a", Day: "2026-11-08", State: "completed", TS: "t8"},
		{Provider: "p", Account: "a", Day: "2026-11-07", State: "completed", TS: "t7"},
		{Provider: "p", Account: "a", Day: "2026-11-05", State: "completed", TS: "t5"},
	}
	item := BuildActivityView(accounts, runs, "2026-11-09", time.Now()).Accounts[0]
	if item.Streak != 2 {
		t.Fatalf("streak = %d, want 2 (today not sent, yesterday backwards)", item.Streak)
	}
}

func TestServiceActivityViewReadsDailyOpsAndHistory(t *testing.T) {
	f := newFixture(t)
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.service.RunActivity(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.ActivityView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary.Total != 2 || view.Summary.Done != 1 {
		t.Fatalf("activity view summary = %+v", view.Summary)
	}
	byAccount := map[string]ActivityAccountView{}
	for _, item := range view.Accounts {
		byAccount[item.Account] = item
	}
	if !byAccount["a1"].SentToday || byAccount["a1"].Streak != 1 ||
		byAccount["a1"].LastAt == "" {
		t.Fatalf("a1 activity = %+v", byAccount["a1"])
	}
	if byAccount["a2"].SentToday || byAccount["a2"].TodayState != "none" {
		t.Fatalf("a2 activity = %+v", byAccount["a2"])
	}
}

func TestSetActivityEnabledPersistsTaskPreference(t *testing.T) {
	f := newFixture(t)
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.service.SetActivityEnabled(ctx, "fixture", "a1", false); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.ActivityView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Accounts {
		if item.Account == "a1" && item.TaskEnabled {
			t.Fatalf("activity task preference not persisted: %+v", item)
		}
		if item.Account == "a2" && !item.TaskEnabled {
			t.Fatalf("other accounts keep the default enabled preference: %+v", item)
		}
	}
}
