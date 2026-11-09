package providerruntime

import (
	"path/filepath"
	"testing"
)

// 随包插件目录必须能被 DiscoverPlugins 全部认出来：QKA 的「支持的供应商」
// 就是这一份清单，漏一个就等于应用管理页少一个供应商。
func TestBundledProviderPluginsDiscover(t *testing.T) {
	plugins, err := DiscoverPlugins(filepath.Join("..", "..", "provider-plugins"))
	if err != nil {
		t.Fatalf("DiscoverPlugins: %v", err)
	}
	got := make(map[string]bool, len(plugins))
	for _, plugin := range plugins {
		got[plugin.Spec] = true
	}
	wanted := []string{
		"@magpie-community/opencode-doubao-work-auth",
		"@magpie-community/opencode-cloudflare-workers-ai-auth",
		"@magpie-community/opencode-minimax-auth",
		"@magpie-community/opencode-qoder-auth",
		"@magpie-community/opencode-trae-auth",
		"@magpie-community/opencode-workbuddy-auth",
		"@magpie-community/opencode-inkstone-auth",
		"@magpie-community/opencode-zed-auth",
		"@magpie-community/opencode-codearts-auth",
		"@magpie-community/opencode-autoclaw-auth",
		"@magpie-community/opencode-zcode-auth",
		"@magpie-community/opencode-sensenova-auth",
		"@magpie-community/opencode-zen-free-auth",
	}
	for _, spec := range wanted {
		if !got[spec] {
			t.Errorf("随包插件未被发现: %s", spec)
		}
	}
	if len(plugins) < len(wanted) {
		t.Fatalf("发现插件数 = %d，want >= %d", len(plugins), len(wanted))
	}
}
