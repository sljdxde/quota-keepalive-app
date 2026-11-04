// detect.go 实现 issue #8 的兜底探测：install.json 丢失/损坏时不能直接断定
// 「没装过」，否则用户手删配置目录、文件写坏或跨机器拷贝后，安装向导会让用户
// 随便选个目录装出第二份 QKA，两个实例抢互斥体。这里按「成本从低到低、线索
// 从强到弱」做三次探测，命中任一就按升级/修复处理，安装目录锁定在探测到的
// 位置，不再让用户改。
package installer

import (
	"path/filepath"
	"strings"

	"qka/internal/applog"
	"qka/internal/selfinstall"
)

// shortcutLnkName 是开始菜单快捷方式的文件名，与 selfinstall 包里的
// shortcutName（"Quota Keepalive"，未导出）保持一致——两边都用注释锚定，
// 改名时需同步。
const shortcutLnkName = "Quota Keepalive.lnk"

// probeSystem 是探测需要的系统动作。生产为真实实现（defaultProbe），测试注入
// 假实现覆盖命中路径 1/2/3、全未命中与异常分支——探测逻辑本身是平台无关的
// 纯函数，mac/linux 上也能全量测试。
type probeSystem struct {
	// localAppData 返回 %LOCALAPPDATA%（探测默认安装目录用），拿不到为空串。
	localAppData func() string
	// appData 返回 %APPDATA%（定位开始菜单快捷方式用），拿不到为空串。
	appData func() string
	// fileExists 报告路径存在且是一个普通文件（目录不算已安装的程序）。
	fileExists func(path string) bool
	// appPathExe 读出 HKCU App Paths 里 QKA.exe 注册的路径；没有注册或读取
	// 失败为 ok=false。
	appPathExe func() (string, bool)
	// lnkTarget 解析 .lnk 快捷方式指向的目标本地路径；文件缺失/解析失败为
	// ok=false。
	lnkTarget func(lnk string) (string, bool)
}

// defaultProbe 组装生产实现：几个 os.Stat 加一次注册表读，个别情况下再读一
// 个 .lnk（纯二进制解析，不开 COM）。任何一步的底层错误都已经在实现里折算成
// 空串/false，不会向上传播。
func defaultProbe() probeSystem {
	return probeSystem{
		localAppData: func() string { return strings.TrimSpace(lookupEnv("LOCALAPPDATA")) },
		appData:      func() string { return strings.TrimSpace(lookupEnv("APPDATA")) },
		fileExists:   dirExistsFile,
		appPathExe:   selfinstall.AppPathExe,
		lnkTarget:    selfinstall.ShortcutTarget,
	}
}

// detectInstall 按优先级探测既有的安装，返回探测到的已安装 exe 完整路径；全部
// 未命中返回空串（= 全新安装）。三条线索按「最常见、最可信」的顺序：
//
//  1. %LOCALAPPDATA%\Programs\Quota Keepalive\QKA.exe——默认安装目录（与
//     resolveDefaultDir 同一套路径推导），绝大多数安装落在这里；
//  2. 注册表 HKCU\...\App Paths\QKA.exe 指向的路径存在——用户自定义目录安装
//     后注册的，指向即安装位置；
//  3. 开始菜单快捷方式 Quota Keepalive.lnk 指向的目标存在——前两条都被清理
//     过时的最后线索。这里完整解析 .lnk 的目标（纯二进制读取，成本就是一次
//     文件读），而不是降级成「.lnk 文件存在即命中」：光知道文件存在拿不到
//     可以锁定的安装目录，把升级错锁到默认目录反而可能再装出第二份，与探测
//     的初衷相悖。
//
// 线索 2/3 的目标文件名必须是固定的 QKA.exe（大小写不敏感）：安装动作永远把
// 程序复制成 ExeName，发行物原名（QKA-0.1.16-windows-amd64.exe 之类）不算
// 安装位置——这样也顺带把开发机上「跑过一次未改名的构建产物」的残留排除在
// 探测之外。
//
// 每条线索内部的各种失败（环境变量缺失、注册表打不开、.lnk 解析不了、目标
// 文件不在了）都折算成本步未命中，继续试下一条；全部落空就是首次安装。
func detectInstall(p probeSystem) string {
	if lad := p.localAppData(); lad != "" {
		if exe := filepath.Join(lad, "Programs", defaultDirName, ExeName); p.fileExists(exe) {
			return exe
		}
	}
	if exe, ok := p.appPathExe(); ok && isInstalledExeName(exe) && p.fileExists(exe) {
		return filepath.Clean(exe)
	}
	if ad := p.appData(); ad != "" {
		lnk := filepath.Join(ad, "Microsoft", "Windows", "Start Menu", "Programs", shortcutLnkName)
		if target, ok := p.lnkTarget(lnk); ok && isInstalledExeName(target) && p.fileExists(target) {
			return filepath.Clean(target)
		}
	}
	return ""
}

// isInstalledExeName 报告 path 是否指向固定的安装文件名 QKA.exe（Windows 路径
// 大小写不敏感）。空路径与发行物原名（QKA-windows-amd64.exe 等）都不是。
func isInstalledExeName(path string) bool {
	return strings.TrimSpace(path) != "" && strings.EqualFold(filepath.Base(filepath.Clean(path)), ExeName)
}

// detectExistingInstall 是探测接缝：默认指向真实探测；测试注入假实现覆盖
// 命中/未命中/异常分支，避免单测依赖真机是否恰好装过 QKA。
var detectExistingInstall = func() string {
	return safeDetect(defaultProbe())
}

// safeDetect 给探测加兜底：任何异常（接缝 panic、越界等）都按「未探测到」
// 处理——探测只是锦上添花的修复信号，绝不能让它挡住安装向导。
func safeDetect(p probeSystem) (exePath string) {
	defer func() {
		if r := recover(); r != nil {
			applog.Warnf("installer", "探测既有安装出现异常，按首次安装处理：%v", r)
			exePath = ""
		}
	}()
	return detectInstall(p)
}

// DetectExistingInstall 探测本机是否留有既有安装（install.json 丢失/损坏时的
// 兜底），返回探测到的已安装 exe 完整路径；未探测到返回空串。向导用它展示
// 「检测到已安装版本（路径 X），将就地升级」。一次调用就是几个 os.Stat 加一次
// 注册表读（个别情况再读一个 .lnk），重复调用无副作用，拿不到已安装版本号——
// 探测线索里不含版本信息，由调用方按「未知/留空」处理。
func DetectExistingInstall() string {
	return detectExistingInstall()
}
