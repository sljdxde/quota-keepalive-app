//go:build darwin

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinPruneOrphans(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	keepDir := "/Users/test/.qka"
	keepLabel := LaunchdLabel(keepDir)
	// 有 plist 的其它配置目录：保留。
	liveLabel := "com.qka.agent.11111111"
	if err := os.WriteFile(filepath.Join(agents, liveLabel+".plist"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleA := "com.qka.agent.22222222"
	staleB := "com.qka.agent.abcdef12"
	listed := strings.Join([]string{
		"-\t78\t" + staleA,
		"-\t0\t" + liveLabel,
		"-\t0\t" + keepLabel,
		"-\t0\t" + staleB,
		"-\t0\tcom.qka.agent",
		"-\t0\tunrelated.service",
	}, "\n") + "\n"
	var booted []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) > 0 && args[0] == "list" {
			return []byte(listed), nil
		}
		if name == "launchctl" && len(args) > 1 && args[0] == "bootout" {
			booted = append(booted, args[1])
			return nil, nil
		}
		return nil, nil
	}
	s := newDarwinScheduler(home, "501", run)
	removed, err := s.PruneOrphans(context.Background(), Spec{ConfigDir: keepDir})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("应清理 2 个无 plist 的陈旧任务，实际 %d（%v）", removed, booted)
	}
	want := map[string]bool{
		"gui/501/" + staleA: true,
		"gui/501/" + staleB: true,
	}
	if len(booted) != len(want) {
		t.Fatalf("应只 bootout 两个陈旧标签，实际 %v", booted)
	}
	for _, target := range booted {
		if !want[target] {
			t.Fatalf("bootout 了不该动的任务：%v（应保留 %s / %s / com.qka.agent）",
				booted, liveLabel, keepLabel)
		}
	}
}
