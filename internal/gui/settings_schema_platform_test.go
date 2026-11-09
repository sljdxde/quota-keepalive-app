package gui

import (
	"encoding/json"
	"runtime"
	"testing"

	"qka/internal/settings"
)

// SettingsSchema 绑定按当前平台过滤：「显示悬浮球」只在 Windows 表单里出现，
// 其它平台只读到一份没有它的 schema（设置值本身仍可读写）。
func TestSettingsSchemaBindingFiltersWindowsOnlyFields(t *testing.T) {
	service := &BoundService{}
	data, err := service.SettingsSchema()
	if err != nil {
		t.Fatalf("SettingsSchema: %v", err)
	}
	var fields []settings.Field
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("解析 schema: %v", err)
	}
	found := false
	for _, field := range fields {
		if field.Key == "show_ball" {
			found = true
		}
	}
	if runtime.GOOS == "windows" {
		if !found {
			t.Fatal("Windows 上设置表单应包含「显示悬浮球」")
		}
		if len(fields) != len(settings.Schema) {
			t.Fatalf("Windows 应返回全部设置项：schema=%d 实际=%d",
				len(settings.Schema), len(fields))
		}
		return
	}
	if found {
		t.Fatalf("%s 上不应出现 Windows 专属的 show_ball", runtime.GOOS)
	}
	if len(fields) != len(settings.Schema)-1 {
		t.Fatalf("非 Windows 应少一项：schema=%d 实际=%d", len(settings.Schema), len(fields))
	}
	// 平台无关的设置项仍必须存在（过滤不能误伤全平台项）。
	hasTheme := false
	for _, field := range fields {
		if field.Key == "theme" {
			hasTheme = true
		}
	}
	if !hasTheme {
		t.Fatal("过滤平台字段时不应丢掉全平台设置项 theme")
	}
}
