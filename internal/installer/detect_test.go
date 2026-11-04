package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/runtimebundle"
)

// withDetect 临时替换「记录缺失后的残留安装探测」接缝，cleanup 时恢复；测试
// 用它把 Plan/Upgrade 与真机是否恰好装过 QKA 隔离开。
func withDetect(t *testing.T, detect func() string) {
	t.Helper()
	old := detectExistingInstall
	t.Cleanup(func() { detectExistingInstall = old })
	detectExistingInstall = detect
}

// fixedPaths 造一个 fileExists：只有 paths 里列出的文件算存在。
func fixedPaths(paths ...string) func(string) bool {
	return func(path string) bool {
		for _, p := range paths {
			if p == path {
				return true
			}
		}
		return false
	}
}

func TestDetectInstall(t *testing.T) {
	localAppData := `/Users/me/AppData/Local`
	defaultExe := filepath.Join(localAppData, "Programs", defaultDirName, ExeName)
	appData := `/Users/me/AppData/Roaming`
	lnk := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", shortcutLnkName)

	t.Run("命中路径1默认目录且优先于其余线索", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return localAppData },
			appData:      func() string { return appData },
			fileExists:   fixedPaths(defaultExe, `D:\Custom\QKA.exe`, `E:\Other\QKA.exe`),
			appPathExe:   func() (string, bool) { return `D:\Custom\QKA.exe`, true },
			lnkTarget:    func(string) (string, bool) { return `E:\Other\QKA.exe`, true },
		}
		if got := detectInstall(p); got != defaultExe {
			t.Fatalf("应命中默认目录 %q，实际 %q", defaultExe, got)
		}
	})

	t.Run("命中路径2App Paths", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return localAppData },
			appData:      func() string { return appData },
			// 默认目录的程序不在了，但 App Paths 指向自定义目录且文件存在。
			fileExists: fixedPaths(`D:\Custom Dir\QKA.exe`),
			appPathExe: func() (string, bool) { return `D:\Custom Dir\QKA.exe`, true },
			lnkTarget:  func(string) (string, bool) { return "", false },
		}
		if got := detectInstall(p); got != `D:\Custom Dir\QKA.exe` {
			t.Fatalf("应命中 App Paths 指向的路径，实际 %q", got)
		}
	})

	t.Run("命中路径3开始菜单快捷方式", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return "" },
			appData:      func() string { return appData },
			fileExists:   fixedPaths(`E:\Custom\QKA.exe`),
			appPathExe:   func() (string, bool) { return "", false },
			lnkTarget: func(path string) (string, bool) {
				if path != lnk {
					t.Fatalf("应解析开始菜单快捷方式 %q，实际 %q", lnk, path)
				}
				return `E:\Custom\QKA.exe`, true
			},
		}
		if got := detectInstall(p); got != `E:\Custom\QKA.exe` {
			t.Fatalf("应命中快捷方式指向的目标，实际 %q", got)
		}
	})

	t.Run("全部未命中时返回空串", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return localAppData },
			appData:      func() string { return appData },
			fileExists:   func(string) bool { return false },
			appPathExe:   func() (string, bool) { return "", false },
			lnkTarget:    func(string) (string, bool) { return "", false },
		}
		if got := detectInstall(p); got != "" {
			t.Fatalf("全未命中应返回空串，实际 %q", got)
		}
	})

	t.Run("App Paths 指向的文件不在了按未命中处理", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return "" },
			appData:      func() string { return "" },
			fileExists:   func(string) bool { return false },
			appPathExe:   func() (string, bool) { return `D:\gone\QKA.exe`, true },
			lnkTarget:    func(string) (string, bool) { return "", false },
		}
		if got := detectInstall(p); got != "" {
			t.Fatalf("注册表指向的文件不存在应降级为未命中，实际 %q", got)
		}
	})

	t.Run("快捷方式解析失败按未命中处理", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return "" },
			appData:      func() string { return appData },
			fileExists:   func(string) bool { return false },
			appPathExe:   func() (string, bool) { return "", false },
			lnkTarget:    func(string) (string, bool) { return "", false },
		}
		if got := detectInstall(p); got != "" {
			t.Fatalf(".lnk 解析失败应降级为未命中，实际 %q", got)
		}
	})

	t.Run("快捷方式指向的目标不在了按未命中处理", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return "" },
			appData:      func() string { return appData },
			fileExists:   func(string) bool { return false },
			appPathExe:   func() (string, bool) { return "", false },
			lnkTarget:    func(string) (string, bool) { return `E:\gone\QKA.exe`, true },
		}
		if got := detectInstall(p); got != "" {
			t.Fatalf("目标不存在应降级为未命中，实际 %q", got)
		}
	})

	t.Run("App Paths 指向发行物原名不算已安装", func(t *testing.T) {
		// 开发机残留：跑过一次未改名的构建产物并注册了 App Paths。文件在，
		// 但不叫 QKA.exe，不是安装动作落位的位置。
		artifact := `D:\repo\dist\build\QKA-windows-amd64.exe`
		p := probeSystem{
			localAppData: func() string { return "" },
			appData:      func() string { return "" },
			fileExists:   fixedPaths(artifact),
			appPathExe:   func() (string, bool) { return artifact, true },
			lnkTarget:    func(string) (string, bool) { return artifact, true },
		}
		if got := detectInstall(p); got != "" {
			t.Fatalf("发行物原名不应命中，实际 %q", got)
		}
	})

	t.Run("目标文件名大小写不敏感", func(t *testing.T) {
		p := probeSystem{
			localAppData: func() string { return "" },
			appData:      func() string { return "" },
			fileExists:   fixedPaths(`D:\Custom\qka.exe`),
			appPathExe:   func() (string, bool) { return `D:\Custom\qka.exe`, true },
			lnkTarget:    func(string) (string, bool) { return "", false },
		}
		if got := detectInstall(p); got != `D:\Custom\qka.exe` {
			t.Fatalf("小写 qka.exe 也应命中，实际 %q", got)
		}
	})
}

// TestSafeDetectRecovers 验证探测的硬性要求：接缝抛出任何异常都兜底成
// 「未探测到」（首次安装），绝不能让异常挡住安装向导。
func TestSafeDetectRecovers(t *testing.T) {
	p := probeSystem{
		localAppData: func() string { return "" },
		appData:      func() string { return "" },
		appPathExe:   func() (string, bool) { panic("registry exploded") },
		fileExists:   func(string) bool { return true },
		lnkTarget:    func(string) (string, bool) { return "", false },
	}
	if got := safeDetect(p); got != "" {
		t.Fatalf("探测异常应降级为未命中，实际 %q", got)
	}
}

// TestDetectDefaultProbeWiring 用环境接缝 + 真实文件验证 defaultProbe 的接线：
// LOCALAPPDATA 指向的默认目录里有 exe 时命中；两个环境变量都拿不到（且把
// 注册表/.lnk 两个线索固定为未命中）时返回空串。注册表与 .lnk 的真实读取分别
// 由 selfinstall 的实现与其测试保证。
func TestDetectDefaultProbeWiring(t *testing.T) {
	t.Run("默认目录有 exe 时命中", func(t *testing.T) {
		lad := t.TempDir()
		withEnv(t, "windows", map[string]string{
			"LOCALAPPDATA": lad,
			"APPDATA":      filepath.Join(t.TempDir(), "roaming"),
		})
		exe := filepath.Join(lad, "Programs", defaultDirName, ExeName)
		writeFile(t, exe, []byte("old-binary"), 0o700)
		if got := detectExistingInstall(); got != filepath.Clean(exe) {
			t.Fatalf("默认目录探测 = %q，want %q", got, filepath.Clean(exe))
		}
	})

	t.Run("目录里只有同目录文件而 exe 不在时不命中", func(t *testing.T) {
		lad := t.TempDir()
		withEnv(t, "windows", map[string]string{
			"LOCALAPPDATA": lad,
			"APPDATA":      filepath.Join(t.TempDir(), "roaming"),
		})
		// 默认目录存在，但里面是别的文件（目录不算已安装的程序）。
		writeFile(t, filepath.Join(lad, "Programs", defaultDirName, "readme.txt"), []byte("x"), 0o600)
		p := defaultProbe()
		p.appPathExe = func() (string, bool) { return "", false }
		p.lnkTarget = func(string) (string, bool) { return "", false }
		if got := safeDetect(p); got != "" {
			t.Fatalf("默认目录没有 exe 应未命中，实际 %q", got)
		}
	})

	t.Run("环境变量都拿不到时未命中", func(t *testing.T) {
		withEnv(t, "windows", nil)
		p := defaultProbe()
		p.appPathExe = func() (string, bool) { return "", false }
		p.lnkTarget = func(string) (string, bool) { return "", false }
		if got := safeDetect(p); got != "" {
			t.Fatalf("没有环境线索应未命中，实际 %q", got)
		}
	})
}

// TestUpgradeRepairsDetectedInstall 覆盖 issue #8 的修复主路径：install.json
// 丢失但探测到残留安装时，升级锁定在探测到的目录就地修复，并补写安装记录。
func TestUpgradeRepairsDetectedInstall(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config") // 没有 install.json
	installDir := filepath.Join(root, "Programs", "Quota Keepalive")
	installedExe := filepath.Join(installDir, ExeName)
	writeFile(t, installedExe, []byte("old-version-binary"), 0o700)
	selfPath := filepath.Join(root, "Downloads", "QKA-0.1.16.exe")
	writeFile(t, selfPath, []byte("new-version-binary"), 0o700)

	withDetect(t, func() string { return installedExe })
	oldRuntime, oldSelfInstall := ensureRuntime, ensureSelfInstall
	t.Cleanup(func() { ensureRuntime, ensureSelfInstall = oldRuntime, oldSelfInstall })
	ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
	ensureSelfInstall = func(string, string, string) error { return nil }

	result, err := Upgrade(context.Background(), Options{
		ConfigDir: configDir,
		Version:   "0.1.16",
		SelfPath:  selfPath,
	})
	if err != nil {
		t.Fatalf("探测到残留安装时应就地修复: %v", err)
	}
	if result.InstallDir != installDir || result.InstalledExe != installedExe {
		t.Fatalf("安装目录应锁定在探测到的位置，Result = %+v", result)
	}
	if data, err := os.ReadFile(installedExe); err != nil || string(data) != "new-version-binary" {
		t.Fatalf("修复后的程序 = %q/%v，应已替换为新版本", data, err)
	}
	record, ok := Load(configDir)
	if !ok || record.Version != "0.1.16" || record.ExePath != installedExe || record.InstallDir != installDir {
		t.Fatalf("修复后应补写安装记录，record = %+v/%v", record, ok)
	}
	// 修复完成后从安装目录启动应直接进托盘，下载目录的副本则按升级处理。
	if got := Plan(configDir, installedExe); got != ModeNone {
		t.Fatalf("修复后已安装 exe 启动 Plan = %v，want none", got)
	}
	if got := Plan(configDir, selfPath); got != ModeUpgrade {
		t.Fatalf("修复后下载副本启动 Plan = %v，want upgrade", got)
	}
}

// TestDetectExistingInstallExported 验证导出的探测入口就是接缝默认指向的
// 实现（向导展示层用它补「检测到已安装版本（路径 X）」的信息）。
func TestDetectExistingInstallExported(t *testing.T) {
	detected := `D:\Custom\QKA.exe`
	withDetect(t, func() string { return detected })
	if got := DetectExistingInstall(); got != detected {
		t.Fatalf("DetectExistingInstall = %q，want %q", got, detected)
	}
	if got := DetectExistingInstall(); !strings.EqualFold(got, detected) {
		t.Fatalf("重复调用应稳定返回同一结果，实际 %q", got)
	}
}
