package cli

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestConfigDirDefaultsToDotQkaUnderHome(t *testing.T) {
	getenv := func(k string) string {
		if k == "HOME" {
			return "/Users/yu"
		}
		return ""
	}
	got := ResolveConfigDir(getenv)
	if got != "/Users/yu/.qka" {
		t.Errorf("期望默认 ~/.qka，实际 %q", got)
	}
}

func TestConfigDirOverrideByEnv(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "HOME":
			return "/Users/yu"
		case "QKA_CONFIG_DIR":
			return "/tmp/custom-qka"
		}
		return ""
	}
	got := ResolveConfigDir(getenv)
	if got != "/tmp/custom-qka" {
		t.Errorf("期望 QKA_CONFIG_DIR 覆盖，实际 %q", got)
	}
}

// withWindowsConfigEnv 把平台与 os.UserConfigDir 换成受控值，测试结束恢复。
func withWindowsConfigEnv(t *testing.T, userConfig string, userConfigErr error) {
	t.Helper()
	oldGOOS, oldUserConfig := runtimeGOOS, userConfigDir
	runtimeGOOS = "windows"
	userConfigDir = func() (string, error) {
		return userConfig, userConfigErr
	}
	t.Cleanup(func() {
		runtimeGOOS, userConfigDir = oldGOOS, oldUserConfig
	})
}

// Windows 上 HOME 通常不存在：必须落到 %LOCALAPPDATA%\QKA，而不是相对路径。
func TestConfigDirWindowsUsesLocalAppData(t *testing.T) {
	withWindowsConfigEnv(t, `C:\Users\me\AppData\Roaming`, nil)
	getenv := func(k string) string {
		switch k {
		case "LOCALAPPDATA":
			return `C:\Users\me\AppData\Local`
		case "HOME":
			return ""
		}
		return ""
	}
	got := ResolveConfigDir(getenv)
	if want := filepath.Join(`C:\Users\me\AppData\Local`, "QKA"); got != want {
		t.Errorf("期望 %q，实际 %q", want, got)
	}
}

// 没有 LOCALAPPDATA 时退回 os.UserConfigDir，仍然不落到相对路径。
func TestConfigDirWindowsFallsBackToUserConfigDir(t *testing.T) {
	withWindowsConfigEnv(t, `C:\Users\me\AppData\Roaming`, nil)
	getenv := func(string) string { return "" }
	got := ResolveConfigDir(getenv)
	if want := filepath.Join(`C:\Users\me\AppData\Roaming`, "QKA"); got != want {
		t.Errorf("期望 %q，实际 %q", want, got)
	}
}

// os.UserConfigDir 不可用时才回退 $HOME/.qka（极少数环境）。
func TestConfigDirWindowsFallsBackToHomeWhenUserConfigDirFails(t *testing.T) {
	withWindowsConfigEnv(t, "", errors.New("no config dir"))
	getenv := func(k string) string {
		if k == "HOME" {
			return `C:\Users\me`
		}
		return ""
	}
	got := ResolveConfigDir(getenv)
	if want := filepath.Join(`C:\Users\me`, ".qka"); got != want {
		t.Errorf("期望 %q，实际 %q", want, got)
	}
}

// os.UserConfigDir 返回空串也不该落到相对路径。
func TestConfigDirWindowsEmptyUserConfigDirFallsBackToHome(t *testing.T) {
	withWindowsConfigEnv(t, "", nil)
	getenv := func(k string) string {
		if k == "HOME" {
			return `C:\Users\me`
		}
		return ""
	}
	got := ResolveConfigDir(getenv)
	if want := filepath.Join(`C:\Users\me`, ".qka"); got != want {
		t.Errorf("期望 %q，实际 %q", want, got)
	}
}
