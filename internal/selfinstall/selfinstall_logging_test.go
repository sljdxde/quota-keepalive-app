package selfinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/applog"
)

// TestEnsureLogsOutcome 验证自安装结果（开始/创建/完成）真的落到 applog。
// 未初始化日志时这些调用静默，所以既有用例不受影响。
func TestEnsureLogsOutcome(t *testing.T) {
	t.Setenv("QKA_LOG_LEVEL", "debug")
	logRoot := t.TempDir()
	closeLogs, err := applog.Init(logRoot, "test")
	if err != nil {
		t.Fatalf("applog.Init: %v", err)
	}
	defer func() { _ = closeLogs() }()

	fake, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if fake.repairs != 1 {
		t.Fatalf("应创建一次快捷方式，实际 %d", fake.repairs)
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
		"开始自安装",
		"已注册 App Paths",
		"已创建开始菜单快捷方式",
		"自安装完成 version=1.2.3",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("日志应包含 %q，实际：\n%s", want, data)
		}
	}
}
