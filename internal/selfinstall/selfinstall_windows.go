//go:build windows

package selfinstall

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// appPathName 是 App Paths 里注册的名字。Win+R、开始菜单搜索与 `start QKA`
// 都按这个名字查找可执行文件，所以用固定的 QKA.exe，而不是发行物下载后的
// 文件名（用户可能把 exe 改名成 QKA-0.1.16-windows-amd64.exe 后双击）。
const appPathName = "QKA.exe"

// appPathKey 是当前用户（HKCU）的 App Paths 项：写这里不需要管理员权限。
const appPathKey = `Software\Microsoft\Windows\CurrentVersion\App Paths\` + appPathName

// Ensure 完成 Windows 上的自安装：开始菜单快捷方式 + HKCU App Paths。
// 便携版直接跳过；失败返回错误，由调用方记一行日志后继续启动 GUI。
func Ensure(configDir, exePath, version string) error {
	if Portable(exePath) {
		return nil
	}
	return ensure(configDir, exePath, version, defaultSystem())
}

// defaultSystem 把真实的 Windows 动作接进平台无关的 ensure。
func defaultSystem() system {
	return system{
		startMenuDir:    startMenuDir,
		writeShortcut:   writeShortcut,
		appPath:         appPathValues,
		registerAppPath: registerAppPath,
	}
}

// startMenuDir 返回当前用户开始菜单的 Programs 目录
// （通常即 %APPDATA%\Microsoft\Windows\Start Menu\Programs）。
func startMenuDir() (string, error) {
	if dir, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_CREATE); err == nil && dir != "" {
		return dir, nil
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errNoStartMenu
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs"), nil
}

// writeShortcut 让 lnk 这个 .lnk 指向 exe：已经指向时不写，否则重写。用的是
// Windows 自带的 WScript.Shell（底层即 IShellLink），和 magpie 一样通过
// go-ole/oleutil 调 COM，不引入额外依赖。
func writeShortcut(lnk, exe string) error {
	// COM 按线程初始化；锁住当前线程，保证 CoInitialize/CoUninitialize 与
	// 后面的 COM 调用在同一个套间里完成。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	balance := false
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		var oleErr *ole.OleError
		switch {
		case !errors.As(err, &oleErr):
			return err
		case oleErr.Code() == 1: // S_FALSE：本线程之前已初始化，仍需配平一次 Uninitialize
			balance = true
		case oleErr.Code() == 0x80010106: // RPC_E_CHANGED_MODE：以其它套间初始化过，仍可用
		default:
			return err
		}
	} else {
		balance = true
	}
	if balance {
		defer ole.CoUninitialize()
	}

	unknown, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return err
	}
	defer unknown.Release()
	shell, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()

	link, err := oleutil.CallMethod(shell, "CreateShortcut", lnk)
	if err != nil {
		return err
	}
	// CallMethod 返回的 VARIANT 持有对象引用；ToIDispatch 不加引用，所以
	// 由 sc.Release 接管这一份，不能再 Clear 这个 VARIANT（否则重复释放）。
	sc := link.ToIDispatch()
	if sc == nil {
		return errors.New("selfinstall: WScript.Shell 没有返回快捷方式对象")
	}
	defer sc.Release()

	// 读出现有目标与工作目录；读不到就当作需要重写。
	get := func(name string) string {
		v, err := oleutil.GetProperty(sc, name)
		if err != nil {
			return ""
		}
		defer func() { _ = v.Clear() }()
		return v.ToString()
	}
	if !stale(get("TargetPath"), get("WorkingDirectory"), exe) {
		return nil
	}

	props := []struct {
		name string
		val  any
	}{
		{"TargetPath", exe},
		{"WorkingDirectory", filepath.Dir(exe)},
		{"IconLocation", exe + ",0"},
		{"Description", shortcutName},
	}
	for _, p := range props {
		if _, err := oleutil.PutProperty(sc, p.name, p.val); err != nil {
			return err
		}
	}
	if _, err := oleutil.CallMethod(sc, "Save"); err != nil {
		return err
	}
	return nil
}

// appPathValues 读出 App Paths 里已注册的 exe 与所在目录；没有时为 ok=false。
func appPathValues() (exe, dir string, ok bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, appPathKey, registry.QUERY_VALUE)
	if err != nil {
		return "", "", false
	}
	defer key.Close()
	if exe, _, err = key.GetStringValue(""); err != nil {
		return "", "", false
	}
	if dir, _, err = key.GetStringValue("Path"); err != nil {
		return "", "", false
	}
	return exe, dir, true
}

// registerAppPath 把 exe 写进当前用户的 App Paths：默认值=exe 绝对路径，
// Path=所在目录。这样 Win+R 里敲 QKA、或命令行 `start QKA` 都能找到它。
func registerAppPath(exe string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, appPathKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetStringValue("", exe); err != nil {
		return err
	}
	return key.SetStringValue("Path", filepath.Dir(exe))
}
