package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeCleanupFile 在测试目录里写一个文件，返回完整路径。
func writeCleanupFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeStagedMetadata 写入一份 staged.json，指向指定安装包路径。
func writeStagedMetadata(t *testing.T, stateDir, version, artifactPath string) {
	t.Helper()
	data, err := json.Marshal(stagedRelease{
		Release: Release{Version: version, Name: filepath.Base(artifactPath)},
		Path:    artifactPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "staged.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeStagedFixture 造一个「已下载但未安装」的安装包与对应的 staged.json。
func writeStagedFixture(t *testing.T, stateDir, version, name string) string {
	t.Helper()
	path := writeCleanupFile(t, filepath.Join(stateDir, name), "payload")
	writeStagedMetadata(t, stateDir, version, path)
	return path
}

// assertPathAbsent 断言路径已被删除。
func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("路径应被清理 path=%s err=%v", path, err)
	}
}

// assertFileContent 断言文件存在且内容不变（用于验证无关文件不被误删）。
func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s 内容 = %q，want %q", path, data, want)
	}
}

// TestCleanupStaleRemovesOldStagedArtifactAndOrphans 覆盖主场景：staged 版本
// 不高于当前版本（更新已安装成功）时，删除其安装包、staged.json、其它旧
// 安装包与中断的 .part，同时不动无关文件。
func TestCleanupStaleRemovesOldStagedArtifactAndOrphans(t *testing.T) {
	stateDir := t.TempDir()
	stagedPath := writeStagedFixture(t, stateDir, "1.0.0", "QKA-1.0.0-arm64.dmg")
	orphan := writeCleanupFile(t, filepath.Join(stateDir, "QKA-0.9.0-arm64.dmg"), "old")
	part := writeCleanupFile(t, filepath.Join(stateDir, "QKA-2.0.0-arm64.dmg.part"), "partial")
	notes := writeCleanupFile(t, filepath.Join(stateDir, "notes.txt"), "排查线索")
	script := writeCleanupFile(t, filepath.Join(stateDir, "qka-install.sh"), "#!/bin/sh")

	svc := &Service{CurrentVersion: "2.0.0", StateDir: stateDir}
	svc.CleanupStale()

	assertPathAbsent(t, stagedPath)
	assertPathAbsent(t, filepath.Join(stateDir, "staged.json"))
	assertPathAbsent(t, orphan)
	assertPathAbsent(t, part)
	assertFileContent(t, notes, "排查线索")
	assertFileContent(t, script, "#!/bin/sh")
}

// TestCleanupStaleRemovesArtifactOfAlreadyRunningVersion 覆盖更新完成后的
// 典型残留：staged 版本与当前版本相同（dmg 已安装、安装脚本只删自身），
// 启动清理应回收这份 60MB+ 的安装包。
func TestCleanupStaleRemovesArtifactOfAlreadyRunningVersion(t *testing.T) {
	stateDir := t.TempDir()
	stagedPath := writeStagedFixture(t, stateDir, "2.0.0", "QKA-2.0.0-arm64.dmg")

	(&Service{CurrentVersion: "2.0.0", StateDir: stateDir}).CleanupStale()

	assertPathAbsent(t, stagedPath)
	assertPathAbsent(t, filepath.Join(stateDir, "staged.json"))
}

// TestCleanupStaleKeepsNewerStagedArtifact 覆盖必须保留的场景：staged 版本
// 高于当前版本（正在等待安装）时，安装包与 staged.json 都不能删；但同目录
// 里的旧安装包与 .part 仍应回收。
func TestCleanupStaleKeepsNewerStagedArtifact(t *testing.T) {
	stateDir := t.TempDir()
	stagedPath := writeStagedFixture(t, stateDir, "3.0.0", "QKA-3.0.0-arm64.dmg")
	old := writeCleanupFile(t, filepath.Join(stateDir, "QKA-2.0.0-arm64.dmg"), "old")
	part := writeCleanupFile(t, filepath.Join(stateDir, "QKA-3.0.0-arm64.dmg.part"), "partial")

	svc := &Service{CurrentVersion: "2.0.0", StateDir: stateDir}
	svc.CleanupStale()

	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("待安装的新版本必须保留 path=%s: %v", stagedPath, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "staged.json")); err != nil {
		t.Fatalf("待安装的新版本元数据必须保留: %v", err)
	}
	assertPathAbsent(t, old)
	assertPathAbsent(t, part)
}

// TestNewCleansStaleUpdateDirectory 验证 New() 构造即触发一次清理：
// 启动时就能回收上次更新留下的安装包，而不是等到下次下载。
func TestNewCleansStaleUpdateDirectory(t *testing.T) {
	configDir := t.TempDir()
	stateDir := filepath.Join(configDir, "updates")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stagedPath := writeStagedFixture(t, stateDir, "0.1.0", "QKA-0.1.0-arm64.dmg")

	New("0.2.0", configDir)

	assertPathAbsent(t, stagedPath)
	assertPathAbsent(t, filepath.Join(stateDir, "staged.json"))
}

// TestCleanupStaleNeverDeletesOutsideStateDir 验证被篡改/损坏的 staged.json
// 即使指向 StateDir 之外，也只清理元数据，绝不删除外部文件。
func TestCleanupStaleNeverDeletesOutsideStateDir(t *testing.T) {
	stateDir := t.TempDir()
	victim := writeCleanupFile(t, filepath.Join(t.TempDir(), "QKA-1.0.0.dmg"), "outside")
	writeStagedMetadata(t, stateDir, "1.0.0", victim)

	(&Service{CurrentVersion: "2.0.0", StateDir: stateDir}).CleanupStale()

	assertFileContent(t, victim, "outside")
	assertPathAbsent(t, filepath.Join(stateDir, "staged.json"))
}

// TestCleanupStaleToleratesBadMetadataAndMissingStateDir 覆盖边界：损坏的
// staged.json、缺失的 StateDir、空 StateDir 都安全返回，不影响启动。
func TestCleanupStaleToleratesBadMetadataAndMissingStateDir(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "staged.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := writeCleanupFile(t, filepath.Join(stateDir, "payload.bin"), "not matching pattern")
	(&Service{CurrentVersion: "2.0.0", StateDir: stateDir}).CleanupStale()
	assertPathAbsent(t, filepath.Join(stateDir, "staged.json"))
	assertFileContent(t, keep, "not matching pattern")

	(&Service{CurrentVersion: "2.0.0", StateDir: filepath.Join(t.TempDir(), "missing")}).CleanupStale()
	(&Service{}).CleanupStale()
}

// TestStaleUpdateFiles 是清理决策纯函数的表驱动测试：只选 *.part 与本服务
// 命名的安装包，且保留当前 staged 的那一份。
func TestStaleUpdateFiles(t *testing.T) {
	stateDir := t.TempDir()
	names := []string{
		"QKA-1.0.0-arm64.dmg",
		"QKA-2.0.0-arm64.dmg",
		"QKA-3.0.0-windows-amd64.exe",
		"QKA.dmg",
		"QKA_1.0.0.zip",
		"QKA.backup.zip",
		"download.part",
		"QKA-4.0.0-arm64.dmg.part",
		"staged.json",
		"qka-install.sh",
		"qka-install-error.log",
		"notes.txt",
		"QKAtool-1.0.0.dmg",
		"README",
	}
	tests := []struct {
		name     string
		keepPath string
		want     []string
	}{
		{
			name:     "保留当前 staged 安装包",
			keepPath: filepath.Join(stateDir, "QKA-2.0.0-arm64.dmg"),
			want: []string{
				"QKA-1.0.0-arm64.dmg", "QKA-3.0.0-windows-amd64.exe", "QKA.dmg",
				"download.part", "QKA-4.0.0-arm64.dmg.part",
			},
		},
		{
			name: "没有 staged 时清理全部安装包",
			want: []string{
				"QKA-1.0.0-arm64.dmg", "QKA-2.0.0-arm64.dmg",
				"QKA-3.0.0-windows-amd64.exe", "QKA.dmg",
				"download.part", "QKA-4.0.0-arm64.dmg.part",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := staleUpdateFiles(stateDir, names, tt.keepPath)
			want := make(map[string]bool, len(tt.want))
			for _, name := range tt.want {
				want[filepath.Join(stateDir, name)] = true
			}
			if len(got) != len(want) {
				t.Fatalf("staleUpdateFiles = %v，want %v", got, tt.want)
			}
			for _, path := range got {
				if !want[path] {
					t.Fatalf("staleUpdateFiles 返回了不该删除的路径 %s（全部结果 %v）", path, got)
				}
			}
		})
	}
}
