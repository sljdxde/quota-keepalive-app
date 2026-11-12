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
		// 全量 = 全平台项 + windows 专属项；darwin 专属（如菜单栏速度样式）
		// 会被过滤掉，所以按 schema 里的 Platform 标注动态计数，加新平台项
		// 时这里不用跟着改数字。
		want := 0
		for _, field := range settings.Schema {
			if field.Platform == "" || field.Platform == "windows" {
				want++
			}
		}
		if len(fields) != want {
			t.Fatalf("Windows 应返回全部平台适用设置项：期望=%d 实际=%d", want, len(fields))
		}
		return
	}
	if found {
		t.Fatalf("%s 上不应出现 Windows 专属的 show_ball", runtime.GOOS)
	}
	// 非 Windows = 全平台项 + 当前平台项：show_ball（windows 专属）被过滤，
	// 其余平台专属项按 Platform 标注动态计数。
	want := 0
	for _, field := range settings.Schema {
		if field.Platform == "" || field.Platform == runtime.GOOS {
			want++
		}
	}
	if len(fields) != want {
		t.Fatalf("%s 应返回 %d 项：实际=%d", runtime.GOOS, want, len(fields))
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
