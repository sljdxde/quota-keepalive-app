package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingScriptAdapter 在标准脚本适配器的入口前插一行「启动即写标记」，
// 用标记文件的字节数精确统计进程被 spawn 了几次。相比 ps/pgrep，它不依赖
// 采样时刻，能稳定区分「Build 期启动」和「首次使用时启动」。
func countingScriptAdapter(t *testing.T, dir, id, result, marker string) string {
	t.Helper()
	scriptPath := writeScriptAdapter(t, dir, id, result)
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(data), "import sys\n",
		"import sys\nopen(r'''"+marker+"''', 'a').write('x')\n", 1)
	if patched == string(data) {
		t.Fatal("脚本模板缺少 import sys，无法注入启动标记")
	}
	if err := os.WriteFile(scriptPath, []byte(patched), 0o700); err != nil {
		t.Fatal(err)
	}
	return scriptPath
}

func spawnCount(t *testing.T, marker string) int {
	t.Helper()
	info, err := os.Stat(marker)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return int(info.Size())
}

// TestAdapterProcessSpawnedOnlyOnFirstUse 是「适配器按需启动」的资源回归用例：
// Build 阶段不得 spawn 任何子进程；第一次 hostFor 只 spawn 一次；重复 hostFor
// 复用同一宿主，不重复 spawn。这是 4 个常驻 Bun 进程（约 33-55MB RSS）被
// 消除的直接证据。
func TestAdapterProcessSpawnedOnlyOnFirstUse(t *testing.T) {
	root := t.TempDir()
	adapters := filepath.Join(root, "adapters")
	marker := filepath.Join(root, "spawns.log")
	scriptPath := countingScriptAdapter(t, filepath.Join(adapters, "lazy"), "lazy", `{"ok":true}`, marker)

	a, err := Build(context.Background(), Config{
		ConfigDir:    filepath.Join(root, "config"),
		AdaptersRoot: adapters,
		BunPath:      scriptPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if got := spawnCount(t, marker); got != 0 {
		t.Fatalf("Build 阶段不应启动适配器进程，实际 spawn %d 次", got)
	}
	if loaded := a.LoadedAdapters(); len(loaded) != 1 || loaded[0].Host != nil {
		t.Fatalf("Build 后应只发现、不启动: %+v", loaded)
	}

	first, err := a.hostFor(context.Background(), "lazy")
	if err != nil {
		t.Fatalf("首次使用应启动适配器: %v", err)
	}
	if got := spawnCount(t, marker); got != 1 {
		t.Fatalf("首次使用应 spawn 1 次，实际 %d 次", got)
	}

	second, err := a.hostFor(context.Background(), "lazy")
	if err != nil || second != first {
		t.Fatalf("重复使用应复用同一宿主: err=%v first=%p second=%p", err, first, second)
	}
	if got := spawnCount(t, marker); got != 1 {
		t.Fatalf("重复使用不应重复 spawn，实际累计 %d 次", got)
	}
}
