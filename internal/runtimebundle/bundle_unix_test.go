//go:build !windows

package runtimebundle

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtractedFilesKeepPermissions 校验解压在 Unix 上保留可执行位：
// bun 与带可执行位的脚本必须可执行，普通文件不能被顺手放宽。
func TestExtractedFilesKeepPermissions(t *testing.T) {
	configDir := t.TempDir()
	paths, err := ensureWith(configDir, "1.0.0", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path       string
		executable bool
	}{
		{paths.Bun, true},
		{filepath.Join(paths.AdaptersRoot, "run.sh"), true},
		{filepath.Join(paths.AdaptersRoot, "qoder", "index.mjs"), false},
	}
	for _, tc := range cases {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		isExec := info.Mode().Perm()&0o111 != 0
		if isExec != tc.executable {
			t.Fatalf("%s 可执行位 = %v，want %v（%v）", tc.path, isExec, tc.executable, info.Mode())
		}
	}
}
