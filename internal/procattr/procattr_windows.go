//go:build windows

package procattr

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hide 让 cmd 的子进程拿到一个没有窗口的控制台。
//
// CREATE_NO_WINDOW 是关键：控制台程序照常启动，标准输入输出照常走管道，只是
// Windows 不给它显示控制台窗口，也不会给它的子进程（bun 内再拉起的进程）新建
// 可见控制台。HideWindow 再让子进程的第一个窗口按隐藏显示
// （STARTF_USESHOWWINDOW + SW_HIDE），挡住控制台窗口出现前的一瞬。
//
// 只增不改：调用方若已设置 CreationFlags 或其它 SysProcAttr 字段，原样保留。
func hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
