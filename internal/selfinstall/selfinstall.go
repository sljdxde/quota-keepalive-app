// Package selfinstall 让 Windows 上单文件发行的 QKA「首次运行即安装」。
//
// 发行物是自包含的一个 exe，没有 zip、没有安装器，所以第一次双击运行就是
// 安装：在用户开始菜单里建一个指向该 exe 的快捷方式，并把 exe 注册到当前
// 用户的 App Paths，于是开始菜单、Windows 搜索、Win+R 与 `start QKA` 都能
// 找到它。设计参考 magpie 的 internal/shortcut：
//
//   - 只在当前用户下动东西（开始菜单 + HKCU），不需要管理员权限；
//   - 幂等：装过一次就记在 <configDir>/selfinstall.json。用户删掉快捷方式后
//     不再每次重建；exe 被移动后把快捷方式和 App Paths 指回新位置；重复
//     Ensure 不会重复写状态文件或注册表；
//   - 失败只返回错误，不 panic，由调用方记一行日志后继续启动 GUI（快捷方式
//     只是便利，不是运行前提）。
//
// 非 Windows 平台没有「开始菜单」这件事，Ensure 是 no-op；macOS 的 QKA 在
// .app 里，安装与卸载由 DMG/skill 负责。
package selfinstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"qka/internal/applog"
	"qka/internal/atomicfile"
)

const (
	// shortcutName 是开始菜单快捷方式的名字，也是 Windows 搜索显示的名字。
	shortcutName = "Quota Keepalive"
	// stateName 是配置目录下记录「已经装过」的状态文件名。
	stateName = "selfinstall.json"
	// schemaVersion 是状态文件的格式号；格式变化时递增，不认识的旧格式按
	// 「没装过」处理，由下一次 Ensure 重新建立。
	schemaVersion = 1
)

// Portable 判定 exePath 是否属于便携版：数据目录与 exe 同放、必须不在机器
// 上留下任何痕迹。QKA 目前没有便携发行物，所以恒为 false，Windows 实现因此
// 总会安装；这个接缝留给将来的便携版。
func Portable(_ string) bool { return false }

// system 是 Ensure 需要操作系统提供的动作；Windows 实现见 selfinstall_windows.go，
// 测试注入假实现来验证调用序列（mac 上没有开始菜单，只能用假实现跑状态机）。
type system struct {
	// startMenuDir 返回当前用户开始菜单的 Programs 目录。
	startMenuDir func() (string, error)
	// writeShortcut 让 lnk 指向 exe；已经指向时不做写操作。
	writeShortcut func(lnk, exe string) error
	// appPath 读出 App Paths 里已注册的 exe 与所在目录；没有时 ok=false。
	appPath func() (exe, dir string, ok bool)
	// registerAppPath 把 exe 与所在目录写进当前用户的 App Paths。
	registerAppPath func(exe string) error
}

// errNoStartMenu 表示当前平台没有开始菜单（非 Windows）。
var errNoStartMenu = errors.New("selfinstall: 当前平台没有开始菜单")

// writeStateFile 把状态文件原子落盘；测试替换它以覆盖写失败路径。
var writeStateFile = func(path string, data []byte) error {
	return atomicfile.WriteFile(path, data, 0o600)
}

// state 是 <configDir>/selfinstall.json 的内容。
type state struct {
	// Schema 是格式号，当前为 schemaVersion。
	Schema int `json:"schema"`
	// ExePath 是上次安装时 exe 的绝对路径；变化说明 exe 被移动了。
	ExePath string `json:"exe_path"`
	// Version 是上次安装时的运行版本（qka/internal/cli.Version）。
	Version string `json:"version"`
	// ShortcutCreated 记录我们建过快捷方式：用户删掉后不再重建。
	ShortcutCreated bool `json:"shortcut_created"`
}

// ensure 是 Ensure 的平台无关主体：校验入参、读状态、按需注册 App Paths、
// 创建/修复/跳过快捷方式，最后只在内容变化时写状态文件。
func ensure(configDir, exePath, version string, sys system) (err error) {
	// 失败统一收口到一处：自安装只是快捷方式便利，失败不该静默，但也绝不能
	// 阻塞启动，调用方只记日志后继续跑 GUI。
	defer func() {
		if err != nil {
			applog.Errorf("selfinstall", "自安装失败（不影响启动）：%v", err)
		}
	}()
	if strings.TrimSpace(configDir) == "" {
		return errors.New("selfinstall: 配置目录为空")
	}
	if strings.TrimSpace(exePath) == "" {
		return errors.New("selfinstall: exe 路径为空")
	}
	exe := filepath.Clean(exePath)
	applog.Debugf("selfinstall", "开始自安装 configDir=%s exe=%s", configDir, exe)

	// 配置目录可能还不存在（真正的第一次双击运行）。先建出来，后面读/写
	// 状态文件才有落脚点；顺带把「目录不可写」这类失败尽早报出去。
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("selfinstall: 创建配置目录: %w", err)
	}

	lnkDir, err := sys.startMenuDir()
	if err != nil {
		if errors.Is(err, errNoStartMenu) {
			applog.Debugf("selfinstall", "当前平台没有开始菜单，跳过自安装")
			return nil
		}
		return fmt.Errorf("selfinstall: 定位开始菜单: %w", err)
	}
	lnk := filepath.Join(lnkDir, shortcutName+".lnk")
	statePath := filepath.Join(configDir, stateName)

	st, found, err := loadState(statePath)
	if err != nil {
		return err
	}

	// App Paths：首次运行、exe 移动后、或用户清理过注册表时补上。值已经
	// 正确时不再写，重复 Ensure 不产生注册表副作用。
	wantDir := filepath.Dir(exe)
	if curExe, curDir, ok := sys.appPath(); !ok || !samePath(curExe, exe) || !samePath(curDir, wantDir) {
		if err := sys.registerAppPath(exe); err != nil {
			return fmt.Errorf("selfinstall: 注册 App Paths: %w", err)
		}
		applog.Infof("selfinstall", "已注册 App Paths: %s", exe)
	}

	created := st.ShortcutCreated
	switch {
	case fileExists(lnk):
		// 快捷方式还在：把它指回当前 exe（已经指向时底层实现不动它）。
		if err := sys.writeShortcut(lnk, exe); err != nil {
			return fmt.Errorf("selfinstall: 修复开始菜单快捷方式: %w", err)
		}
		applog.Infof("selfinstall", "已修复开始菜单快捷方式: %s", lnk)
		created = true
	case found && st.ShortcutCreated:
		// 用户删掉了我们建过的快捷方式：保持删除，不再重建。
		applog.Debugf("selfinstall", "用户已删除开始菜单快捷方式，保持不重建")
	default:
		// 第一次运行（或状态文件丢失）：真正把快捷方式建出来。
		if err := os.MkdirAll(lnkDir, 0o755); err != nil {
			return fmt.Errorf("selfinstall: 创建开始菜单目录: %w", err)
		}
		if err := sys.writeShortcut(lnk, exe); err != nil {
			return fmt.Errorf("selfinstall: 创建开始菜单快捷方式: %w", err)
		}
		applog.Infof("selfinstall", "已创建开始菜单快捷方式: %s", lnk)
		created = true
	}

	next := state{Schema: schemaVersion, ExePath: exe, Version: version, ShortcutCreated: created}
	if found && next == st {
		applog.Debugf("selfinstall", "自安装状态未变化，跳过写入")
		return nil // 内容没变：不重写状态文件，保证重复 Ensure 无副作用
	}
	data, _ := json.MarshalIndent(next, "", "  ") // 只有 JSON 原生类型，不会失败
	data = append(data, '\n')
	if err := writeStateFile(statePath, data); err != nil {
		return fmt.Errorf("selfinstall: 写状态文件: %w", err)
	}
	applog.Infof("selfinstall", "自安装完成 version=%s", version)
	return nil
}

// loadState 读取状态文件。文件不存在、内容损坏或格式号不认识时按「没装过」
// 处理（found=false），而不是让自安装永远失败——损坏的状态文件不该阻止 QKA
// 启动，也不该让快捷方式永远缺失。
func loadState(path string) (state, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state{}, false, nil
	}
	if err != nil {
		return state{}, false, fmt.Errorf("selfinstall: 读取状态文件: %w", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, false, nil
	}
	if st.Schema != schemaVersion {
		return state{}, false, nil
	}
	return st, true, nil
}

// fileExists 报告 path 是否存在（捷径文件是一个普通文件）。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// samePath 判断两个路径是否同一个。Windows 路径大小写不敏感，且末尾分隔符
// 不影响身份，所以统一 Clean 后 EqualFold；本包只服务于 Windows 语义。
func samePath(a, b string) bool {
	return a != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// stale 报告一个指向 target、工作目录为 dir 的快捷方式是否不能再用：还没有
// 快捷方式，或者 exe 被移动了。目录也要比较：exe 同名换目录同样要重写。
func stale(target, dir, exe string) bool {
	return !samePath(target, exe) || !samePath(dir, filepath.Dir(exe))
}
