//go:build windows

package scheduler

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// missingTaskGBK 是中文 Windows 上 `schtasks /Query /TN <不存在>` 的真实输出：
// 「错误: 系统找不到指定的文件。」按控制台代码页 CP936 编码后的字节。
// 旧实现按 UTF-8 比较「找不到」，因此永远识别不出任务不存在。
var missingTaskGBK = []byte{
	0xb4, 0xed, 0xce, 0xf3, 0x3a, 0x20,
	0xcf, 0xb5, 0xcd, 0xb3, 0xd5, 0xd2, 0xb2, 0xbb, 0xb5, 0xbd,
	0xd6, 0xb8, 0xb6, 0xa8, 0xb5, 0xc4, 0xce, 0xc4, 0xbc, 0xfe, 0xa1, 0xa3,
	0x0d, 0x0a,
}

func TestTaskMissingDetectsLocalizedConsoleOutput(t *testing.T) {
	if !taskMissing(missingTaskGBK) {
		t.Fatal("中文 Windows 的「系统找不到指定的文件」必须识别为任务不存在")
	}
	// 其它本地化错误不能被当成「任务不存在」，否则会掩盖真正的失败。
	other := []byte{0xb4, 0xed, 0xce, 0xf3, 0x3a, 0x20, 0xbe, 0xdc, 0xbe, 0xf8, 0xb7, 0xc3, 0xce, 0xca, 0xa1, 0xa3}
	if taskMissing(other) {
		t.Fatal("「拒绝访问」不能识别为任务不存在")
	}
}

func TestStatusTreatsLocalizedMissingTaskAsNotInstalled(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "schtasks" || len(args) < 2 || args[0] != "/Query" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return missingTaskGBK, errors.New("exit status 1")
	}
	s := newWindowsScheduler(run, nil)
	status, err := s.Status(context.Background(), Spec{ConfigDir: `C:\Users\test\.qka`})
	if err != nil {
		t.Fatalf("任务不存在不应让状态查询失败: %v", err)
	}
	if status.Installed {
		t.Fatalf("任务不存在时应报告未安装: %+v", status)
	}
}

func TestRemoveIgnoresLocalizedMissingTask(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "schtasks" || len(args) < 2 || args[0] != "/Delete" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return missingTaskGBK, errors.New("exit status 1")
	}
	s := newWindowsScheduler(run, nil)
	if err := s.Remove(context.Background(), Spec{ConfigDir: `C:\Users\test\.qka`}); err != nil {
		t.Fatalf("删除不存在的任务应视为成功: %v", err)
	}
}

// TestStatusAgainstRealLocalizedSchtasks 用真实的 schtasks 复核当前系统语言的
// 输出：临时配置目录派生出的任务名必然不存在，Status 必须报告「未安装」而不是
// 把本地化错误当成调度器故障。只查询，不创建/删除任何任务。
func TestStatusAgainstRealLocalizedSchtasks(t *testing.T) {
	if _, err := exec.LookPath("schtasks"); err != nil {
		t.Skipf("schtasks unavailable: %v", err)
	}
	s := New()
	status, err := s.Status(context.Background(), Spec{ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatalf("真实 schtasks 的任务不存在输出未被识别: %v", err)
	}
	if status.Installed {
		t.Fatalf("随机配置目录不应已有系统任务: %+v", status)
	}
}
