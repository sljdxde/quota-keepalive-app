//go:build !windows

package procattr

import "os/exec"

// hide 什么都不做：只有 Windows 会给子进程一个自己的窗口。
func hide(*exec.Cmd) {}
