package gui

import (
	"strconv"
	"testing"

	"qka/internal/tokenspeed"
)

// benchmarkSnapshot 造一份接近真实速度页的快照：9 个数据源 + 30 个会话 +
// 一份 13 级称号目录。tokenSpeedSignature 每 500-750ms 跑一次，这份夹具
// 用来量化它的序列化成本（也是 P1-2「换廉价指纹」的基线）。
func benchmarkSnapshot() tokenspeed.Snapshot {
	sources := make([]tokenspeed.SourceSnapshot, 0, 9)
	for i := 0; i < 9; i++ {
		sources = append(sources, tokenspeed.SourceSnapshot{
			Source:                "source-" + strconv.Itoa(i),
			Label:                 "Source " + strconv.Itoa(i),
			OutputTokensPerSecond: float64(i) * 12.5,
			InputTokensPerSecond:  float64(i) * 2.5,
			TotalTokensPerSecond:  float64(i) * 15,
			GeneratingAgents:      i % 3,
			TodayInputTokens:      int64(i) * 1000,
			TodayOutputTokens:     int64(i) * 5000,
			TodayTotalTokens:      int64(i) * 6000,
		})
	}
	sessions := make([]tokenspeed.SessionSnapshot, 0, 30)
	for i := 0; i < 30; i++ {
		sessions = append(sessions, tokenspeed.SessionSnapshot{
			ID:                    "session-" + strconv.Itoa(i),
			Label:                 "workspace/project-" + strconv.Itoa(i%7),
			Project:               "project-" + strconv.Itoa(i%7),
			Source:                "source-" + strconv.Itoa(i%9),
			SourceLabel:           "Source " + strconv.Itoa(i%9),
			Model:                 "model-" + strconv.Itoa(i%4),
			OutputTokensPerSecond: float64(i) * 3.25,
			InputTokensPerSecond:  float64(i) * 0.75,
			TotalTokensPerSecond:  float64(i) * 4,
			Generating:            i%5 == 0,
			LastActiveAt:          "2026-11-13T15:20:00+08:00",
		})
	}
	return tokenspeed.Snapshot{
		OutputTokensPerSecond: 123.4,
		InputTokensPerSecond:  23.4,
		TotalTokensPerSecond:  146.8,
		TodayTotalTokens:      987654,
		TodayTokenDate:        "2026-11-13",
		Active:                true,
		GeneratingAgents:      2,
		Agent:                 "codex",
		Title:                 "跑車",
		TitleTheme:            "vehicles",
		TitleThemeID:          "vehicles",
		TitleCatalog: []tokenspeed.TitleThemeDefinition{{
			ID: "vehicles", Label: "交通工具", Idle: "步行",
			Thresholds: []float64{0, 0.02, 0.03, 0.04, 0.06, 0.08, 0.12, 0.17, 0.24, 0.34, 0.5, 0.7, 1},
			Titles:     []string{"步行", "自行車", "電動自行車", "摩托車", "汽車", "跑車", "高鐵", "賽車", "磁浮列車", "噴射客機", "戰鬥機", "極音速飛機", "火箭"},
		}},
		Sources:                  sources,
		Sessions:                 sessions,
		IdleSourceCount:          3,
		UpdatedAt:                "2026-11-13T15:20:00+08:00",
		PeakTodayTokensPerSecond: 512.5,
		EstimatedShare:           0.32,
	}
}

// TestTokenSpeedSignatureIgnoresOnlyUpdatedAt 守住「每轮 UpdatedAt 必变、
// 但不能因此每轮都广播」的去重前提：只改 UpdatedAt 时指纹必须不变。
func TestTokenSpeedSignatureIgnoresOnlyUpdatedAt(t *testing.T) {
	a := benchmarkSnapshot()
	b := benchmarkSnapshot()
	b.UpdatedAt = "2026-11-13T15:20:01+08:00"
	if tokenSpeedSignature(a) != tokenSpeedSignature(b) {
		t.Fatal("UpdatedAt 变化不应改变内容指纹")
	}

	c := benchmarkSnapshot()
	c.OutputTokensPerSecond = 999
	if tokenSpeedSignature(a) == tokenSpeedSignature(c) {
		t.Fatal("展示字段变化必须改变内容指纹，否则前端收不到刷新")
	}
}

// BenchmarkTokenSpeedSignature 量化「每 500-750ms 一次全量 json.Marshal」的
// 成本，作为后续改廉价指纹时的对照基线。
func BenchmarkTokenSpeedSignature(b *testing.B) {
	snapshot := benchmarkSnapshot()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tokenSpeedSignature(snapshot)
	}
}
