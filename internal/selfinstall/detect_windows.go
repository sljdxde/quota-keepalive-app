//go:build windows

package selfinstall

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// AppPathExe 读出 HKCU App Paths 里注册的 QKA.exe 路径（注册表项的默认值）。
// 给 installer 在 install.json 丢失后探测「是否装过」用：没有注册或读取失败
// 时 ok=false——探测语义下任何错误都只是一条「无线索」，不向上传播、不阻塞
// 安装向导。
func AppPathExe() (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, appPathKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()
	exe, _, err := key.GetStringValue("")
	if err != nil || strings.TrimSpace(exe) == "" {
		return "", false
	}
	return exe, true
}
