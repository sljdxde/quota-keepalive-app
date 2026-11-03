package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/applog"
	"qka/internal/cli"
)

// stubAppLog 替换日志初始化接缝，避免用例往真实配置目录写文件。
func stubAppLog(t *testing.T) {
	t.Helper()
	old := initAppLog
	t.Cleanup(func() { initAppLog = old })
	initAppLog = func(string, string) (func() error, error) {
		return func() error { return nil }, nil
	}
}

// stubRun 替换 CLI 入口，返回固定退出码（或执行 fn）。
func stubRun(t *testing.T, fn func(context.Context, *cli.Env) int) {
	t.Helper()
	old := runCLI
	t.Cleanup(func() { runCLI = old })
	runCLI = fn
}

func TestRunUsesDefaultEnvironment(t *testing.T) {
	oldEnv := defaultEnv
	t.Cleanup(func() { defaultEnv = oldEnv })
	stubAppLog(t)

	wantCtx := context.Background()
	wantEnv := &cli.Env{}
	stubRun(t, func(ctx context.Context, env *cli.Env) int {
		if ctx != wantCtx {
			t.Fatal("上下文应原样传递")
		}
		if env != wantEnv {
			t.Fatalf("应使用默认环境，实际 %#v", env)
		}
		return 17
	})
	defaultEnv = func() *cli.Env {
		return wantEnv
	}
	if got := run(wantCtx); got != 17 {
		t.Fatalf("应返回 CLI 退出码 17，实际 %d", got)
	}
}

func TestMainDelegatesExitCode(t *testing.T) {
	oldEnv, oldExit := defaultEnv, exitProcess
	t.Cleanup(func() {
		defaultEnv, exitProcess = oldEnv, oldExit
	})
	stubAppLog(t)
	stubRun(t, func(context.Context, *cli.Env) int { return 23 })

	defaultEnv = func() *cli.Env { return &cli.Env{} }
	var got int
	exitProcess = func(code int) { got = code }
	main()
	if got != 23 {
		t.Fatalf("main 应传递退出码，实际 %d", got)
	}
}

func TestRunWritesStartupAndExitToAppLog(t *testing.T) {
	oldEnv := defaultEnv
	t.Cleanup(func() { defaultEnv = oldEnv })

	// 这里用真实的 applog：日志必须真的落到 <configDir>/logs/，run 返回时关闭。
	logRoot := t.TempDir()
	oldInit := initAppLog
	t.Cleanup(func() { initAppLog = oldInit })
	initAppLog = func(_, version string) (func() error, error) {
		return applog.Init(logRoot, version)
	}

	var stderr bytes.Buffer
	defaultEnv = func() *cli.Env {
		return &cli.Env{Getenv: os.Getenv, Stdout: &stderr, Stderr: &stderr}
	}
	stubRun(t, func(context.Context, *cli.Env) int { return 19 })

	if got := run(context.Background()); got != 19 {
		t.Fatalf("应返回退出码 19，实际 %d", got)
	}

	matches, err := filepath.Glob(filepath.Join(logRoot, "logs", "qka-*.log"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("应生成日志文件，实际 %v（err=%v）", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}
	for _, want := range []string{"QKA 启动", "QKA 退出 code=19", "version=", "os=", "arch="} {
		if !strings.Contains(string(data), want) {
			t.Errorf("日志应包含 %q，实际：\n%s", want, data)
		}
	}
}

func TestRunKeepsGoingWhenLogInitFails(t *testing.T) {
	oldEnv := defaultEnv
	t.Cleanup(func() { defaultEnv = oldEnv })
	oldInit := initAppLog
	t.Cleanup(func() { initAppLog = oldInit })
	initAppLog = func(string, string) (func() error, error) {
		return func() error { return nil }, errors.New("日志目录不可写")
	}

	var stderr bytes.Buffer
	defaultEnv = func() *cli.Env {
		return &cli.Env{Getenv: os.Getenv, Stdout: &stderr, Stderr: &stderr}
	}
	stubRun(t, func(context.Context, *cli.Env) int { return 5 })

	if got := run(context.Background()); got != 5 {
		t.Fatalf("日志初始化失败不应影响退出码，实际 %d", got)
	}
	if !strings.Contains(stderr.String(), "日志初始化失败") {
		t.Errorf("应在 stderr 提示日志初始化失败，实际 %q", stderr.String())
	}
}

func TestRunMapsPanicToExitCode(t *testing.T) {
	oldEnv := defaultEnv
	t.Cleanup(func() { defaultEnv = oldEnv })
	stubAppLog(t)
	defaultEnv = func() *cli.Env {
		return &cli.Env{Getenv: os.Getenv, Stderr: &bytes.Buffer{}}
	}
	stubRun(t, func(context.Context, *cli.Env) int { panic("run boom") })

	if got := run(context.Background()); got != applog.PanicExitCode {
		t.Fatalf("panic 应映射成退出码 %d，实际 %d", applog.PanicExitCode, got)
	}
}
