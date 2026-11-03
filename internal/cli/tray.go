package cli

import (
	"context"
	"log"
	"net/http"

	"qka/internal/app"
	"qka/internal/gui"
	"qka/internal/selfinstall"
)

var runTrayGUI = func(built *app.App) error {
	return built.GUI().Run()
}

// acquireTrayInstance 是 GUI 单实例守卫：Windows 上重复双击 exe 时返回 false
// 并唤醒已有实例的主窗口；其它平台恒为 true。测试替换它来验证第二实例的
// 早退路径。
var acquireTrayInstance = gui.AcquireGUIInstance

// ensureSelfInstall 是进入 GUI 事件循环前的自安装动作；测试替换它以覆盖
// 失败路径，验证失败不会阻塞启动。
var ensureSelfInstall = selfinstall.Ensure

// trayHandler 启动菜单栏 GUI：装配组合根，在后台 goroutine 启动 MCP HTTP 网关，
// 然后在主 goroutine 进入 Wails 事件循环（托盘 + 面板 + 主窗口），阻塞直到退出。
func trayHandler(ctx context.Context, env *Env, args []string) (*Response, error) {
	// 同一用户只允许一个 GUI 实例。第二个进程在装配数据库、适配器、HTTP 网关
	// 和托盘之前就退出，只负责把已有实例的主窗口唤到前台（Windows）；否则
	// 用户会看到第二个托盘，而且第二个进程的 HTTP 端口还会启动失败。
	if !acquireTrayInstance() {
		return nil, nil
	}

	c, err := parseCommonFlags("tray", args)
	if err != nil {
		return nil, err
	}

	built, err := c.buildApp(ctx, env)
	if err != nil {
		return nil, err
	}
	defer func() { _ = built.Close() }()

	// MCP HTTP 网关在后台运行；GUI 事件循环占据主 goroutine。
	var httpSrv *http.Server
	httpSrv, err = built.Serve(ctx, c.listenAddr())
	if err != nil {
		return nil, err
	}
	go func() { _ = httpSrv.ListenAndServe() }()

	// 双击即自安装：Windows 上建开始菜单快捷方式并注册 App Paths（开始菜单 /
	// Win+R / `start QKA` 都能找到），其它平台是 no-op。已经通过安装向导装过
	// 时，快捷方式与 App Paths 始终指向安装目录里的程序：用户再次双击下载目录
	// 里的 exe 只进托盘，不会把快捷方式劫持回下载位置。快捷方式只是便利不是
	// 运行前提：失败只记一行日志，不阻塞 GUI 启动。
	exe, _ := executablePath()
	if record, ok := loadInstallRecord(env.configDir()); ok {
		exe = record.ExePath
	}
	if err := ensureSelfInstall(env.configDir(), exe, Version); err != nil {
		log.Printf("qka: 自安装未完成（不影响启动）: %v", err)
	}

	if runErr := runTrayGUI(built); runErr != nil {
		_ = httpSrv.Shutdown(context.Background())
		return nil, runErr
	}
	_ = httpSrv.Shutdown(context.Background())
	return nil, nil
}
