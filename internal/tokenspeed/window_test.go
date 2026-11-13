package tokenspeed

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

// 滑窗折算的纯函数：区间重叠、按比例分摊 token、短响应钳到 500ms。
func TestWindowOverlapAndTokens(t *testing.T) {
	if got := windowOverlapMS(0, 1000, 500, 2000); got != 500 {
		t.Fatalf("重叠应为 500ms，实际 %d", got)
	}
	if got := windowOverlapMS(0, 1000, 2000, 3000); got != 0 {
		t.Fatalf("无交集应为 0，实际 %d", got)
	}
	if got := windowOverlapMS(1000, 1000, 0, 2000); got != 0 {
		t.Fatalf("零长度区间应为 0，实际 %d", got)
	}

	// 解码窗口 [8000, 10000]（2s，1000 token），窗口 [9000, 12000]：
	// 只有后一半落在窗口内 → 500 token。
	if got := measuredWindowTokens(10000, 2000, 1000, 9000, 12000); math.Abs(got-500) > 0.01 {
		t.Fatalf("相交代折算 500 token，实际 %.3f", got)
	}
	// 整段落在窗口内 → 全部 token。
	if got := measuredWindowTokens(10000, 2000, 1000, 8000, 12000); math.Abs(got-1000) > 0.01 {
		t.Fatalf("全交应折算全部 token，实际 %.3f", got)
	}
	// 窗口完全在响应之后 → 0。
	if got := measuredWindowTokens(10000, 2000, 1000, 11000, 12000); got != 0 {
		t.Fatalf("窗口在响应之后应为 0，实际 %.3f", got)
	}
	// 短于 500ms 的区间按下限拉宽：token 与分母一起拉宽，贡献仍是 100
	// （不会把 100ms 的 100 token 当成 1000 tok/s）。
	if got := measuredWindowTokens(10000, 100, 100, 8000, 12000); math.Abs(got-100) > 0.01 {
		t.Fatalf("短区间应按 500ms 计并折算全部 token，实际 %.3f", got)
	}
}

// 一条 2 秒、3000 token 的响应结束后，速率保留最后一次实测窗口值，不再
// 随实时窗口滑过而掉到 0；只有超过 staleAfter 没有活动才归零。
func TestRateHoldsAfterResponseEnds(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	writeLines(t, path,
		map[string]any{
			"timestamp": ts(start),
			"type":      "turn_context",
			"payload":   map[string]any{"model": "gpt-5.5"},
		},
		map[string]any{
			"timestamp": ts(start.Add(time.Second)),
			"type":      "response_item",
			"payload":   map[string]any{"type": "reasoning", "role": "assistant"},
		},
		map[string]any{
			"timestamp": ts(start.Add(2 * time.Second)),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": "resp-decay",
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 3000,
					"total_tokens":  3000,
				},
			},
		},
	)

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "codex", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()
	// 解码窗口 [start+1s, start+2s] = 1s、3000 token，完整落在 3s 窗口内
	// → 3000 / 3s = 1000 tok/s。
	assertNear(t, service.Snapshot().OutputTokensPerSecond, 1000)

	// 向前推 1 秒：窗口 [start+0.5s, start+3.5s]，解码窗口仍完整在内 → 不变。
	now = start.Add(3500 * time.Millisecond)
	service.Poll()
	assertNear(t, service.Snapshot().OutputTokensPerSecond, 1000)

	// 再推 1 秒：实时窗口 [start+1.5s, start+4.5s] 只剩一半解码窗口，
	// 实时值是 500；但保留值仍是响应结束时刻的 1000（两者取大），所以
	// 界面不会在两次响应之间衰减到 0。
	now = start.Add(4500 * time.Millisecond)
	service.Poll()
	assertNear(t, service.Snapshot().OutputTokensPerSecond, 1000)

	// 实时窗口完全滑过响应后，保留值继续生效：显示 1000（3 秒分母的口径），
	// 而不是响应自身的 3000 tok/s，也不是 0。
	now = start.Add(6 * time.Second)
	service.Poll()
	assertNear(t, service.Snapshot().OutputTokensPerSecond, 1000)

	// 普通闲置只保留 liveWindow（15 秒），之后要尽快回落，数字才会「跟手」。
	now = start.Add(2*time.Second + liveWindow + time.Second)
	service.Poll()
	if got := service.Snapshot().OutputTokensPerSecond; got != 0 {
		t.Fatalf("闲置超过 15 秒后速率应归零，实际 %.3f", got)
	}
}

// 今日峰值与两个事件序号：峰值只升不降；破纪录事件按冷却去抖；响应完成
// 每条只计一次（界面靠序号播放冲击波）。
func TestPeakAndEventSequences(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	// 两条响应：先 1000 tok/s 的一条，再在更晚时间补一条更快的。
	writeLines(t, path,
		map[string]any{
			"timestamp": ts(start),
			"type":      "turn_context",
			"payload":   map[string]any{"model": "gpt-5.5"},
		},
		map[string]any{
			"timestamp": ts(start.Add(time.Second)),
			"type":      "response_item",
			"payload":   map[string]any{"type": "reasoning", "role": "assistant"},
		},
		map[string]any{
			"timestamp": ts(start.Add(2 * time.Second)),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": "resp-peak-1",
				"usage":       map[string]any{"output_tokens": 3000, "total_tokens": 3000},
			},
		},
	)
	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "codex", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()
	first := service.Snapshot()
	if first.OutputTokensPerSecond <= 0 {
		t.Fatalf("首条响应应有速率，实际 %.3f", first.OutputTokensPerSecond)
	}
	if math.Abs(first.PeakTodayTokensPerSecond-first.OutputTokensPerSecond) > 0.01 {
		t.Fatalf("峰值应等于当前速率：peak=%.3f rate=%.3f",
			first.PeakTodayTokensPerSecond, first.OutputTokensPerSecond)
	}
	if first.ResponseSeq != 1 {
		t.Fatalf("一条完成响应应使序号为 1，实际 %d", first.ResponseSeq)
	}
	peakSeqAfterFirst := first.PeakSeq

	// 同一轮再 Poll：没有新响应，序号不得重复递增。
	service.Poll()
	again := service.Snapshot()
	if again.ResponseSeq != 1 || again.PeakSeq != peakSeqAfterFirst {
		t.Fatalf("重复轮询不应递增事件序号: resp=%d peak=%d", again.ResponseSeq, again.PeakSeq)
	}
	if again.PeakTodayTokensPerSecond < first.PeakTodayTokensPerSecond {
		t.Fatalf("峰值只升不降: %+v", again)
	}
}

// 长 step 空窗时保留的速率必须与实时窗口同口径：同样按 3 秒分母折算，
// 否则从「实时」切到「保留」会突然跳成响应自身的瞬时速度。
func TestHeldLiveRateMatchesLiveWindow(t *testing.T) {
	const (
		endMS     = int64(100_000)
		decodeMS  = int64(1_000)
		outTokens = int64(3_000)
	)
	record := responseRecord{
		agentID:        "codex:agent",
		source:         "codex",
		model:          "gpt-5.5",
		startMS:        endMS - decodeMS,
		hasStart:       true,
		decodeStartMS:  endMS - decodeMS,
		hasDecodeStart: true,
		endMS:          endMS,
		final:          true,
		usage:          usage{output: outTokens},
	}
	measured := []timedResponse{{
		responseRecord: record,
		startMS:        endMS - decodeMS,
		durationMS:     decodeMS,
	}}
	// 解码窗口 [end-1s, end] 完整落在 [end-3s, end] 内 → 3000 token / 3s。
	output, total, ok := heldLiveRate(measured, record.agentID, endMS)
	if !ok {
		t.Fatal("最近有实测响应时应能保留速率")
	}
	assertNear(t, output, 1000)
	assertNear(t, total, 1000)

	// 别的 agent 没有可保留的响应。
	if _, _, ok := heldLiveRate(measured, "codex:other", endMS); ok {
		t.Fatal("没有实测响应的 agent 不应保留速率")
	}
	// 最后一刻过去太久（窗口完全不重叠）同样没有可保留值。
	if _, _, ok := heldLiveRate(measured, record.agentID, endMS+10_000); ok {
		t.Fatal("窗口完全过去后不应再保留速率")
	}
}
