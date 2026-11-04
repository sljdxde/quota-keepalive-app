//go:build !windows

package selfinstall

// AppPathExe 在非 Windows 平台恒为「没有注册」：App Paths 是 Windows 独有的
// 程序查找机制。保留同一签名，installer 的探测代码就无需按平台分开接线。
func AppPathExe() (string, bool) {
	return "", false
}
