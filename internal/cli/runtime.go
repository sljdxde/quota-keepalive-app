package cli

import (
	"os"
	"os/exec"
	"path/filepath"

	"qka/internal/portsel"
	"qka/internal/runtimebundle"
)

var (
	workingDir = os.Getwd
	lookPath   = exec.LookPath
)

// 内嵌运行时的接入点：发布构建（-tags embeddedruntime）里会把 Bun、adapters/
// 与 provider-plugins/ 解压到 <configDir>/runtime/<Version>/，开发构建里
// Available 恒为 false，行为与以前完全一致。
//
// 这两个变量同样作为测试接缝，用来覆盖「内嵌运行时优先」与「解压失败回退」。
var (
	runtimeAvailable = runtimebundle.Available
	runtimeEnsure    = runtimebundle.Ensure
)

// resolveEmbeddedRuntime 返回内嵌运行时解压后的路径；本次构建没有内嵌资源或
// 解压失败时返回 false，调用方继续走旧的文件布局。失败只降级，不 panic。
var resolveEmbeddedRuntime = func(env *Env) (runtimebundle.Paths, bool) {
	if !runtimeAvailable() {
		return runtimebundle.Paths{}, false
	}
	paths, err := runtimeEnsure(env.configDir(), Version)
	if err != nil {
		return runtimebundle.Paths{}, false
	}
	return paths, true
}

// defaultPort 是 serve 未指定端口时的监听端口（与 portsel 一致）。
const defaultPort = portsel.DefaultPort

// resolveAdaptersRoot 返回适配器父目录：
// 显式覆盖 > 环境变量 > 内嵌运行时 > 可执行文件同级 > cwd。
func resolveAdaptersRoot(env *Env, override string) string {
	if override != "" {
		return override
	}
	if root := env.Getenv("QKA_ADAPTERS_DIR"); root != "" {
		return root
	}
	if paths, ok := resolveEmbeddedRuntime(env); ok && paths.AdaptersRoot != "" {
		return paths.AdaptersRoot
	}
	if exe, err := executablePath(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "adapters")
		if isDir(cand) {
			return cand
		}
	}
	if cwd, err := workingDir(); err == nil {
		cand := filepath.Join(cwd, "adapters")
		if isDir(cand) {
			return cand
		}
	}
	return "adapters"
}

// resolveBun 返回 Bun 路径：
// 显式覆盖 > 环境变量 > 内嵌运行时 > 可执行文件同级 > PATH。
func resolveBun(env *Env, override string) string {
	if override != "" {
		return override
	}
	if p := env.Getenv("QKA_BUN"); p != "" {
		return p
	}
	if paths, ok := resolveEmbeddedRuntime(env); ok && paths.Bun != "" {
		return paths.Bun
	}
	if exe, err := executablePath(); err == nil {
		if bun := siblingBun(filepath.Dir(exe)); bun != "" {
			return bun
		}
	}
	if p, err := lookPath("bun"); err == nil {
		return p
	}
	return "bun"
}

// resolveProviderPluginsRoot finds the bundled OpenCode provider plugins:
// 显式覆盖 > QKA_PROVIDER_PLUGINS_DIR > 内嵌运行时 > 可执行文件同级/Resources > cwd。
// QKA_PROVIDER_PLUGINS_DIR may point at a development copy.
func resolveProviderPluginsRoot(env *Env, override string) string {
	if override != "" {
		return override
	}
	if root := env.Getenv("QKA_PROVIDER_PLUGINS_DIR"); root != "" {
		return root
	}
	if paths, ok := resolveEmbeddedRuntime(env); ok && paths.PluginsRoot != "" {
		return paths.PluginsRoot
	}
	if exe, err := executablePath(); err == nil {
		base := filepath.Dir(exe)
		for _, candidate := range []string{
			filepath.Join(base, "provider-plugins"),
			filepath.Join(base, "..", "Resources", "provider-plugins"),
		} {
			if isDir(candidate) {
				return filepath.Clean(candidate)
			}
		}
	}
	if cwd, err := workingDir(); err == nil {
		candidate := filepath.Join(cwd, "provider-plugins")
		if isDir(candidate) {
			return candidate
		}
	}
	return ""
}

func siblingBun(dir string) string {
	for _, name := range []string{"bun", "bun.exe"} {
		candidate := filepath.Join(dir, name)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
