package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"

	"qka/internal/app"
)

// 自安装失败只记一行日志，GUI 仍然进入事件循环：快捷方式只是便利，
// 不是 QKA 能启动的前提。
func TestTrayHandlerSelfInstallFailureDoesNotBlockGUI(t *testing.T) {
	oldRunTray, oldEnsure := runTrayGUI, ensureSelfInstall
	oldLog := log.Writer()
	t.Cleanup(func() {
		runTrayGUI, ensureSelfInstall = oldRunTray, oldEnsure
		log.SetOutput(oldLog)
	})

	env, _, _ := coverageEnv(t)
	env.Stdout = io.Discard
	env.Stderr = io.Discard
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var ran bool
	runTrayGUI = func(*app.App) error { ran = true; return nil }
	var gotConfigDir, gotExe, gotVersion string
	ensureSelfInstall = func(configDir, exe, version string) error {
		gotConfigDir, gotExe, gotVersion = configDir, exe, version
		return errors.New("controlled selfinstall failure")
	}
	var logged bytes.Buffer
	log.SetOutput(&logged)

	if resp, err := trayHandler(ctx, env, []string{"--addr", "127.0.0.1:0"}); err != nil || resp != nil {
		t.Fatalf("自安装失败不应阻塞 tray: resp=%v err=%v", resp, err)
	}
	if !ran {
		t.Fatal("自安装失败后 GUI 仍应启动")
	}
	if gotConfigDir == "" || gotExe == "" {
		t.Errorf("自安装应收到配置目录与 exe 路径: config=%q exe=%q", gotConfigDir, gotExe)
	}
	if gotVersion != Version {
		t.Errorf("自安装版本 = %q，期望 %q", gotVersion, Version)
	}
	if !strings.Contains(logged.String(), "自安装未完成") {
		t.Errorf("自安装失败应记一行日志，实际 %q", logged.String())
	}
}

// 已有 GUI 实例时，第二个进程必须在装配数据库/适配器/HTTP/GUI 之前退出，
// 只负责唤醒已有实例（Windows 上由 gui.AcquireGUIInstance 完成）。
func TestTrayHandlerSecondInstanceExitsBeforeStartingAnything(t *testing.T) {
	oldAcquire, oldRunTray := acquireTrayInstance, runTrayGUI
	t.Cleanup(func() { acquireTrayInstance, runTrayGUI = oldAcquire, oldRunTray })

	acquireTrayInstance = func() bool { return false }
	ran := false
	runTrayGUI = func(*app.App) error { ran = true; return nil }

	// 无效参数与不可装配的 env 都证明第二实例在解析参数、buildApp 之前就退出了。
	env := &Env{Getenv: func(string) string { return "" }}
	resp, err := trayHandler(context.Background(), env, []string{"--unknown-flag"})
	if err != nil || resp != nil {
		t.Fatalf("第二实例应静默退出: resp=%v err=%v", resp, err)
	}
	if ran {
		t.Fatal("第二实例不应启动 GUI")
	}
}
