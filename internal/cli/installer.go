package cli

import (
	"errors"
	"fmt"

	"qka/internal/gui"
	"qka/internal/installer"
	"qka/internal/runtimebundle"
)

// 安装向导模式的接缝：默认指向真实实现，测试替换它们以避免在单测里进入
// Wails 事件循环、弹原生窗口。
var (
	// planInstall 判定本次启动应进入首次安装、升级还是直接进托盘。
	planInstall = installer.Plan
	// newInstallerWizard 创建安装向导窗口。
	newInstallerWizard = gui.NewInstaller
	// runInstallWizardApp 进入安装向导事件循环（方法表达式，便于替换）。
	runInstallWizardApp = (*gui.Installer).Run
	// loadInstallRecord 读取安装记录：托盘启动时用它把快捷方式固定在安装目录。
	loadInstallRecord = installer.Load
	// runtimeReady 报告本次构建是否内嵌了运行时。GUI 发行物必须内嵌：否则
	// 向导会一路走到「解压运行时」才失败，用户只看到「安装失败」却不知道
	// 是构建方式不对（开发构建没加 -tags embeddedruntime）。
	runtimeReady = runtimebundle.Available
	// showInstallBlocked 把「无法安装」的原因直接弹给用户。windowsgui 没有
	// 可见的 stderr，这里必须用原生弹窗，否则双击后看起来毫无反应。
	showInstallBlocked = gui.ShowFatalDialog
)

// errNoEmbeddedRuntime 是「GUI 版但没内嵌运行时」的明确诊断：这种 exe 无法
// 安装，必须换用发行构建方式重新构建，而不是让用户在向导里重试。
var errNoEmbeddedRuntime = errors.New(
	"这个 QKA 构建没有内嵌运行时，无法安装：请下载官方发行版，" +
		"或用 make build-windows / scripts/build-windows-exe.sh 重新构建" +
		"（它们会内嵌 Bun、adapters 与 provider-plugins）")

// installerPlan 判定这次无参数启动应进入哪种形态。
//
// 只有同时满足「Windows、GUI 发行构建、无参数、未显式指定 QKA_CONFIG_DIR」才会
// 去问 installer.Plan：install → 首次安装向导，upgrade → 升级向导，none → 托盘。
// 任何带参数的调用、以及设置了 QKA_CONFIG_DIR 的开发/自动化场景都直接 none，
// 避免误触安装向导。
func installerPlan(env *Env) installer.Mode {
	if runtimeGOOS != "windows" || BuildFlavor != "gui" {
		return installer.ModeNone
	}
	if len(env.Args) != 0 || env.Getenv == nil {
		return installer.ModeNone
	}
	if env.Getenv("QKA_CONFIG_DIR") != "" {
		return installer.ModeNone
	}
	selfPath, err := executablePath()
	if err != nil {
		return installer.ModeNone
	}
	return planInstall(env.configDir(), selfPath)
}

// runInstallWizard 创建并运行安装向导，阻塞到用户完成安装或关闭窗口。
func runInstallWizard(env *Env) error {
	// 先确认这次构建能安装：没内嵌运行时的 GUI 构建（漏了 -tags embeddedruntime）
	// 在「解压运行时」一步必然失败，与其让用户等到最后一步，不如立刻给出原因与
	// 修法。安装目录此时还没有被写入，不会留下半成品。
	if !runtimeReady() {
		showInstallBlocked("Quota Keepalive 无法安装", errNoEmbeddedRuntime.Error())
		return errNoEmbeddedRuntime
	}
	selfPath, err := executablePath()
	if err != nil {
		return fmt.Errorf("定位当前程序失败：%w", err)
	}
	wizard := newInstallerWizard(gui.InstallerDeps{
		ConfigDir: env.configDir(),
		Version:   Version,
		SelfPath:  selfPath,
	})
	return runInstallWizardApp(wizard)
}
