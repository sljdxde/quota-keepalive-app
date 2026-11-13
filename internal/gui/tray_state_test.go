package gui

import (
	"os"
	"path/filepath"
	"testing"

	"qka/internal/applog"
)

func TestTrayStateRoundTrip(t *testing.T) {
	// applog 会一直持有日志文件句柄，不能用 t.TempDir（清理会失败）。
	dir, err := os.MkdirTemp("", "qka-tray-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	closeLogs, err := applog.Init(dir, "test")
	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	defer closeLogs()

	if got := loadTrayState(); got != (trayState{}) {
		t.Fatalf("没有状态文件时应返回零值，实际 %+v", got)
	}
	saveTrayState(trayState{Pet: "pikachu", Caught: true})
	if got := loadTrayState(); got.Pet != "pikachu" || !got.Caught {
		t.Fatalf("状态未正确落盘：%+v", got)
	}
	path := filepath.Join(dir, trayStateFile)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("状态文件应存在且为 0600：info=%v err=%v", info, err)
	}
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadTrayState(); got != (trayState{}) {
		t.Fatalf("损坏的状态文件应退回零值，实际 %+v", got)
	}
}
