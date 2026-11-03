// Package procattr 给 QKA 后台启动的子命令补上平台属性，只有一件事：不让
// 子进程弹窗。
//
// Windows 上 QKA.exe 是 GUI 程序（链接参数 -H windowsgui，没有控制台）。
// Windows 遇到「没有控制台的进程启动控制台程序」时，会给子进程新建一个控制台
// ——也就是一个黑色窗口。于是 bun.exe、netstat、tasklist、schtasks、
// powershell.exe 每启动一次就弹一个窗口，标题还是子进程的路径。Hide 让这些
// 子进程在自己的、没有窗口的控制台里运行；其它平台不需要，是空操作。
//
// 只在「GUI 进程在后台 spawn」的位置调用：子进程的输出都走管道回收，不依赖
// 终端，隐藏控制台不改变任何行为。
package procattr

import "os/exec"

// Hide 标记 cmd，使其在 Windows 上启动的子进程不显示控制台窗口。cmd 上已经
// 设置过的 SysProcAttr 字段会保留（只增不改）；其它平台什么都不做。
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	hide(cmd)
}
