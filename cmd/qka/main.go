package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"qka/internal/applog"
	"qka/internal/cli"
	"qka/internal/privacy"
)

var (
	runCLI      = cli.Run
	defaultEnv  = cli.DefaultEnv
	exitProcess = os.Exit
	// initAppLog 是日志初始化的接缝：测试替换它，避免往真实配置目录写文件。
	initAppLog = applog.Init
)

func main() {
	exitProcess(run(context.Background()))
}

// run 执行一次命令并返回进程退出码。
//
// 启动、退出码与 panic 都落到 <configDir>/logs/：windowsgui 程序没有控制台，
// 落盘日志是用户事后唯一能拿到的排查线索。日志初始化失败不影响运行，只是
// 退回「无日志」模式；panic 被捕获后写日志/崩溃文件并返回 applog.PanicExitCode，
// 而不是让进程静默消失。
func run(ctx context.Context) (code int) {
	env := defaultEnv()
	getenv := env.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	configDir := cli.ResolveConfigDir(getenv)

	closeLogs, err := initAppLog(configDir, cli.Version)
	if closeLogs != nil {
		defer func() { _ = closeLogs() }()
	}
	if err != nil && env.Stderr != nil {
		fmt.Fprintf(env.Stderr, "qka: 日志初始化失败（不影响运行）：%v\n", err)
	}

	applog.Infof("main", "QKA 启动 version=%s os=%s arch=%s args=%s",
		cli.Version, runtime.GOOS, runtime.GOARCH,
		privacy.SanitizeText(strings.Join(os.Args, " "), 0))

	// panic 在这一层被 applog.Run 捕获：写日志/崩溃文件后按 PanicExitCode
	// 退出，退出码仍然记一行。
	code = applog.Run(func() int { return runCLI(ctx, env) })
	applog.Infof("main", "QKA 退出 code=%d", code)
	return code
}
