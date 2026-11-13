package tokenspeed

import "time"

// 实时速率的滑动窗口与聚合方式，取自 agent-speed 的 spec/METRICS.md §6.3：
//
//	v = Σ（每条响应落在窗口内的 token ÷ 窗口秒数）
//
// 已完成响应按「其流式区间与窗口的重叠时长 × 该响应的实测速率」折算，
// 生成中的会话按「参考速率 × 已在飞时长」估算。这些纯函数只负责窗口内的
// 贡献；computeSnapshot 会再把结果与「最后一次响应结束时刻的窗口值」取大，
// 因此最终快照不会在两次响应之间掉到 0（见 Snapshot 的说明）。
const (
	// liveRateWindow 是实时速率的滑动窗口长度。
	liveRateWindow = 3 * time.Second
	// minResponseDuration 是实测区间的下限：更短的响应按它计，避免一条
	// 几十毫秒的响应算出天文数字。
	minResponseDuration = 500 * time.Millisecond
	// peakEventCooldown 是「破纪录」事件的最小间隔：庆祝不刷屏。
	peakEventCooldown = 60 * time.Second
	// peakEventMinimum 是破纪录的最低速率：空闲基线太低时不值得庆祝。
	peakEventMinimum = 20.0
	// peakEventMinimumGrowth 是新峰值相对旧峰值至少高出的速率。
	peakEventMinimumGrowth = 1.0
	// responseEventWindow 是「响应完成」事件的有效期：更早的补读不算实时完成。
	responseEventWindow = 120 * time.Second
)

// windowOverlapMS 返回 [startMS, endMS] 与 [fromMS, toMS] 的重叠毫秒数。
func windowOverlapMS(startMS, endMS, fromMS, toMS int64) int64 {
	if endMS <= startMS || toMS <= fromMS {
		return 0
	}
	start := maxInt64(startMS, fromMS)
	end := toMS
	if endMS < end {
		end = endMS
	}
	if end <= start {
		return 0
	}
	return end - start
}

// widenedInterval 返回一条响应用于速率折算的区间：时长不足下限时补足，
// 语义与 METRICS §3.3 的「最短 500ms」一致。
func widenedInterval(startMS, endMS int64) (int64, int64) {
	if endMS <= startMS {
		return startMS, endMS
	}
	minimum := minResponseDuration.Milliseconds()
	if endMS-startMS < minimum {
		startMS = endMS - minimum
	}
	return startMS, endMS
}

// measuredWindowTokens 计算一条已完成响应落在窗口内的输出 token 数：
// token 在该响应的实测区间内均匀分布，按重叠比例折算。
//
// 注意必须用「实测区间」（解码窗口）而不是整条响应的起止：思考/首字等待
// 不计入速率，若用整段区间会把 token 摊薄到不产生 token 的时间里。
func measuredWindowTokens(endMS, windowMS, tokens, fromMS, toMS int64) float64 {
	if tokens <= 0 || windowMS <= 0 {
		return 0
	}
	// 不足下限的区间整体拉宽到 500ms：token 与分母一起拉宽，速率不变。
	minimum := minResponseDuration.Milliseconds()
	if windowMS < minimum {
		windowMS = minimum
	}
	startMS, stopMS := widenedInterval(endMS-windowMS, endMS)
	overlap := windowOverlapMS(startMS, stopMS, fromMS, toMS)
	if overlap <= 0 {
		return 0
	}
	return float64(tokens) * float64(overlap) / float64(windowMS)
}

// inputWindowTokens 计算输入 token 落在窗口内的部分：输入 token 属于整段
// 请求时长，按整段区间折算（与 METRICS 的 total 口径一致）。
func inputWindowTokens(startMS, endMS, tokens, fromMS, toMS int64) float64 {
	if tokens <= 0 || endMS <= startMS {
		return 0
	}
	overlap := windowOverlapMS(startMS, endMS, fromMS, toMS)
	if overlap <= 0 {
		return 0
	}
	return float64(tokens) * float64(overlap) / float64(endMS-startMS)
}

// estimateWindowContribution 计算一个在飞会话在窗口内的估算贡献：
// 参考速率 × 已生成时长（截到窗口内）。
func estimateWindowContribution(estimateStartMS, nowMS, fromMS int64, rate float64) float64 {
	if rate <= 0 || estimateStartMS <= 0 {
		return 0
	}
	overlap := windowOverlapMS(estimateStartMS, nowMS, fromMS, nowMS)
	if overlap <= 0 {
		return 0
	}
	return rate * float64(overlap) / 1000
}

// liveRateFromContributions 把窗口内的 token 贡献折算回 tok/s。
func liveRateFromContributions(tokens float64, window time.Duration) float64 {
	seconds := window.Seconds()
	if seconds <= 0 || tokens <= 0 {
		return 0
	}
	return tokens / seconds
}
