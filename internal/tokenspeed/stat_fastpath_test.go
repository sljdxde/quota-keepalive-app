package tokenspeed

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestNonWalkPollSkipsOpenWhenFileUnchanged 确认目录不重扫的轮次里，文件
// size/mtime 未变化时不再 open；追加内容后仍能正常增量读取。
func TestNonWalkPollSkipsOpenWhenFileUnchanged(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "session.jsonl")
	line := `{"type":"assistant","timestamp":"2026-02-03T04:05:06Z","message":{"usage":{"output_tokens":5}}}`
	if err := os.WriteFile(file, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var opens int32
	restoreOpen(t, func(path string) (transcriptFile, error) {
		atomic.AddInt32(&opens, 1)
		return os.Open(path)
	})

	clock := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	service := New(Config{
		Roots:        []Root{{Source: "test", Path: root}},
		PollInterval: time.Second,
		WalkInterval: 30 * time.Second,
		Now:          func() time.Time { return clock },
	})
	service.Poll()
	if got := atomic.LoadInt32(&opens); got != 1 {
		t.Fatalf("首轮应打开 1 次，实际 %d 次", got)
	}
	// 第二轮不重扫目录；文件没有任何变化，必须靠 stat 快路径跳过 open。
	service.Poll()
	if got := atomic.LoadInt32(&opens); got != 1 {
		t.Fatalf("文件未变化时不应再次 open，实际累计 %d 次", got)
	}

	// 追加内容后必须重新打开并推进 offset，证明快路径没有吞掉增量。
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
	if got := atomic.LoadInt32(&opens); got != 2 {
		t.Fatalf("文件增长后应重新 open，实际累计 %d 次", got)
	}
	if state := service.files[file]; state == nil || state.offset == 0 {
		t.Fatalf("文件增长后应推进读取游标: %+v", state)
	}
	service.Poll()
	if got := atomic.LoadInt32(&opens); got != 2 {
		t.Fatalf("读完增量后再次未变化不应 open，实际累计 %d 次", got)
	}
}

// TestDSHNonWalkPollSkipsOpenWhenFileUnchanged 确认 DSH 非重扫轮次同样先
// stat：压缩会话未变化时不 open，追加新 frame 后仍能重新解压。
func TestDSHNonWalkPollSkipsOpenWhenFileUnchanged(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	path := dshSessionPath(root, "skip-open")
	dshWriteFile(t, path, dshFrame(t, dshAssistantEvent("s1", now, 1, 1, 2, 0)))

	var opens int32
	restoreOpen(t, func(path string) (transcriptFile, error) {
		atomic.AddInt32(&opens, 1)
		return os.Open(path)
	})

	service := New(Config{
		Roots:        []Root{{Source: "dsh", Path: root}},
		WalkInterval: 30 * time.Second,
		Now:          func() time.Time { return now },
	})
	service.Poll()
	if got := atomic.LoadInt32(&opens); got != 1 {
		t.Fatalf("首轮应打开 1 次，实际 %d 次", got)
	}
	service.Poll()
	if got := atomic.LoadInt32(&opens); got != 1 {
		t.Fatalf("DSH 文件未变化时不应再次 open，实际累计 %d 次", got)
	}

	dshAppendFile(t, path, dshFrame(t, dshAssistantEvent("s2", now.Add(time.Second), 1, 1, 2, 0)))
	if err := os.Chtimes(path, now.Add(2*time.Second), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	service.Poll()
	if got := atomic.LoadInt32(&opens); got != 2 {
		t.Fatalf("DSH 文件增长后应重新 open，实际累计 %d 次", got)
	}
	if got := service.Snapshot().TodayTotalTokens; got != 4 {
		t.Fatalf("追加 frame 后 today = %d, want 4", got)
	}
}
