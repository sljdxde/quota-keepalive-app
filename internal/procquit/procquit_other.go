//go:build !windows

package procquit

// Listen 在非 Windows 平台是 no-op：没有命名事件通道，macOS 由 LaunchServices
// 保证单实例，重复打开走 reopen 事件。
func Listen(func()) func() { return func() {} }

// Signal 在非 Windows 平台没有跨进程通道。
func Signal() bool { return false }

// TerminateByImage 在非 Windows 平台不结束任何进程：安装向导只在 Windows 单
// 文件发行物里出现，这里保持签名一致以便跨平台编译与单测。
func TerminateByImage(string) ([]uint32, error) { return nil, nil }
