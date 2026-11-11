package settings

import "testing"

// 平台专属设置项（如仅 Windows 的「显示悬浮球」）只应在声明平台上展示：
// 表单由 schema 生成，若不过滤，macOS 用户会看到一个点了没反应的开关。
func TestFieldsForPlatformFiltersPlatformSpecificFields(t *testing.T) {
	hasKey := func(fields []Field, key string) bool {
		for _, field := range fields {
			if field.Key == key {
				return true
			}
		}
		return false
	}

	windows := FieldsForPlatform("windows")
	darwin := FieldsForPlatform("darwin")
	linux := FieldsForPlatform("linux")

	if !hasKey(windows, "show_ball") {
		t.Fatal("windows 平台应展示 show_ball")
	}
	for _, other := range []string{"darwin", "linux"} {
		if hasKey(FieldsForPlatform(other), "show_ball") {
			t.Fatalf("%s 平台不应展示 show_ball", other)
		}
	}
	if !hasKey(darwin, KeyTokenSpeedMenuBar) {
		t.Fatal("darwin 平台应展示菜单栏速度样式")
	}
	for _, other := range []string{"windows", "linux"} {
		if hasKey(FieldsForPlatform(other), KeyTokenSpeedMenuBar) {
			t.Fatalf("%s 平台不应展示菜单栏速度样式", other)
		}
	}
	// windows 少 token_speed_menubar；darwin 少 show_ball；linux 两个都少。
	if len(darwin) != len(Schema)-1 {
		t.Fatalf("darwin 平台应只少 show_ball：schema=%d darwin=%d",
			len(Schema), len(darwin))
	}
	if len(linux) != len(Schema)-2 {
		t.Fatalf("linux 平台应少 show_ball 与 token_speed_menubar：schema=%d linux=%d",
			len(Schema), len(linux))
	}
	// 未声明平台（空串）的项在所有平台可见，顺序保持不变。
	if len(windows) != len(Schema)-1 {
		t.Fatalf("windows 平台应包含除菜单栏样式外的全部设置项：schema=%d windows=%d",
			len(Schema), len(windows))
	}
}

// 每个声明了 Platform 的字段都必须出现在它自己的平台上，且只在那个平台上。
func TestFieldsForPlatformHonoursDeclaredPlatforms(t *testing.T) {
	hasKey := func(fields []Field, key string) bool {
		for _, field := range fields {
			if field.Key == key {
				return true
			}
		}
		return false
	}
	platforms := []string{"windows", "darwin", "linux"}
	for _, field := range Schema {
		if field.Platform == "" {
			for _, platform := range platforms {
				if !hasKey(FieldsForPlatform(platform), field.Key) {
					t.Errorf("%q 未声明平台，应在 %s 可见", field.Key, platform)
				}
			}
			continue
		}
		if !hasKey(FieldsForPlatform(field.Platform), field.Key) {
			t.Errorf("%q 未出现在它声明的平台 %s 上", field.Key, field.Platform)
		}
		for _, platform := range platforms {
			if platform == field.Platform {
				continue
			}
			if hasKey(FieldsForPlatform(platform), field.Key) {
				t.Errorf("%q 不该出现在 %s 上", field.Key, platform)
			}
		}
	}
}
