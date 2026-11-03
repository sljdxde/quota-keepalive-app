//go:build !windows

package gui

import "testing"

// 非 Windows 平台不抢单实例：macOS 由 LaunchServices 保证同一个 .app 只有一个
// 进程；唤醒回调也保持 no-op。
func TestAcquireGUIInstanceAlwaysPrimaryOffWindows(t *testing.T) {
	if !AcquireGUIInstance() {
		t.Fatal("非 Windows 平台应始终视为唯一实例")
	}
	if !AcquireGUIInstance() {
		t.Fatal("重复调用也应成功")
	}
	setInstanceWakeHandler(func() { t.Error("非 Windows 平台不应有唤醒回调") })
	setInstanceWakeHandler(nil)
}
