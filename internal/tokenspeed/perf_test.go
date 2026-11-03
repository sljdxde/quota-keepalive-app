package tokenspeed

import (
	"strings"
	"testing"
	"time"
)

// 750ms 轮询一次，单次超过 150ms 就值得记一条 perf 告警：界面刷新会被拖慢。
func TestSlowPollWarning(t *testing.T) {
	if _, ok := slowPollWarning(10 * time.Millisecond); ok {
		t.Fatal("10ms 不应告警")
	}
	if _, ok := slowPollWarning(slowPollThreshold); ok {
		t.Fatal("恰好等于阈值不应告警")
	}
	message, ok := slowPollWarning(200 * time.Millisecond)
	if !ok {
		t.Fatal("200ms 超过 150ms 阈值，应告警")
	}
	if !strings.Contains(message, "200ms") {
		t.Fatalf("告警应包含实测耗时，实际 %q", message)
	}
	if !strings.Contains(message, "150ms") {
		t.Fatalf("告警应包含阈值，实际 %q", message)
	}
	if !strings.Contains(message, "轮询") {
		t.Fatalf("告警应说明是轮询耗时，实际 %q", message)
	}
	if slowPollThreshold != 150*time.Millisecond {
		t.Fatalf("阈值应为 150ms，实际 %v", slowPollThreshold)
	}
}
