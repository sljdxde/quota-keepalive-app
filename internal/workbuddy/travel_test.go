package workbuddy

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeTravelUpstream struct {
	state       string
	limit       bool
	restricted  bool
	buddy       bool // Buddy 实例已存在
	fbClaimed   bool
	fbCompleted bool
	departCalls int
	claimCalls  int
}

func (f *fakeTravelUpstream) fetch(_ context.Context, method, rawURL string, body []byte) (int, []byte, error) {
	if f.restricted {
		return 403, []byte(`{"code":11140,"msg":"request illegal"}`), nil
	}
	path := rawURL
	if parsed, err := url.Parse(rawURL); err == nil {
		path = parsed.Path
	}
	envelope := func(code int, data any) []byte {
		payload := map[string]any{"code": code, "msg": "ok"}
		if data != nil {
			payload["data"] = data
		}
		raw, _ := json.Marshal(payload)
		return raw
	}
	switch {
	case strings.HasSuffix(path, travelStatusPath):
		return 200, envelope(0, map[string]any{
			"state": f.state, "daily_limit_reached": f.limit,
		}), nil
	case strings.HasSuffix(path, travelConfigPath):
		return 200, envelope(0, map[string]any{
			"locations": []map[string]any{{"id": 7, "name": "猫猫岛"}},
		}), nil
	case strings.HasSuffix(path, travelDepartPath):
		f.departCalls++
		if !f.buddy {
			return 200, []byte(`{"code":500,"msg":"no active buddy"}`), nil
		}
		return 200, envelope(0, map[string]any{
			"location": map[string]any{"name": "火山口"},
		}), nil
	case strings.HasSuffix(path, travelClaimPath):
		f.claimCalls++
		return 200, envelope(0, map[string]any{"reward_credit": 88}), nil
	case strings.HasSuffix(path, tasksPath):
		status := "not_accepted"
		current := 0
		if f.fbClaimed {
			status = "claimed"
		} else if f.fbCompleted {
			status = "completed"
			current = 1
		}
		return 200, envelope(0, map[string]any{"tasks": []map[string]any{{
			"task_code": firstBuddyCode, "task_type": "auto",
			"accept_status": status,
			"progress":      map[string]any{"current": current, "target": 1},
		}}}), nil
	case strings.HasSuffix(path, tasksReportPath):
		f.fbCompleted = true
		return 200, envelope(0, nil), nil
	case strings.Contains(path, firstBuddyCode) && strings.HasSuffix(path, "/claim"):
		f.fbClaimed = true
		f.buddy = true
		return 200, envelope(0, map[string]any{"credit": 300}), nil
	}
	return 404, []byte(`{"code":404,"msg":"not found"}`), nil
}

func newTestRunner(f *fakeTravelUpstream) *TravelRunner {
	return NewTravelRunner(TravelConfig{
		BaseURL: "https://copilot.tencent.com",
		UID:     "uid-travel",
		Fetch:   f.fetch,
		Now:     func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) },
		Sleep:   func(context.Context, time.Duration) error { return nil },
	})
}

func TestTravelArrivedClaimsReward(t *testing.T) {
	f := &fakeTravelUpstream{state: "arrived"}
	result, err := newTestRunner(f).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Action != "claim" || result.Credit != 88 {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Message, "88") {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestTravelIdleDepartsWithLocation(t *testing.T) {
	f := &fakeTravelUpstream{state: "idle", buddy: true}
	result, err := newTestRunner(f).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Action != "depart" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Message, "火山口") {
		t.Fatalf("message = %q", result.Message)
	}
	if f.departCalls != 1 {
		t.Fatalf("depart calls = %d", f.departCalls)
	}
}

func TestTravelIdleEnsuresBuddyThenDeparts(t *testing.T) {
	f := &fakeTravelUpstream{state: "idle"}
	result, err := newTestRunner(f).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Action != "depart" {
		t.Fatalf("result = %+v", result)
	}
	if !f.fbClaimed {
		t.Fatal("first_buddy should be claimed before retry")
	}
	if f.departCalls != 2 {
		t.Fatalf("depart calls = %d, want retry", f.departCalls)
	}
}

func TestTravelDailyLimitAndTraveling(t *testing.T) {
	f := &fakeTravelUpstream{state: "idle", limit: true}
	result, err := newTestRunner(f).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Action != "idle" || !strings.Contains(result.Message, "今日已完成") {
		t.Fatalf("limit result = %+v", result)
	}
	if f.departCalls != 0 {
		t.Fatal("must not depart when daily limit reached")
	}

	f = &fakeTravelUpstream{state: "traveling"}
	result, err = newTestRunner(f).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Action != "traveling" {
		t.Fatalf("traveling result = %+v", result)
	}
}

func TestTravelRestricted(t *testing.T) {
	f := &fakeTravelUpstream{restricted: true}
	result, err := newTestRunner(f).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || !result.Restricted {
		t.Fatalf("restricted result = %+v", result)
	}
}
