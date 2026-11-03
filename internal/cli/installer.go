package cli

import (
	"fmt"

	"qka/internal/gui"
	"qka/internal/installer"
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
)

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
