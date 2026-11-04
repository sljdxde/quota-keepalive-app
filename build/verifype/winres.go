package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// identityFromWinres 读 go-winres 配置里 RT_MANIFEST 的 identity.name，作为
// manifest 校验的期望值——与生成 syso 用的是同一份配置，天然单一来源。
// JSON 结构：RT_MANIFEST → 名称(如 "#1") → 语言(如 "0409") → identity.name。
// 顶层先按 json.RawMessage 解：各资源类型的值结构互不相同（RT_GROUP_ICON 是
// 数组），整棵树用同一个类型会反序列化失败。
func identityFromWinres(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 %s: %w", path, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return "", fmt.Errorf("解析 %s: %w", path, err)
	}
	branch, ok := top["RT_MANIFEST"]
	if !ok {
		return "", fmt.Errorf("%s 里没有 RT_MANIFEST 配置", path)
	}
	var manifests map[string]map[string]struct {
		Identity struct {
			Name string `json:"name"`
		} `json:"identity"`
	}
	if err := json.Unmarshal(branch, &manifests); err != nil {
		return "", fmt.Errorf("解析 %s 的 RT_MANIFEST: %w", path, err)
	}
	if len(manifests) == 0 {
		return "", fmt.Errorf("%s 里没有 RT_MANIFEST 配置", path)
	}
	// map 遍历无序，按键排序保证行为确定。
	names := make([]string, 0, len(manifests))
	for name := range manifests {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		langs := manifests[name]
		langKeys := make([]string, 0, len(langs))
		for lang := range langs {
			langKeys = append(langKeys, lang)
		}
		sort.Strings(langKeys)
		for _, lang := range langKeys {
			if n := langs[lang].Identity.Name; n != "" {
				return n, nil
			}
		}
	}
	return "", fmt.Errorf("%s 的 RT_MANIFEST 缺少 identity.name", path)
}
