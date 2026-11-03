package cli

import (
	"os"
	"path/filepath"
	"runtime"
)

// 运行环境接缝：测试可替换，用来覆盖 Windows 与「没有 HOME」的分支。
var (
	runtimeGOOS   = runtime.GOOS
	userConfigDir = os.UserConfigDir
)

// ResolveConfigDir 返回当前生效的配置目录。
//
// 优先级：QKA_CONFIG_DIR > 平台默认位置。Windows 上 HOME 通常不存在，
// 所以不能退回 filepath.Join("", ".qka") 这种相对路径——那会把配置与
// 内嵌运行时解压到进程 cwd（从快捷方式启动时甚至是 System32）。Windows
// 用 %LOCALAPPDATA%\QKA（体积大的运行时不该进漫游配置），拿不到时退回
// os.UserConfigDir；其它平台保持 $HOME/.qka 不变。
func ResolveConfigDir(getenv func(string) string) string {
	if override := getenv("QKA_CONFIG_DIR"); override != "" {
		return override
	}
	if runtimeGOOS == "windows" {
		if dir := getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "QKA")
		}
		if dir, err := userConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "QKA")
		}
	}
	return filepath.Join(getenv("HOME"), ".qka")
}
