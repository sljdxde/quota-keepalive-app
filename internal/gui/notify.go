package gui

import (
	"context"
	"os/exec"
	"strconv"

	"qka/internal/procattr"
)

// Notifier 发送系统通知。
type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// NoopNotifier 不发送任何通知（默认关闭时使用）。
type NoopNotifier struct{}

// Notify 实现 Notifier，直接返回。
func (NoopNotifier) Notify(context.Context, string, string) error { return nil }

// MacNotifier 用 osascript display notification 发送 macOS 系统通知。
type MacNotifier struct {
	run func(ctx context.Context, name string, args ...string) error
}

// NewMacNotifier 组装 macOS 通知器。
func NewMacNotifier() *MacNotifier {
	return &MacNotifier{
		run: func(ctx context.Context, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			// 通知命令是后台子进程；Windows 的 GUI 版 QKA 没有控制台，不隐藏
			// 会弹一个黑窗口（macOS 上 Hide 是空操作）。
			procattr.Hide(cmd)
			return cmd.Run()
		},
	}
}

// Notify 发送一条带标题的系统通知。
func (n *MacNotifier) Notify(ctx context.Context, title, body string) error {
	script := "display notification " + strconv.Quote(body) +
		" with title " + strconv.Quote(title)
	return n.run(ctx, "osascript", "-e", script)
}
