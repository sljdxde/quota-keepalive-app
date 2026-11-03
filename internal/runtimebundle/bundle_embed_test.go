//go:build embeddedruntime

package runtimebundle

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedBuildExtractsRuntime 只在带 embeddedruntime 标签且已生成资源的
// 构建里运行，验证「内嵌 → 首次解压 → 路径可用」整条链路。
func TestEmbeddedBuildExtractsRuntime(t *testing.T) {
	if !Available() {
		t.Fatal("带 embeddedruntime 标签的构建应内嵌运行时资源")
	}
	configDir := t.TempDir()
	paths, err := Ensure(configDir, "0.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Bun, paths.AdaptersRoot, paths.PluginsRoot} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("解压产物缺失 %s: %v", path, err)
		}
	}
	if filepath.Dir(paths.Bun) != filepath.Dir(paths.AdaptersRoot) {
		t.Fatalf("产物不在同一版本目录: %+v", paths)
	}
	if _, err := Ensure(configDir, "0.0.0-test"); err != nil {
		t.Fatalf("重复 Ensure 应命中就绪标记: %v", err)
	}
}
