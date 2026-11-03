//go:build !windows

package applog

import (
	"fmt"
	"os"
)

// showCrashNotice 在非 Windows 平台没有原生弹窗：panic 信息已经写进
// crash 文件，桌面端从 Finder 启动也看不到 stderr，这里把它打到标准错误，
// 供从终端启动时排查。保持与 Windows 实现相同的签名。
func showCrashNotice(logDir string) {
	fmt.Fprintf(os.Stderr, "qka: 未处理的 panic，诊断日志在 %s\n", logDir)
}
