//go:build !windows

package procattr

import (
	"os/exec"
	"testing"
)

func TestHideIsNoopElsewhere(t *testing.T) {
	cmd := exec.Command("echo", "hi")
	Hide(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatalf("非 Windows 平台不应设置 SysProcAttr: %+v", cmd.SysProcAttr)
	}
	Hide(nil) // 不应 panic
}
