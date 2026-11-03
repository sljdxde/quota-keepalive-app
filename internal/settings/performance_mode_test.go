package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func performanceModeField(t *testing.T) Field {
	t.Helper()
	for _, field := range Schema {
		if field.Key == "performance_mode" {
			return field
		}
	}
	t.Fatal("设置 schema 缺少 performance_mode")
	return Field{}
}

func TestPerformanceModeDefaultsToOff(t *testing.T) {
	field := performanceModeField(t)
	if field.Type != FieldSelect {
		t.Fatalf("performance_mode 应为 select，实际 %v", field.Type)
	}
	if got := field.DefaultString(); got != "off" {
		t.Fatalf("默认值应为 off（默认保留全部特效），实际 %q", got)
	}
	if got := Default().GetString("performance_mode"); got != "off" {
		t.Fatalf("默认设置 performance_mode 应为 off，实际 %q", got)
	}
	options := map[string]bool{}
	for _, option := range field.Options {
		options[option.Value] = true
	}
	for _, value := range []string{"auto", "on", "off"} {
		if !options[value] {
			t.Fatalf("缺少选项 %q", value)
		}
	}
	if len(options) != 3 {
		t.Fatalf("性能模式只应有 auto/on/off 三个选项，实际 %d 个", len(options))
	}
}

func TestPerformanceModeRejectsUnknownOption(t *testing.T) {
	s := Default()
	if err := s.SetString("performance_mode", "auto"); err != nil {
		t.Fatalf("auto 应被接受: %v", err)
	}
	if err := s.SetString("performance_mode", "on"); err != nil {
		t.Fatalf("on 应被接受: %v", err)
	}
	if err := s.SetString("performance_mode", "off"); err != nil {
		t.Fatalf("off 应被接受: %v", err)
	}
	if err := s.SetString("performance_mode", "low"); err != ErrInvalidOption {
		t.Fatalf("未知选项应被拒绝，实际 %v", err)
	}
	if err := s.Set("performance_mode", true); err != ErrWrongType {
		t.Fatalf("performance_mode 不是布尔项，实际 %v", err)
	}
}

func TestPerformanceModePersistsAcrossSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	if err := s.SetString("performance_mode", "on"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if got := loaded.GetString("performance_mode"); got != "on" {
		t.Fatalf("往返后应为 on，实际 %q", got)
	}
}

func TestPerformanceModeMissingFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	// 旧版本配置文件没有 performance_mode 字段，升级后应自动补默认值。
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("旧配置文件不应导致加载失败: %v", err)
	}
	if got := loaded.GetString("performance_mode"); got != "off" {
		t.Fatalf("旧配置缺失 performance_mode 时应回退默认值 off（保留特效），实际 %q", got)
	}
	if got := loaded.GetString("theme"); got != "dark" {
		t.Fatalf("旧配置的 theme 不应被覆盖，实际 %q", got)
	}
}
