//go:build windows

package procattr

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideSetsNoWindowFlags(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ver")
	Hide(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("Hide 应设置 SysProcAttr")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatalf("Hide 应设置 HideWindow: %+v", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("Hide 应设置 CREATE_NO_WINDOW: %+v", cmd.SysProcAttr)
	}
	// 隐藏控制台不改变输出回收：子进程的 stdout 仍然经管道读得到。
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("隐藏控制台的子进程输出应仍可读: %q, %v", out, err)
	}
}

func TestHideKeepsExistingSysProcAttr(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ver")
	// 各调用点可能已经带了平台属性；Hide 只能追加，不能覆盖。
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	Hide(cmd)
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("已有的 CreationFlags 被覆盖: %#x", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("CREATE_NO_WINDOW 未并入: %#x", cmd.SysProcAttr.CreationFlags)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatalf("HideWindow 未设置: %+v", cmd.SysProcAttr)
	}
}

func TestHideIsIdempotent(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ver")
	Hide(cmd)
	flags := cmd.SysProcAttr.CreationFlags
	Hide(cmd)
	if cmd.SysProcAttr.CreationFlags != flags || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("重复调用不应改变属性: 首次 %#x，再次 %#x", flags, cmd.SysProcAttr.CreationFlags)
	}
}
