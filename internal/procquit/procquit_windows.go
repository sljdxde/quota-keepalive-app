//go:build windows

package procquit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// listenMu 保护 handler；等待循环在独立 goroutine 里读它。
	listenMu sync.Mutex
	handler  func()
)

// Listen 创建命名事件并等待它；每次收到信号调用一次 handler，返回停止函数。
// 创建失败（命名对象权限异常等）时返回 no-op：不能因为「请退出」通道不可用
// 就挡住 GUI 启动。
func Listen(cb func()) (stop func()) {
	event, err := windows.CreateEvent(nil, 0, 0, windows.StringToUTF16Ptr(QuitEventName))
	if err != nil {
		if event != 0 {
			_ = windows.CloseHandle(event)
		}
		return func() {}
	}

	listenMu.Lock()
	handler = cb
	listenMu.Unlock()

	done := make(chan struct{})
	go func() {
		for {
			state, err := windows.WaitForSingleObject(event, windows.INFINITE)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				return
			}
			listenMu.Lock()
			current := handler
			listenMu.Unlock()
			if current != nil {
				current()
			}
			select {
			case <-done:
				return
			default:
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			listenMu.Lock()
			handler = nil
			listenMu.Unlock()
			_ = windows.CloseHandle(event)
		})
	}
}

// Signal 通知正在运行的实例退出；没有监听者（旧构建或未启动）时返回 false。
func Signal() bool {
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false,
		windows.StringToUTF16Ptr(QuitEventName))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(event) }()
	return windows.SetEvent(event) == nil
}

// TerminateByImage 按可执行文件的绝对路径找到同路径的进程并结束它们，返回
// 被结束的 PID。用于没有实现退出通道的旧构建：安装向导不能一直卡在
// 「旧版本正在运行」上等用户手动点退出。
//
// 只匹配路径完全相同的进程，绝不误杀当前进程（升级向导自己就在跑另一个路径
// 的 exe；若路径相同则调用方根本不会走到这里）。
func TerminateByImage(exePath string) ([]uint32, error) {
	canonical, err := filepath.Abs(exePath)
	if err != nil {
		return nil, err
	}
	self := uint32(os.Getpid())

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}

	var killed []uint32
	for {
		pid := entry.ProcessID
		if pid != self {
			if image, err := imagePath(pid); err == nil && sameFile(image, canonical) {
				if err := terminate(pid); err == nil {
					killed = append(killed, pid)
				}
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return killed, nil
}

// imagePath 返回进程可执行文件的完整路径。
func imagePath(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

// terminate 结束指定进程并等待句柄可用（最多 3 秒）。
func terminate(pid uint32) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	if err := windows.TerminateProcess(handle, 0); err != nil {
		return err
	}
	state, err := windows.WaitForSingleObject(handle, uint32((3 * time.Second).Milliseconds()))
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 {
		return errors.New("等待旧进程退出超时")
	}
	return nil
}

// sameFile 比较两个路径是否指向同一个文件（大小写不敏感，Windows 惯例）。
func sameFile(a, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	return strings.EqualFold(cleanA, cleanB)
}
