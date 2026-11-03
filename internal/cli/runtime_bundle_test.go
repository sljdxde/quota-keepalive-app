package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"qka/internal/runtimebundle"
)

// stubEmbedded 替换内嵌运行时接入点，返回恢复函数。
func stubEmbedded(t *testing.T, fn func(*Env) (runtimebundle.Paths, bool)) {
	t.Helper()
	old := resolveEmbeddedRuntime
	resolveEmbeddedRuntime = fn
	t.Cleanup(func() { resolveEmbeddedRuntime = old })
}

func TestResolveEmbeddedRuntimePriority(t *testing.T) {
	oldExecutable, oldWorkingDir, oldLookPath := executablePath, workingDir, lookPath
	oldAvailable, oldEnsure := runtimeAvailable, runtimeEnsure
	t.Cleanup(func() {
		executablePath, workingDir, lookPath = oldExecutable, oldWorkingDir, oldLookPath
		runtimeAvailable, runtimeEnsure = oldAvailable, oldEnsure
	})

	// 可执行文件同级放一套旧布局，作为「内嵌运行时之后」的一级。
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(filepath.Join(bin, "adapters"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bin, "provider-plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	siblingBunPath := filepath.Join(bin, "bun")
	if err := os.WriteFile(siblingBunPath, []byte("bun"), 0o700); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return filepath.Join(bin, "qka"), nil }
	workingDir = func() (string, error) { return t.TempDir(), nil }
	lookPath = func(string) (string, error) { return "/usr/bin/bun", nil }

	embedded := runtimebundle.Paths{
		Bun:          filepath.Join(root, "embedded", "bun"),
		AdaptersRoot: filepath.Join(root, "embedded", "adapters"),
		PluginsRoot:  filepath.Join(root, "embedded", "provider-plugins"),
	}
	calls := 0
	stubEmbedded(t, func(*Env) (runtimebundle.Paths, bool) {
		calls++
		return embedded, true
	})

	env := &Env{Getenv: func(string) string { return "" }}
	// 内嵌运行时优先于可执行文件同级布局。
	if got := resolveBun(env, ""); got != embedded.Bun {
		t.Fatalf("bun 应用内嵌运行时路径，实际 %q", got)
	}
	if got := resolveAdaptersRoot(env, ""); got != embedded.AdaptersRoot {
		t.Fatalf("adapters 应用内嵌运行时路径，实际 %q", got)
	}
	if got := resolveProviderPluginsRoot(env, ""); got != embedded.PluginsRoot {
		t.Fatalf("plugins 应用内嵌运行时路径，实际 %q", got)
	}
	if calls != 3 {
		t.Fatalf("三个路径各应查询一次内嵌运行时，实际 %d", calls)
	}

	// 显式覆盖与环境变量排在更前面，且不应触发解压。
	calls = 0
	if got := resolveBun(env, "override-bun"); got != "override-bun" {
		t.Fatalf("bun 显式覆盖 = %q", got)
	}
	if got := resolveAdaptersRoot(env, "override-adapters"); got != "override-adapters" {
		t.Fatalf("adapters 显式覆盖 = %q", got)
	}
	if got := resolveProviderPluginsRoot(env, "override-plugins"); got != "override-plugins" {
		t.Fatalf("plugins 显式覆盖 = %q", got)
	}
	if calls != 0 {
		t.Fatalf("显式覆盖不应查询内嵌运行时，实际 %d 次", calls)
	}

	envWithVars := &Env{Getenv: func(key string) string {
		switch key {
		case "QKA_BUN":
			return "env-bun"
		case "QKA_ADAPTERS_DIR":
			return "env-adapters"
		case "QKA_PROVIDER_PLUGINS_DIR":
			return "env-plugins"
		default:
			return ""
		}
	}}
	if got := resolveBun(envWithVars, ""); got != "env-bun" {
		t.Fatalf("bun 环境变量 = %q", got)
	}
	if got := resolveAdaptersRoot(envWithVars, ""); got != "env-adapters" {
		t.Fatalf("adapters 环境变量 = %q", got)
	}
	if got := resolveProviderPluginsRoot(envWithVars, ""); got != "env-plugins" {
		t.Fatalf("plugins 环境变量 = %q", got)
	}
	if calls != 0 {
		t.Fatalf("环境变量不应查询内嵌运行时，实际 %d 次", calls)
	}

	// 内嵌运行时不参与或解压失败时，继续走可执行文件同级布局。
	stubEmbedded(t, func(*Env) (runtimebundle.Paths, bool) {
		return runtimebundle.Paths{}, false // 模拟 Ensure 失败后的降级
	})
	if got := resolveBun(env, ""); got != siblingBunPath {
		t.Fatalf("内嵌不可用时应回退到同级 bun，实际 %q", got)
	}
	if got := resolveAdaptersRoot(env, ""); got != filepath.Join(bin, "adapters") {
		t.Fatalf("内嵌不可用时应回退到同级 adapters，实际 %q", got)
	}
	if got := resolveProviderPluginsRoot(env, ""); got != filepath.Join(bin, "provider-plugins") {
		t.Fatalf("内嵌不可用时应回退到同级 plugins，实际 %q", got)
	}

	// 内嵌运行时字段为空时也不应采用空路径。
	stubEmbedded(t, func(*Env) (runtimebundle.Paths, bool) {
		return runtimebundle.Paths{}, true
	})
	if got := resolveBun(env, ""); got != siblingBunPath {
		t.Fatalf("空内嵌路径应回退，实际 %q", got)
	}
}

func TestResolveEmbeddedRuntimeDefault(t *testing.T) {
	oldAvailable, oldEnsure := runtimeAvailable, runtimeEnsure
	oldVersion := Version
	t.Cleanup(func() {
		runtimeAvailable, runtimeEnsure = oldAvailable, oldEnsure
		Version = oldVersion
	})

	// 默认构建（无 embeddedruntime 标签）里 Available 为 false：直接返回 false。
	// 带标签且已生成资源的构建会真正解压，跳过这条断言。
	env := &Env{ConfigDir: t.TempDir(), Getenv: func(string) string { return "" }}
	if !runtimebundle.Available() {
		if _, ok := resolveEmbeddedRuntime(env); ok {
			t.Fatal("未内嵌运行时的构建不应返回内嵌路径")
		}
	}

	// 解压失败只降级，不 panic。
	runtimeAvailable = func() bool { return true }
	runtimeEnsure = func(string, string) (runtimebundle.Paths, error) {
		return runtimebundle.Paths{}, errors.New("disk full")
	}
	if _, ok := resolveEmbeddedRuntime(env); ok {
		t.Fatal("解压失败应返回 false")
	}

	// 成功时把已解析的配置目录与注入版本传给 Ensure。
	Version = "9.9.9"
	var gotConfigDir, gotVersion string
	want := runtimebundle.Paths{Bun: "bun", AdaptersRoot: "adapters", PluginsRoot: "plugins"}
	runtimeEnsure = func(configDir, version string) (runtimebundle.Paths, error) {
		gotConfigDir, gotVersion = configDir, version
		return want, nil
	}
	paths, ok := resolveEmbeddedRuntime(env)
	if !ok || paths != want {
		t.Fatalf("成功路径 = %+v/%v", paths, ok)
	}
	if gotConfigDir != env.ConfigDir || gotVersion != "9.9.9" {
		t.Fatalf("Ensure 入参 = %q/%q", gotConfigDir, gotVersion)
	}
}

func TestEnvConfigDirResolution(t *testing.T) {
	// 已解析的配置目录优先，避免 buildAppAt 与外层重复推导。
	env := &Env{ConfigDir: "/from/build", Getenv: func(string) string { return "/from/env" }}
	if got := env.configDir(); got != "/from/build" {
		t.Fatalf("configDir = %q", got)
	}
	// 为空时按 QKA_CONFIG_DIR/HOME 推导。
	env = &Env{Getenv: func(key string) string {
		switch key {
		case "QKA_CONFIG_DIR":
			return "/custom"
		default:
			return ""
		}
	}}
	if got := env.configDir(); got != "/custom" {
		t.Fatalf("QKA_CONFIG_DIR 覆盖 = %q", got)
	}
	env = &Env{Getenv: func(key string) string {
		if key == "HOME" {
			return "/home/u"
		}
		return ""
	}}
	if got := env.configDir(); got != filepath.Join("/home/u", ".qka") {
		t.Fatalf("HOME 推导 = %q", got)
	}
}

func TestBuildAppAtRecordsConfigDirForEmbeddedRuntime(t *testing.T) {
	env, configDir, _ := coverageEnv(t)
	var seen []string
	stubEmbedded(t, func(inner *Env) (runtimebundle.Paths, bool) {
		seen = append(seen, inner.ConfigDir)
		return runtimebundle.Paths{}, false
	})
	built, err := (&commonFlags{}).buildAppAt(context.Background(), env, configDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = built.Close() }()
	if len(seen) == 0 {
		t.Fatal("buildAppAt 应查询内嵌运行时")
	}
	for _, got := range seen {
		if got != configDir {
			t.Fatalf("内嵌运行时使用的配置目录 = %q，want %q", got, configDir)
		}
	}
	if env.ConfigDir != configDir {
		t.Fatalf("buildAppAt 应把配置目录记录到 Env，实际 %q", env.ConfigDir)
	}
}
