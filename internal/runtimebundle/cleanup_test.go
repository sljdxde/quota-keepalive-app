package runtimebundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qka/internal/applog"
)

// makeVersionDir 在 runtimeRoot 下造一个历史版本目录，带点内容以贴近真实副本。
func makeVersionDir(t *testing.T, runtimeRoot, name string) string {
	t.Helper()
	dir := filepath.Join(runtimeRoot, name)
	if err := os.MkdirAll(filepath.Join(dir, AdaptersDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bunExecutableName), []byte("old-bun"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assertDirExists 断言目录的存在性与预期一致。
func assertDirExists(t *testing.T, dir string, want bool) {
	t.Helper()
	_, err := os.Stat(dir)
	got := err == nil
	if got != want {
		t.Fatalf("目录 %s 存在=%v，want %v（err=%v）", dir, got, want, err)
	}
}

// TestCompareVersions 覆盖版本号比较的排序规则：数值比较、预发布小于正式版。
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.9", "0.1.10", -1},
		{"0.1.16", "0.1.23", -1},
		{"0.1.23", "0.1.16", 1},
		{"0.1.23", "0.1.23", 0},
		{"2.0.0", "10.0.0", -1},      // 按数值比较，不是字典序
		{"0.1.23", "0.1.23-beta", 1}, // 正式版大于预发布
		{"0.1.23-beta.2", "0.1.23-beta.10", -1},
		{"1.0", "1.0.0", -1},  // 段数少视为更小
		{"dev", "0.1.23", -1}, // 非数字段排最后，开发版最先被清
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestCleanupOldVersionsKeepsCurrentAndPrevious 验证多版本目录只保留
// 「当前 + 上一个版本」两份，其余删除。
func TestCleanupOldVersionsKeepsCurrentAndPrevious(t *testing.T) {
	cases := []struct {
		name       string
		dirs       []string
		keep       string
		wantKeep   []string
		wantRemove []string
	}{
		{
			name:       "常规升级链只保留最近两版",
			dirs:       []string{"0.1.16", "0.1.20", "0.1.21", "0.1.23"},
			keep:       "0.1.23",
			wantKeep:   []string{"0.1.23", "0.1.21"},
			wantRemove: []string{"0.1.16", "0.1.20"},
		},
		{
			// 回滚到旧版本后再运行：当前版本未必是最大号，仍保留最新的一份
			// 旧版本供再次回滚。
			name:       "当前版本不是最新时保留最新旧版",
			dirs:       []string{"0.1.16", "0.1.20", "0.1.21", "0.1.23"},
			keep:       "0.1.20",
			wantKeep:   []string{"0.1.20", "0.1.23"},
			wantRemove: []string{"0.1.16", "0.1.21"},
		},
		{
			name:     "两份以内不删",
			dirs:     []string{"0.1.21", "0.1.23"},
			keep:     "0.1.23",
			wantKeep: []string{"0.1.21", "0.1.23"},
		},
		{
			name:     "只有当前版本不删",
			dirs:     []string{"0.1.23"},
			keep:     "0.1.23",
			wantKeep: []string{"0.1.23"},
		},
		{
			name:       "版本号按数值排序而非字典序",
			dirs:       []string{"0.1.9", "0.1.10", "0.1.11"},
			keep:       "0.1.11",
			wantKeep:   []string{"0.1.11", "0.1.10"},
			wantRemove: []string{"0.1.9"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeRoot := t.TempDir()
			for _, name := range tc.dirs {
				makeVersionDir(t, runtimeRoot, name)
			}
			cleanupOldVersions(runtimeRoot, tc.keep)
			for _, name := range tc.wantKeep {
				assertDirExists(t, filepath.Join(runtimeRoot, name), true)
			}
			for _, name := range tc.wantRemove {
				assertDirExists(t, filepath.Join(runtimeRoot, name), false)
			}
		})
	}
}

// TestCleanupOldVersionsKeepsFreshStagingAndFiles 验证清理不碰新鲜的
// .staging- 临时目录（可能正被并发进程写入）与散落的普通文件。
func TestCleanupOldVersionsKeepsFreshStagingAndFiles(t *testing.T) {
	runtimeRoot := t.TempDir()
	makeVersionDir(t, runtimeRoot, "0.1.16")
	makeVersionDir(t, runtimeRoot, "0.1.20")
	makeVersionDir(t, runtimeRoot, "0.1.23")
	staging := filepath.Join(runtimeRoot, stagingPrefix+"0.1.24-123456")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	strayFile := filepath.Join(runtimeRoot, "notes.txt")
	if err := os.WriteFile(strayFile, []byte("排查线索"), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupOldVersions(runtimeRoot, "0.1.23")

	assertDirExists(t, staging, true)
	if data, err := os.ReadFile(strayFile); err != nil || string(data) != "排查线索" {
		t.Fatalf("散落文件不应被清理：data=%q err=%v", data, err)
	}
	assertDirExists(t, filepath.Join(runtimeRoot, "0.1.20"), true)
	assertDirExists(t, filepath.Join(runtimeRoot, "0.1.16"), false)
}

// TestCleanupOldVersionsRemovesStaleStaging 验证崩溃残留的 .staging-* 目录：
// mtime 早于 1 小时的删除，新鲜的（可能正被并发进程写入）保留。
func TestCleanupOldVersionsRemovesStaleStaging(t *testing.T) {
	runtimeRoot := t.TempDir()
	makeVersionDir(t, runtimeRoot, "0.1.23")
	oldStaging := filepath.Join(runtimeRoot, stagingPrefix+"0.1.22-100")
	freshStaging := filepath.Join(runtimeRoot, stagingPrefix+"0.1.24-200")
	for _, dir := range []string{oldStaging, freshStaging} {
		if err := os.MkdirAll(filepath.Join(dir, AdaptersDirName), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// 用 os.Chtimes 造两小时前的旧目录；新鲜目录保持当前 mtime。
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldStaging, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	cleanupOldVersions(runtimeRoot, "0.1.23")

	assertDirExists(t, oldStaging, false)
	assertDirExists(t, freshStaging, true)
	assertDirExists(t, filepath.Join(runtimeRoot, "0.1.23"), true)
}

// TestCleanupOldVersionsToleratesStagingRemoveFailures 验证过期 staging 目录
// 删除失败（如 Windows 上文件被占用）时只跳过，不影响主流程。
func TestCleanupOldVersionsToleratesStagingRemoveFailures(t *testing.T) {
	oldRemove := removePath
	t.Cleanup(func() { removePath = oldRemove })

	runtimeRoot := t.TempDir()
	makeVersionDir(t, runtimeRoot, "0.1.23")
	stale := filepath.Join(runtimeRoot, stagingPrefix+"0.1.22-100")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	removePath = func(path string) error {
		if path == stale {
			return errors.New("被占用")
		}
		return oldRemove(path)
	}

	cleanupOldVersions(runtimeRoot, "0.1.23")

	assertDirExists(t, stale, true) // 删不掉的留着下次再试
}

// TestCleanupOldVersionsToleratesRemoveFailures 模拟 Windows 上旧 bun.exe 被
// 残留子进程占用：删不掉的目录跳过并下次再试，不影响其余清理，更不影响主流程。
func TestCleanupOldVersionsToleratesRemoveFailures(t *testing.T) {
	oldRemove := removePath
	t.Cleanup(func() { removePath = oldRemove })

	t.Run("单个目录被占用时跳过并继续删其余", func(t *testing.T) {
		runtimeRoot := t.TempDir()
		for _, name := range []string{"0.1.16", "0.1.20", "0.1.21", "0.1.23"} {
			makeVersionDir(t, runtimeRoot, name)
		}
		busy := filepath.Join(runtimeRoot, "0.1.16")
		removePath = func(path string) error {
			if path == busy {
				return errors.New("被残留子进程占用")
			}
			return oldRemove(path)
		}
		// 同步调用不 panic、不报错即证明主流程不受影响。
		cleanupOldVersions(runtimeRoot, "0.1.23")
		assertDirExists(t, busy, true) // 删不掉的留着下次再试
		assertDirExists(t, filepath.Join(runtimeRoot, "0.1.20"), false)
		assertDirExists(t, filepath.Join(runtimeRoot, "0.1.21"), true)
		assertDirExists(t, filepath.Join(runtimeRoot, "0.1.23"), true)
	})

	t.Run("全部删除失败也安然返回", func(t *testing.T) {
		runtimeRoot := t.TempDir()
		for _, name := range []string{"0.1.16", "0.1.20", "0.1.21", "0.1.23"} {
			makeVersionDir(t, runtimeRoot, name)
		}
		removePath = func(string) error { return errors.New("locked") }
		cleanupOldVersions(runtimeRoot, "0.1.23")
		for _, name := range []string{"0.1.16", "0.1.20", "0.1.21", "0.1.23"} {
			assertDirExists(t, filepath.Join(runtimeRoot, name), true)
		}
	})
}

// TestCleanupOldVersionsHandlesMissingOrEmptyRoot 覆盖空目录、不存在的目录
// 与「runtime 路径是普通文件」等边界：全部安全返回，不 panic。
func TestCleanupOldVersionsHandlesMissingOrEmptyRoot(t *testing.T) {
	t.Run("runtime目录不存在", func(t *testing.T) {
		cleanupOldVersions(filepath.Join(t.TempDir(), runtimeDirName), "0.1.23")
	})
	t.Run("runtime目录为空", func(t *testing.T) {
		cleanupOldVersions(t.TempDir(), "0.1.23")
	})
	t.Run("runtime路径是普通文件", func(t *testing.T) {
		root := t.TempDir()
		runtimePath := filepath.Join(root, runtimeDirName)
		if err := os.WriteFile(runtimePath, []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
		cleanupOldVersions(runtimePath, "0.1.23")
	})
}

// TestEnsureCleansOldVersionDirs 端到端验证：解压完成与启动命中缓存两条路径
// 都会触发清理，且不影响解压产物与回滚副本。
func TestEnsureCleansOldVersionDirs(t *testing.T) {
	configDir := t.TempDir()
	runtimeRoot := filepath.Join(configDir, runtimeDirName)
	// 预置两份历史版本，模拟 0.1.16 → 0.1.21 两次升级留下的副本。
	for _, old := range []string{"0.1.16", "0.1.21"} {
		if err := installReadyTree(t, filepath.Join(runtimeRoot, old), old); err != nil {
			t.Fatal(err)
		}
	}

	// 升级到 0.1.23：解压完成后清掉最旧的 0.1.16，保留 0.1.21 供回滚。
	paths, err := ensureWith(configDir, "0.1.23", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, paths.Bun); got != "bun-binary" {
		t.Fatalf("解压后 bun 内容 = %q", got)
	}
	assertDirExists(t, filepath.Join(runtimeRoot, "0.1.21"), true)
	assertDirExists(t, filepath.Join(runtimeRoot, "0.1.16"), false)

	// 再次启动命中缓存时也应触发清理：手工塞一份更旧的目录，应被清掉。
	stale := filepath.Join(runtimeRoot, "0.1.10")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWith(configDir, "0.1.23", standardAssets(t)); err != nil {
		t.Fatal(err)
	}
	assertDirExists(t, stale, false)
	assertDirExists(t, filepath.Join(runtimeRoot, "0.1.21"), true)
	assertNoStaging(t, configDir)
}

// TestCleanupLogsActions 验证清理动作落日志：保留/删除摘要、单目录删除成功与
// 失败原因（写法参照 TestEnsureLogsProgress）。
func TestCleanupLogsActions(t *testing.T) {
	t.Setenv("QKA_LOG_LEVEL", "debug")
	logRoot := t.TempDir()
	closeLogs, err := applog.Init(logRoot, "test")
	if err != nil {
		t.Fatalf("applog.Init: %v", err)
	}

	oldRemove := removePath
	t.Cleanup(func() { removePath = oldRemove })
	busy := "0.1.16"
	removePath = func(path string) error {
		if strings.HasSuffix(path, busy) {
			return errors.New("被残留子进程占用")
		}
		return oldRemove(path)
	}

	runtimeRoot := t.TempDir()
	for _, name := range []string{"0.1.16", "0.1.20", "0.1.21", "0.1.23"} {
		makeVersionDir(t, runtimeRoot, name)
	}
	cleanupOldVersions(runtimeRoot, "0.1.23")
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
		"清理旧版本运行时目录：保留=0.1.23,0.1.21 待删=0.1.20,0.1.16",
		"已删除旧版本运行时目录 dir=0.1.20",
		"删除旧版本运行时目录失败（下次启动重试）dir=0.1.16",
		"被残留子进程占用",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("日志应包含 %q，实际：\n%s", want, data)
		}
	}
}
