//go:build !windows

package selfinstall

import "testing"

// 非 Windows 上探测线索恒为「没有」：探测代码可以无差别接线。
func TestAppPathExeIsMissOffWindows(t *testing.T) {
	if exe, ok := AppPathExe(); ok || exe != "" {
		t.Fatalf("非 Windows 的 AppPathExe 应为未命中，实际 %q/%v", exe, ok)
	}
}
