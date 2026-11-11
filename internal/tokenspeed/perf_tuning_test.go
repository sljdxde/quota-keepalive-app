package tokenspeed

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNextPollIntervalAdaptsToActivity 确认有会话生成时才用快速间隔。
func TestNextPollIntervalAdaptsToActivity(t *testing.T) {
	service := New(Config{PollInterval: 750 * time.Millisecond, IdlePollInterval: 2 * time.Second})
	if got := service.nextPollInterval(); got != 2*time.Second {
		t.Fatalf("空闲间隔 = %v，want 2s", got)
	}

	service.snapshotMu.Lock()
	service.snapshot.Active = true
	service.snapshotMu.Unlock()
	if got := service.nextPollInterval(); got != 750*time.Millisecond {
		t.Fatalf("生成中的间隔 = %v，want 750ms", got)
	}

	service.snapshotMu.Lock()
	service.snapshot.Active = false
	service.snapshot.GeneratingAgents = 2
	service.snapshotMu.Unlock()
	if got := service.nextPollInterval(); got != 750*time.Millisecond {
		t.Fatalf("有待生成会话的间隔 = %v，want 750ms", got)
	}

	// 没配置空闲间隔时沿用 PollInterval，保持旧行为。
	legacy := New(Config{PollInterval: time.Second})
	if got := legacy.nextPollInterval(); got != time.Second {
		t.Fatalf("未配置空闲间隔时应沿用 PollInterval，实际 %v", got)
	}
}

// TestWalkThrottleKeepsReadingKnownFiles 确认节流期间不再重扫目录，但已知文件
// 的新增内容仍然会被读到（否则速度会停更）。
func TestWalkThrottleKeepsReadingKnownFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "session.jsonl")
	line := `{"type":"assistant","timestamp":"2026-02-03T04:05:06Z","message":{"usage":{"output_tokens":5}}}`
	if err := os.WriteFile(file, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	clock := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	service := New(Config{
		Roots:        []Root{{Source: "test", Path: root}},
		PollInterval: time.Second,
		WalkInterval: 30 * time.Second,
		Now:          func() time.Time { return clock },
	})
	service.Poll()
	if len(service.files) != 1 {
		t.Fatalf("首轮应发现 1 个会话文件，实际 %d", len(service.files))
	}
	walkedAt := service.lastWalk
	if walkedAt.IsZero() {
		t.Fatal("首轮应记录目录扫描时间")
	}

	// 追加内容后再轮询：walk 未到期，必须靠增量读取拿到新数据。
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	service.Poll()
	state := service.files[file]
	if state == nil {
		t.Fatal("节流期间文件状态不应丢失")
	}
	if state.offset == 0 {
		t.Fatal("节流期间仍应增量读取已知文件")
	}
	if !service.lastWalk.Equal(walkedAt) {
		t.Fatal("未到期时不应重扫目录（lastWalk 不该前移）")
	}
}

// TestDSHWalkThrottleRefreshesKnownSessions 确认 DSH 源同样节流且继续增量解压。
func TestDSHWalkThrottleRefreshesKnownSessions(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "session-abc", "session.v4.jsonl.zstd")
	dshWriteFile(t, first, dshFrame(t, `{"type":"session","data":{"id":"abc"}}`))

	clock := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	service := New(Config{
		Roots:        []Root{{Source: "dsh", Path: root}},
		WalkInterval: 30 * time.Second,
		Now:          func() time.Time { return clock },
	})
	service.Poll()
	if len(service.dsh) != 1 {
		t.Fatalf("首轮应发现 1 个 DSH 会话，实际 %d", len(service.dsh))
	}
	firstWalk := service.lastDSHWalk
	if firstWalk.IsZero() {
		t.Fatal("首轮应记录 DSH 扫描时间")
	}
	service.Poll()
	if !service.lastDSHWalk.Equal(firstWalk) {
		t.Fatal("未到期时不应重扫 DSH 目录")
	}
	if len(service.dsh) != 1 {
		t.Fatalf("节流期间不应丢弃已知会话，实际 %d", len(service.dsh))
	}

	// 时钟前进超过 WalkInterval 后应重新扫描：新建会话能立刻出现。
	clock = clock.Add(31 * time.Second)
	dshWriteFile(t, filepath.Join(root, "session-def", "session.v4.jsonl.zstd"),
		dshFrame(t, `{"type":"session","data":{"id":"def"}}`))
	service.Poll()
	if len(service.dsh) != 2 {
		t.Fatalf("重扫后应发现 2 个 DSH 会话，实际 %d", len(service.dsh))
	}
}

// TestWalkDueAlwaysTrueWithoutInterval 确认未配置 WalkInterval 时每轮都重扫：
// 测试与一次性工具依赖「新建文件下一轮就可见」。
func TestWalkDueAlwaysTrueWithoutInterval(t *testing.T) {
	now := time.Now()
	service := New(Config{})
	if !service.walkDue(now) || !service.walkDue(now) {
		t.Fatal("未配置 WalkInterval 时应每轮重扫")
	}
	if !service.dshWalkDue(now) || !service.dshWalkDue(now) {
		t.Fatal("未配置 WalkInterval 时 DSH 也应每轮重扫")
	}
}

// TestDefaultConfigSetsPerformanceWindows 固定出厂默认：空闲 2s、目录 3s。
func TestDefaultConfigSetsPerformanceWindows(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.IdlePollInterval != 2*time.Second {
		t.Fatalf("默认空闲间隔 = %v，want 2s", cfg.IdlePollInterval)
	}
	if cfg.WalkInterval != 3*time.Second {
		t.Fatalf("默认目录扫描间隔 = %v，want 3s", cfg.WalkInterval)
	}
}

// TestSetPollIntervalTakesEffectAtRuntime 确认设置页能在运行中改采样频率：
// 改完下一次调度就用新值，非法值不会把轮询打成忙等。
func TestSetPollIntervalTakesEffectAtRuntime(t *testing.T) {
	service := New(Config{PollInterval: 750 * time.Millisecond, IdlePollInterval: 2 * time.Second})
	if got := service.PollInterval(); got != 750*time.Millisecond {
		t.Fatalf("初始间隔 = %v，want 750ms", got)
	}

	service.SetPollInterval(200 * time.Millisecond)
	if got := service.PollInterval(); got != 200*time.Millisecond {
		t.Fatalf("调整后间隔 = %v，want 200ms", got)
	}

	// 活跃状态下 nextPollInterval 必须返回新值（否则设置项等于没生效）。
	service.snapshotMu.Lock()
	service.snapshot.Active = true
	service.snapshotMu.Unlock()
	if got := service.nextPollInterval(); got != 200*time.Millisecond {
		t.Fatalf("活跃时的间隔 = %v，want 200ms", got)
	}

	// 空闲时仍用 IdlePollInterval，不被采样频率影响。
	service.snapshotMu.Lock()
	service.snapshot.Active = false
	service.snapshotMu.Unlock()
	if got := service.nextPollInterval(); got != 2*time.Second {
		t.Fatalf("空闲时的间隔 = %v，want 2s", got)
	}

	// 非正值被忽略，保留上一次的有效值。
	service.SetPollInterval(0)
	service.SetPollInterval(-time.Second)
	if got := service.PollInterval(); got != 200*time.Millisecond {
		t.Fatalf("非法值应被忽略，实际 %v", got)
	}
}
