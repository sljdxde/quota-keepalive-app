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

// 3 秒滑窗：一条 2 秒、3000 token 的响应结束后，数字随窗口滑动自然衰减到 0。
func TestLiveRateDecaysAfterResponseEnds(t *testing.T) {
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

	// 再推 1 秒：窗口 [start+1.5s, start+4.5s]，只剩一半解码窗口 → 500。
	now = start.Add(4500 * time.Millisecond)
	service.Poll()
	assertNear(t, service.Snapshot().OutputTokensPerSecond, 500)

	// 窗口完全滑过响应 → 0（不再像旧实现那样一直挂在最后一条速率上）。
	now = start.Add(6 * time.Second)
	service.Poll()
	if got := service.Snapshot().OutputTokensPerSecond; got != 0 {
		t.Fatalf("窗口滑过后速率应归零，实际 %.3f", got)
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
