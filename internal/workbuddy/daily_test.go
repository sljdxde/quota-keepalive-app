package workbuddy

import (
	"strings"
	"testing"
	"time"
)

func TestDailyActivityEventMatchesBillingContract(t *testing.T) {
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	event := DailyActivityEventFor("uid-123", now)
	if event.EventCode != "chat_request_send" {
		t.Fatalf("eventCode = %q", event.EventCode)
	}
	if event.UserID != "uid-123" {
		t.Fatalf("userId = %q", event.UserID)
	}
	if event.Timestamp != now.UnixMilli() {
		t.Fatalf("timestamp = %d, want %d", event.Timestamp, now.UnixMilli())
	}
	if event.Mode != "craft" || event.AgentName != "default" ||
		event.AgentType != "conversation" ||
		event.RequestModelID != "deepseek-v4-flash" ||
		event.RequestModelName != "DeepSeek V4 Flash" || event.IsPlan || event.ReportDelay != 0 {
		t.Fatalf("event fields off-contract: %+v", event)
	}
	if event.ConversationID == "" || event.RequestID != event.ConversationID+"-req" {
		t.Fatalf("ids malformed: %+v", event)
	}
}

func TestDailyActivityHeadersCarryBillingIdentity(t *testing.T) {
	headers := DailyActivityHeaders("user", time.Now())
	if headers["origin"] != "https://www.codebuddy.cn" ||
		headers["referer"] != "https://www.codebuddy.cn/" {
		t.Fatalf("origin/referer = %q/%q", headers["origin"], headers["referer"])
	}
	if headers["x-codebuddy-request"] != "1" || headers["accept-language"] != "zh-CN" {
		t.Fatalf("billing headers = %+v", headers)
	}
	if headers["x-machine-id"] != "168ca8e9-dc26-4399-a0bb-6c4a36a024fc" {
		t.Fatalf("machine id = %q", headers["x-machine-id"])
	}
	if headers["x-session-id"] != "6dd2ce0f-e3ca-460e-af22-1403f0a46baf" {
		t.Fatalf("session id = %q", headers["x-session-id"])
	}
	if len(headers["x-request-id"]) != 32 || strings.Contains(headers["x-request-id"], "-") {
		t.Fatalf("request id = %q", headers["x-request-id"])
	}
	// 同一个账号必须永远来自同一台「设备」：两次调用指纹一致。
	again := DailyActivityHeaders("user", time.Now())
	if again["x-machine-id"] != headers["x-machine-id"] ||
		again["x-session-id"] != headers["x-session-id"] {
		t.Fatal("device fingerprint is not stable")
	}
	if DailyActivityHeaders("other", time.Now())["x-machine-id"] == headers["x-machine-id"] {
		t.Fatal("different accounts must get different machine ids")
	}
}

func TestParseDailyActivityResponse(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantOK    bool
		wantRestr bool
		wantAuth  bool
		wantRetry bool
		wantCode  int
		wantMessa bool
	}{
		{name: "success", status: 200, body: `{"code":0,"msg":"ok"}`, wantOK: true},
		{name: "empty body success", status: 200, body: "", wantOK: true},
		{name: "http 403", status: 403, body: `{"code":11140,"msg":"request illegal"}`, wantRestr: true, wantCode: 11140, wantMessa: true},
		{name: "envelope 11140", status: 200, body: `{"code":11140,"msg":"request illegal"}`, wantRestr: true, wantCode: 11140},
		{name: "http 401", status: 401, body: `{"code":401,"msg":"expired"}`, wantAuth: true},
		{name: "http 429", status: 429, body: `{"code":429,"msg":"slow down"}`, wantRetry: true},
		{name: "http 500", status: 500, body: "oops", wantRetry: true},
		{name: "other code", status: 200, body: `{"code":5,"msg":"bad"}`, wantRetry: true, wantCode: 5, wantMessa: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDailyActivityResponse(tc.status, []byte(tc.body))
			if got.OK != tc.wantOK || got.Restricted != tc.wantRestr ||
				got.Unauthorized != tc.wantAuth || got.Retryable != tc.wantRetry {
				t.Fatalf("result = %+v", got)
			}
			if tc.wantCode != 0 && got.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", got.Code, tc.wantCode)
			}
			if tc.wantMessa && strings.TrimSpace(got.Message) == "" {
				t.Fatal("expected upstream message to be kept")
			}
		})
	}
}
