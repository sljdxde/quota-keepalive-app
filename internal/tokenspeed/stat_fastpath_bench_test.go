package tokenspeed

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// benchTranscriptLine 是一条能计入 today 的 assistant 记录，用于造多个
// 「已读且不再变化」的会话文件。
const benchTranscriptLine = `{"type":"assistant","timestamp":"2026-02-03T04:05:06Z","message":{"usage":{"output_tokens":5}}}`

// BenchmarkDiscoverAndReadUnchangedFiles 衡量「空闲轮次」的目录扫描成本：
// 50 个已知会话文件全部未变化时，stat 快路径应避免 open 与解析。关注
// ns/op 与 allocs/op，用来防回归（改动前每轮会 open 全部 50 个文件）。
func BenchmarkDiscoverAndReadUnchangedFiles(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 50; i++ {
		name := filepath.Join(root, fmt.Sprintf("session-%02d.jsonl", i))
		if err := os.WriteFile(name, []byte(benchTranscriptLine+"\n"), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	service := New(Config{
		Roots:        []Root{{Source: "bench", Path: root}},
		PollInterval: time.Second,
		WalkInterval: time.Hour,
		Now:          func() time.Time { return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC) },
	})
	// 首轮建立 files 表并记录 size/modNS；之后每轮都走非重扫快路径。
	service.discoverAndRead(b.Context(), service.cfg.Now())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.discoverAndRead(b.Context(), service.cfg.Now())
	}
}

// BenchmarkPollUnchangedFiles 是完整轮询（含快照计算与裁剪）的端到端成本，
// 更贴近真实空闲态：2 秒一次的定时器每次都会跑这一套。
func BenchmarkPollUnchangedFiles(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 50; i++ {
		name := filepath.Join(root, fmt.Sprintf("session-%02d.jsonl", i))
		if err := os.WriteFile(name, []byte(benchTranscriptLine+"\n"), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	service := New(Config{
		Roots:        []Root{{Source: "bench", Path: root}},
		PollInterval: time.Second,
		WalkInterval: time.Hour,
		Now:          func() time.Time { return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC) },
	})
	service.Poll()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.Poll()
	}
}
