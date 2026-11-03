//go:build windows

package gui

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// instanceMutexName 是 GUI 单实例的命名互斥体。Local\ 前缀让它按登录会话
	// 隔离：同一用户在同一会话里无论双击多少次，只有一个 QKA 托盘。
	instanceMutexName = `Local\QuotaKeepalive.GUI.Instance`
	// instanceEventName 是唤醒已有实例主窗口的命名事件。
	instanceEventName = `Local\QuotaKeepalive.GUI.ShowMain`
	// instanceWakeWait 是第二个实例等待第一个实例建好事件的上限。
	instanceWakeWait = time.Second
)

var (
	instanceMu    sync.Mutex
	instanceWake  func()
	instanceMutex windows.Handle // 进程生命周期内一直持有，进程退出时由系统回收
)

// AcquireGUIInstance 让当前进程成为唯一的 GUI 实例。已有实例在运行时返回
// false，并尽力让那个实例把主窗口显示到前台；调用方拿到 false 后应直接退出，
// 不要启动第二份数据库、适配器、HTTP 网关和系统托盘。
func AcquireGUIInstance() bool {
	handle, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(instanceMutexName))
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			_ = windows.CloseHandle(handle)
			if !signalExistingInstance() {
				warnAlreadyRunning()
			}
			return false
		}
		// 单实例保护自身失败（权限/命名对象异常等）时放行：不能因为保护
		// 机制反而把 GUI 挡在门外。
		return true
	}
	instanceMu.Lock()
	instanceMutex = handle
	instanceMu.Unlock()
	startInstanceWake()
	return true
}

// setInstanceWakeHandler 注册第一个实例收到唤醒信号后执行的动作（显示主窗口）。
func setInstanceWakeHandler(handler func()) {
	instanceMu.Lock()
	instanceWake = handler
	instanceMu.Unlock()
}

// startInstanceWake 建命名事件并等待它；每收到一次信号就调用一次已注册的
// 唤醒动作，所以用户反复双击 exe 都能把主窗口叫回来。
func startInstanceWake() {
	event, err := windows.CreateEvent(nil, 0, 0, windows.StringToUTF16Ptr(instanceEventName))
	if err != nil {
		_ = windows.CloseHandle(event)
		return
	}
	go func() {
		for {
			state, err := windows.WaitForSingleObject(event, windows.INFINITE)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				return
			}
			instanceMu.Lock()
			handler := instanceWake
			instanceMu.Unlock()
			if handler != nil {
				handler()
			}
		}
	}()
}

// signalExistingInstance 通知已有实例显示主窗口。第一个实例刚拿到互斥体、
// 还没来得及建事件时有极短窗口期，所以短暂重试。
func signalExistingInstance() bool {
	deadline := time.Now().Add(instanceWakeWait)
	for {
		event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, windows.StringToUTF16Ptr(instanceEventName))
		if err == nil {
			defer func() { _ = windows.CloseHandle(event) }()
			return windows.SetEvent(event) == nil
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// warnAlreadyRunning 在唤醒通道不可用时给用户明确提示，而不是「双击毫无反应」。
func warnAlreadyRunning() {
	title := windows.StringToUTF16Ptr("Quota Keepalive")
	text := windows.StringToUTF16Ptr("Quota Keepalive 已在运行，请从系统托盘的 QKA 图标打开主窗口。")
	_, _ = windows.MessageBox(0, text, title,
		windows.MB_OK|windows.MB_ICONINFORMATION|windows.MB_SETFOREGROUND)
}
