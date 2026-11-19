//go:build windows

package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const (
	runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	// runName 是 HKCU Run 的值名。任务管理器的「启动」页直接显示这个名字，
	// 所以必须是产品名，而不是旧缩写。
	runName = "Quota Keepalive"
	// legacyRunName 是改名前的值名。旧版本写下的这条仍然会开机启动，改名后
	// 如果不清掉，任务管理器里会同时出现「QKA」和「Quota Keepalive」两条。
	legacyRunName = "QKA"
)

// On writes the current user's HKCU Run value. Windows ignores homeDir; the
// parameter keeps the cross-platform API symmetric with macOS.
func On(_ string, programPath, _ string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	// 先清掉改名前的旧值，避免开机时被拉起两次（单实例互斥只会让后启动的
	// 那次静默退出，但启动项里会一直留着一条不再更新的记录）。
	if err := key.DeleteValue(legacyRunName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	// Windows file names cannot contain a double quote, so wrapping the path
	// is sufficient and avoids Go string escaping leaking doubled backslashes.
	return key.SetStringValue(runName, `"`+programPath+`" `+Arg)
}

// Off removes the Run value(s). A missing key or value is already off.
func Off(_ string, _ string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	for _, name := range []string{runName, legacyRunName} {
		if err := key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return nil
}

// IsEnabled reports whether a Run value written by this app exists. The legacy
// name counts: an old install that never re-saved settings still autostarts.
func IsEnabled(_ string, _ string) bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	for _, name := range []string{runName, legacyRunName} {
		if _, _, err := key.GetStringValue(name); err == nil {
			return true
		}
	}
	return false
}
