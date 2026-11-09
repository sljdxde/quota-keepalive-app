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
	if len(darwin) != len(Schema)-1 || len(linux) != len(Schema)-1 {
		t.Fatalf("非 Windows 平台应只少 show_ball 一项：schema=%d darwin=%d linux=%d",
			len(Schema), len(darwin), len(linux))
	}
	// 未声明平台（空串）的项在所有平台可见，顺序保持不变。
	if len(windows) != len(Schema) {
		t.Fatalf("windows 平台应包含全部设置项：schema=%d windows=%d", len(Schema), len(windows))
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
