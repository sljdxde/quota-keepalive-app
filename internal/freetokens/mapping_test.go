package freetokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 映射表必须与 provider-plugins/ 实际内置的插件一致：每个映射的 npm 包都要
// 能在仓库里找到对应目录与 package.json，否则 UI 会宣称「已支持」却加载不出。
func TestMappingsCoverBundledPlugins(t *testing.T) {
	root := filepath.Join("..", "..", "provider-plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读取 provider-plugins: %v", err)
	}
	bundled := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "package.json"))
		if err != nil {
			continue
		}
		var manifest struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil || manifest.Name == "" {
			continue
		}
		bundled[manifest.Name] = entry.Name()
	}
	if len(bundled) < 13 {
		t.Fatalf("内置 provider 插件数量 = %d，want >= 13", len(bundled))
	}
	for _, mapping := range Mappings {
		dir, ok := bundled[mapping.NPM]
		if !ok {
			t.Errorf("映射 %q 的插件 %q 未内置", mapping.Provider, mapping.NPM)
			continue
		}
		// 入口文件必须存在，否则 provider runtime 扫不到。
		if _, err := os.Stat(filepath.Join(root, dir, "index.mjs")); err != nil {
			t.Errorf("插件 %s 缺少入口 index.mjs", mapping.NPM)
		}
	}
}

func TestLookupProviderTrimsAndRejectsUnknown(t *testing.T) {
	if mapping, ok := LookupProvider("  MiniMax "); !ok || mapping.Name != "MiniMax Code" {
		t.Fatalf("MiniMax 映射异常: %+v ok=%v", mapping, ok)
	}
	if _, ok := LookupProvider("不存在的小厂"); ok {
		t.Fatal("未知厂商不应命中")
	}
}

func TestMatchesProviderByIDAndName(t *testing.T) {
	mapping, _ := LookupProvider("阿里云 Qoder")
	if !mapping.MatchesProvider("qoder-cn", "Qoder CN") {
		t.Fatal("应命中 qoder-cn 的 target id")
	}
	if !mapping.MatchesProvider("", "Qoder") {
		t.Fatal("target id 变化时应能按名称兜底")
	}
	if mapping.MatchesProvider("zed", "Zed") {
		t.Fatal("不应命中无关 provider")
	}
}
