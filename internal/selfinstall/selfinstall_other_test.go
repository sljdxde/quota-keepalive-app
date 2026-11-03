//go:build !windows

package selfinstall

import (
	"os"
	"path/filepath"
	"testing"
)

// 非 Windows 上 Ensure 是纯 no-op：不建目录、不写状态文件、不返回错误。
func TestEnsureIsNoOpOffWindows(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	if err := Ensure(configDir, "/Applications/QKA.app/Contents/MacOS/qka", "1.2.3"); err != nil {
		t.Fatalf("非 Windows 的 Ensure 应为 no-op，实际 %v", err)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("no-op 不应创建配置目录，stat 错误 = %v", err)
	}
}
