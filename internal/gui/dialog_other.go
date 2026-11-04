//go:build !windows

package gui

// ShowFatalDialog 在非 Windows 平台不弹原生窗口：安装向导只在 Windows 的
// GUI 发行构建里出现，这里的调用路径实际不可达；保持空实现让跨平台构建与
// 单测不受影响。
func ShowFatalDialog(string, string) {}
