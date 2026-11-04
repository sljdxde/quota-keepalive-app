package app

import (
	"context"
	"testing"
	"time"

	"qka/internal/gui"
	"qka/internal/store"
)

func TestPruneLogsEnforcesFortyEightHourWindow(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		ts      string
		message string
	}{
		{now.Add(-49 * time.Hour).Format(time.RFC3339), "expired"},
		{now.Add(-47 * time.Hour).Format(time.RFC3339), "recent"},
	} {
		if _, err := st.DB().Exec(`
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, 'fixture', 'a1', 'checkin', 'completed', 'info', ?)`,
			item.ts, item.message); err != nil {
			t.Fatal(err)
		}
	}

	a := &App{store: st, now: func() time.Time { return now }}
	a.pruneLogs(context.Background())

	var count int
	var message string
	if err := st.DB().QueryRow(`SELECT COUNT(*), MIN(message) FROM logs`).Scan(&count, &message); err != nil {
		t.Fatal(err)
	}
	if count != 1 || message != "recent" {
		t.Fatalf("48 小时滚动清理结果 count=%d message=%q", count, message)
	}
}

func TestLogPrunerLifecycleDefensivePathsAndTicker(t *testing.T) {
	var nilApp *App
	nilApp.startLogPruner()
	nilApp.stopLogPruner()
	nilApp.pruneLogs(context.Background())
	(&App{}).startLogPruner()
	(&App{}).stopLogPruner()
	(&App{}).pruneLogs(context.Background())

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().Exec(`
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, 'fixture', 'a1', 'checkin', 'completed', 'info', 'expired by ticker')`,
		now.Add(-49*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	app := &App{store: st, now: func() time.Time { return now }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go app.runLogPruner(ctx, done, 5*time.Millisecond)

	deadline := time.After(time.Second)
	for {
		var count int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("periodic pruning did not remove the expired row")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("log pruner did not stop after cancellation")
	}
}

// TestLogsBindingDoesNotPruneOnEachCall 验证 logs 绑定不再每次翻页触发同步
// DELETE：过期行只由 startLogPruner 的定时任务清理，翻页后应原样保留。
func TestLogsBindingDoesNotPruneOnEachCall(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	state := &guiState{app: &App{store: st, now: func() time.Time { return now }}}
	if _, err := st.DB().Exec(`
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, 'fixture', 'a1', 'checkin', 'completed', 'info', 'expired beyond 48h')`,
		now.Add(-49*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if _, err := state.logs(context.Background(), gui.LogFilter{}, 10, 0); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("翻页不应触发 prune：过期行仍被删除，剩余 count=%d", count)
	}
}
