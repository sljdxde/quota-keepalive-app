package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/app"
	"qka/internal/gui"
	"qka/internal/installer"
)

// installerTestEnv 是「双击启动」形态的 Env：默认配置目录、无参数、不设
// QKA_CONFIG_DIR。
func installerTestEnv(configDir string) *Env {
	return &Env{
		ConfigDir: configDir,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
		Getenv:    func(string) string { return "" },
	}
}

func TestInstallerPlan(t *testing.T) {
	oldGOOS, oldFlavor, oldPlan, oldExecutable := runtimeGOOS, BuildFlavor, planInstall, executablePath
	t.Cleanup(func() {
		runtimeGOOS, BuildFlavor = oldGOOS, oldFlavor
		planInstall, executablePath = oldPlan, oldExecutable
	})

	configDir := t.TempDir()
	env := installerTestEnv(configDir)
	executablePath = func() (string, error) { return "/downloads/QKA-0.1.16.exe", nil }
	var gotConfigDir, gotSelfPath string
	var planCalls int
	planInstall = func(dir, selfPath string) installer.Mode {
		gotConfigDir, gotSelfPath = dir, selfPath
		planCalls++
		return installer.ModeInstall
	}

	runtimeGOOS = "darwin"
	BuildFlavor = "gui"
	if got := installerPlan(env); got != installer.ModeNone {
		t.Fatalf("非 Windows 平台 Plan = %v，want none", got)
	}

	runtimeGOOS = "windows"
	BuildFlavor = "cli"
	if got := installerPlan(env); got != installer.ModeNone {
		t.Fatalf("CLI 构建 Plan = %v，want none", got)
	}

	BuildFlavor = "gui"
	if got := installerPlan(&Env{ConfigDir: configDir}); got != installer.ModeNone {
		t.Fatalf("没有 Getenv 的环境 Plan = %v，want none", got)
	}
	if got := installerPlan(&Env{Args: []string{"tray"}, ConfigDir: configDir, Getenv: func(string) string { return "" }}); got != installer.ModeNone {
		t.Fatalf("带参数启动 Plan = %v，want none", got)
	}
	if got := installerPlan(&Env{ConfigDir: configDir, Getenv: func(key string) string {
		if key == "QKA_CONFIG_DIR" {
			return configDir
		}
		return ""
	}}); got != installer.ModeNone {
		t.Fatalf("显式指定 QKA_CONFIG_DIR 时 Plan = %v，want none", got)
	}
	if planCalls != 0 {
		t.Fatalf("被排除的场景不应调用 installer.Plan，实际调用 %d 次", planCalls)
	}

	executablePath = func() (string, error) { return "", errors.New("unavailable") }
	if got := installerPlan(env); got != installer.ModeNone {
		t.Fatalf("定位当前程序失败时 Plan = %v，want none", got)
	}
	if planCalls != 0 {
		t.Fatal("定位失败时不应调用 installer.Plan")
	}

	executablePath = func() (string, error) { return "/downloads/QKA-0.1.16.exe", nil }
	if got := installerPlan(env); got != installer.ModeInstall {
		t.Fatalf("Windows GUI 无参数未安装 Plan = %v，want install", got)
	}
	if gotConfigDir != configDir || gotSelfPath != "/downloads/QKA-0.1.16.exe" {
		t.Fatalf("Plan 参数 = %q/%q", gotConfigDir, gotSelfPath)
	}

	planInstall = func(string, string) installer.Mode { return installer.ModeUpgrade }
	if got := installerPlan(env); got != installer.ModeUpgrade {
		t.Fatalf("下载目录里的新 exe 应走升级，Plan = %v", got)
	}
	planInstall = func(string, string) installer.Mode { return installer.ModeNone }
	if got := installerPlan(env); got != installer.ModeNone {
		t.Fatalf("已安装的那份应直接托盘，Plan = %v", got)
	}
}

func TestRunNoArgsInstallerMode(t *testing.T) {
	oldGOOS, oldFlavor := runtimeGOOS, BuildFlavor
	oldPlan, oldNew, oldRunGoroutine := planInstall, newInstallerWizard, runInstallWizardApp
	oldTray, oldExecutable := trayModeHandler, executablePath
	oldRuntimeReady := runtimeReady
	t.Cleanup(func() {
		runtimeGOOS, BuildFlavor = oldGOOS, oldFlavor
		planInstall, newInstallerWizard, runInstallWizardApp = oldPlan, oldNew, oldRunGoroutine
		trayModeHandler, executablePath = oldTray, oldExecutable
		runtimeReady = oldRuntimeReady
	})

	runtimeGOOS = "windows"
	BuildFlavor = "gui"
	// 测试二进制没有 embeddedruntime 标签，显式声明「这次构建可安装」，
	// 否则向导会在前置检查处提前失败（那条分支由
	// TestRunInstallWizardRequiresEmbeddedRuntime 覆盖）。
	runtimeReady = func() bool { return true }
	executablePath = func() (string, error) { return "/downloads/QKA-0.1.16.exe", nil }

	configDir := t.TempDir()
	var gotDeps gui.InstallerDeps
	var wizardRan bool
	newInstallerWizard = func(deps gui.InstallerDeps) *gui.Installer {
		gotDeps = deps
		return &gui.Installer{}
	}
	runInstallWizardApp = func(*gui.Installer) error {
		wizardRan = true
		return nil
	}
	var trayRan bool
	trayModeHandler = func(context.Context, *Env, []string) (*Response, error) {
		trayRan = true
		return nil, nil
	}

	var stderr strings.Builder
	env := installerTestEnv(configDir)
	env.Stderr = &stderr

	// 首次安装：进安装向导，不启动托盘/HTTP/适配器。
	planInstall = func(string, string) installer.Mode { return installer.ModeInstall }
	if code := Run(context.Background(), env); code != 0 {
		t.Fatalf("首次安装向导退出码 = %d，stderr=%q", code, stderr.String())
	}
	if !wizardRan || trayRan {
		t.Fatalf("未安装时应进安装向导、不进托盘: wizard=%v tray=%v", wizardRan, trayRan)
	}
	if gotDeps.ConfigDir != configDir || gotDeps.Version != Version || gotDeps.SelfPath != "/downloads/QKA-0.1.16.exe" {
		t.Fatalf("安装向导依赖 = %+v", gotDeps)
	}

	// 升级：同样进向导（由向导内部按 Plan 切文案与动作）。
	wizardRan, trayRan = false, false
	planInstall = func(string, string) installer.Mode { return installer.ModeUpgrade }
	if code := Run(context.Background(), env); code != 0 {
		t.Fatalf("升级向导退出码 = %d", code)
	}
	if !wizardRan || trayRan {
		t.Fatalf("升级场景应进升级向导: wizard=%v tray=%v", wizardRan, trayRan)
	}

	// 当前 exe 就是已安装的那份：直接托盘，不弹向导。
	wizardRan, trayRan = false, false
	planInstall = func(string, string) installer.Mode { return installer.ModeNone }
	if code := Run(context.Background(), env); code != 0 {
		t.Fatalf("托盘退出码 = %d", code)
	}
	if wizardRan || !trayRan {
		t.Fatalf("已安装时应直接进托盘: wizard=%v tray=%v", wizardRan, trayRan)
	}

	// 向导返回错误：退出码 2，错误写 stderr。
	planInstall = func(string, string) installer.Mode { return installer.ModeInstall }
	wizardRan = false
	runInstallWizardApp = func(*gui.Installer) error {
		wizardRan = true
		return errors.New("controlled wizard failure")
	}
	stderr.Reset()
	if code := Run(context.Background(), env); code != 2 || !strings.Contains(stderr.String(), "controlled wizard failure") {
		t.Fatalf("安装向导失败应返回退出码 2: code=%d stderr=%q", code, stderr.String())
	}
	if !wizardRan {
		t.Fatal("安装失败前应确实进过安装向导")
	}
}

func TestRunInstallWizardResolvesSelfPath(t *testing.T) {
	oldNew, oldRunGoroutine, oldExecutable := newInstallerWizard, runInstallWizardApp, executablePath
	oldRuntimeReady := runtimeReady
	t.Cleanup(func() {
		newInstallerWizard, runInstallWizardApp = oldNew, oldRunGoroutine
		executablePath = oldExecutable
		runtimeReady = oldRuntimeReady
	})
	runtimeReady = func() bool { return true }

	configDir := t.TempDir()
	env := installerTestEnv(configDir)

	var gotDeps gui.InstallerDeps
	newInstallerWizard = func(deps gui.InstallerDeps) *gui.Installer {
		gotDeps = deps
		return &gui.Installer{}
	}
	runInstallWizardApp = func(*gui.Installer) error { return nil }
	executablePath = func() (string, error) { return "/opt/QKA.exe", nil }
	if err := runInstallWizard(env); err != nil {
		t.Fatalf("安装向导不应失败: %v", err)
	}
	if gotDeps.SelfPath != "/opt/QKA.exe" || gotDeps.ConfigDir != configDir || gotDeps.Version != Version {
		t.Fatalf("安装向导依赖 = %+v", gotDeps)
	}

	executablePath = func() (string, error) { return "", errors.New("unavailable") }
	if err := runInstallWizard(env); err == nil || !strings.Contains(err.Error(), "定位当前程序失败") {
		t.Fatalf("定位当前程序失败应回传错误，err=%v", err)
	}

	// 事件循环的错误原样向上抛。
	executablePath = func() (string, error) { return "/opt/QKA.exe", nil }
	runInstallWizardApp = func(*gui.Installer) error { return errors.New("loop failed") }
	if err := runInstallWizard(env); err == nil || !strings.Contains(err.Error(), "loop failed") {
		t.Fatalf("事件循环错误应向上抛，err=%v", err)
	}
}

// TestRunInstallWizardRequiresEmbeddedRuntime 覆盖「GUI 版但没嵌运行时」：
// 必须在打开向导之前就失败并给出可执行的修法，而不是让用户在最后一步
// 看到「解压运行时失败」。
func TestRunInstallWizardRequiresEmbeddedRuntime(t *testing.T) {
	oldNew, oldRunGoroutine, oldRuntimeReady := newInstallerWizard, runInstallWizardApp, runtimeReady
	oldShowBlocked := showInstallBlocked
	t.Cleanup(func() {
		newInstallerWizard, runInstallWizardApp = oldNew, oldRunGoroutine
		runtimeReady = oldRuntimeReady
		showInstallBlocked = oldShowBlocked
	})

	wizardCreated := false
	newInstallerWizard = func(gui.InstallerDeps) *gui.Installer {
		wizardCreated = true
		return &gui.Installer{}
	}
	runInstallWizardApp = func(*gui.Installer) error {
		t.Fatal("未内嵌运行时时不应进入向导事件循环")
		return nil
	}
	runtimeReady = func() bool { return false }
	// windowsgui 没有可见 stderr：拦截原生弹窗，断言用户确实被告知原因。
	var dialogTitle, dialogBody string
	showInstallBlocked = func(title, body string) {
		dialogTitle, dialogBody = title, body
	}

	err := runInstallWizard(installerTestEnv(t.TempDir()))
	if err == nil {
		t.Fatal("未内嵌运行时必须失败")
	}
	for _, want := range []string{"没有内嵌运行时", "build-windows"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误消息缺少 %q: %v", want, err)
		}
	}
	if dialogTitle == "" || !strings.Contains(dialogBody, "没有内嵌运行时") {
		t.Fatalf("应弹出原生提示说明原因，实际 title=%q body=%q", dialogTitle, dialogBody)
	}
	if wizardCreated {
		t.Fatal("应在创建向导之前就拦住")
	}
}

func TestInstallerPlanUsesDefaultConfigDir(t *testing.T) {
	oldGOOS, oldFlavor, oldPlan, oldExecutable := runtimeGOOS, BuildFlavor, planInstall, executablePath
	t.Cleanup(func() {
		runtimeGOOS, BuildFlavor = oldGOOS, oldFlavor
		planInstall, executablePath = oldPlan, oldExecutable
	})

	runtimeGOOS = "windows"
	BuildFlavor = "gui"
	executablePath = func() (string, error) { return `C:\Users\me\Downloads\QKA.exe`, nil }
	wantDir := filepath.Join(`C:\Users\me\AppData\Local`, "QKA")
	planInstall = func(dir, selfPath string) installer.Mode {
		if dir != wantDir {
			t.Fatalf("配置目录 = %q，want %q", dir, wantDir)
		}
		if selfPath != `C:\Users\me\Downloads\QKA.exe` {
			t.Fatalf("selfPath = %q", selfPath)
		}
		return installer.ModeInstall
	}
	env := &Env{
		Stdout: io.Discard,
		Stderr: io.Discard,
		Getenv: func(key string) string {
			if key == "LOCALAPPDATA" {
				return `C:\Users\me\AppData\Local`
			}
			return ""
		},
	}
	if got := installerPlan(env); got != installer.ModeInstall {
		t.Fatalf("Windows GUI 无参数未安装应进安装向导，Plan = %v", got)
	}
}

// 已经通过安装向导装过时，托盘启动的自安装必须把快捷方式/App Paths 指向安装
// 目录里的 QKA.exe，而不是用户可能再次双击的下载文件。
func TestTraySelfInstallUsesInstalledExe(t *testing.T) {
	oldRunTray, oldEnsure := runTrayGUI, ensureSelfInstall
	t.Cleanup(func() { runTrayGUI, ensureSelfInstall = oldRunTray, oldEnsure })

	env, configDir, _ := coverageEnv(t)
	env.Stdout = io.Discard
	env.Stderr = io.Discard

	installedExe := filepath.Join(configDir, "Programs", "Quota Keepalive", "QKA.exe")
	record := installer.Record{
		Schema:      installer.SchemaVersion,
		Version:     Version,
		InstallDir:  filepath.Dir(installedExe),
		ExePath:     installedExe,
		InstalledAt: "2026-01-02T03:04:05Z",
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "install.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runTrayGUI = func(*app.App) error { return nil }
	var gotExe string
	ensureSelfInstall = func(_, exe, _ string) error {
		gotExe = exe
		return nil
	}
	if _, err := trayHandler(ctx, env, []string{"--addr", "127.0.0.1:0"}); err != nil {
		t.Fatalf("托盘启动失败: %v", err)
	}
	if gotExe != installedExe {
		t.Fatalf("自安装应指向已安装的程序 %q，实际 %q", installedExe, gotExe)
	}
}
