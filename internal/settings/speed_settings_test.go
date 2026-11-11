package settings

import "testing"

// 速度相关设置项必须存在、默认值合理，且越界值能安全回退。
func TestTokenSpeedSettingsDefaults(t *testing.T) {
	def := Default()
	if got := def.SpeedPollInterval(); got.String() != "500ms" {
		t.Fatalf("默认采样间隔 = %v，want 500ms", got)
	}
	if got := def.GetString(KeyTokenSpeedDisplay); got != "roll" {
		t.Fatalf("默认数字显示 = %q，want roll", got)
	}
	if got := def.MenuBarSpeedStyle(); got != MenuBarSpeedLabel {
		t.Fatalf("默认菜单栏样式 = %q，want %q", got, MenuBarSpeedLabel)
	}
}

func TestSpeedPollIntervalClampsAndFallsBack(t *testing.T) {
	s := Default()
	// schema 允许的档位都能取到。
	for _, value := range []string{"200", "300", "500", "750"} {
		if err := s.SetString(KeyTokenSpeedPoll, value); err != nil {
			t.Fatalf("SetString(%q) = %v", value, err)
		}
		if got := s.SpeedPollInterval().String(); got != value+"ms" {
			t.Fatalf("SpeedPollInterval() = %v，want %sms", got, value)
		}
	}
	// 手工写坏的配置值必须回退到 500ms，而不是让轮询卡住。
	s.values[KeyTokenSpeedPoll] = "0"
	if got := s.SpeedPollInterval().String(); got != "500ms" {
		t.Fatalf("越界值应回退 500ms，实际 %v", got)
	}
	s.values[KeyTokenSpeedPoll] = "abc"
	if got := s.SpeedPollInterval().String(); got != "500ms" {
		t.Fatalf("非数字应回退 500ms，实际 %v", got)
	}
	s.values[KeyTokenSpeedPoll] = "9000"
	if got := s.SpeedPollInterval().String(); got != "500ms" {
		t.Fatalf("过大值应回退 500ms，实际 %v", got)
	}
}

func TestMenuBarSpeedStyleFallsBack(t *testing.T) {
	s := Default()
	s.values[KeyTokenSpeedMenuBar] = "future"
	if got := s.MenuBarSpeedStyle(); got != MenuBarSpeedLabel {
		t.Fatalf("未知菜单栏样式应回退 label，实际 %q", got)
	}
}

func TestIsTokenSpeedKey(t *testing.T) {
	for _, key := range []string{KeyTokenSpeedPoll, KeyTokenSpeedDisplay, KeyTokenSpeedMenuBar} {
		if !IsTokenSpeedKey(key) {
			t.Errorf("%q 应被识别为速度设置", key)
		}
	}
	for _, key := range []string{"theme", "title_theme", ""} {
		if IsTokenSpeedKey(key) {
			t.Errorf("%q 不该被识别为速度设置", key)
		}
	}
}
