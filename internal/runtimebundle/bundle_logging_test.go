package runtimebundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/applog"
)

// TestEnsureLogsProgress 验证解压进度真的落到 applog：开始/完成是 info，
// 命中缓存是 debug（打开 debug 级别才可见）。未初始化日志时这些调用静默，
// 所以既有用例不受影响。
func TestEnsureLogsProgress(t *testing.T) {
	t.Setenv("QKA_LOG_LEVEL", "debug")
	logRoot := t.TempDir()
	closeLogs, err := applog.Init(logRoot, "test")
	if err != nil {
		t.Fatalf("applog.Init: %v", err)
	}
	defer func() { _ = closeLogs() }()

	configDir := t.TempDir()
	if _, err := ensureWith(configDir, "1.0.0", standardAssets(t)); err != nil {
		t.Fatalf("首次解压: %v", err)
	}
	if _, err := ensureWith(configDir, "1.0.0", standardAssets(t)); err != nil {
		t.Fatalf("命中缓存: %v", err)
	}
	if err := closeLogs(); err != nil {
		t.Fatalf("关闭日志: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(logRoot, "logs", "qka-*.log"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("应生成日志文件，实际 %v（err=%v）", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}
	for _, want := range []string{
		"开始解压内嵌运行时",
		"内嵌运行时解压完成",
		"耗时=",
		"命中已解压运行时",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("日志应包含 %q，实际：\n%s", want, data)
		}
	}
}
