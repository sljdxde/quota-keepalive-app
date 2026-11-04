package gui

import (
	"strings"
	"testing"

	"qka/internal/settings"
)

// 界面缩放接线的静态检查：Windows 150% 缩放的笔记本上内容区只有 595 CSS px
// 高，界面按 1240×720 设计，必须按视口收缩才不会「字太大、展示不全」。
// 纯逻辑在 assets/ui-scale.js（node 单测见 frontend-js-tests），这里确认主窗口
// 真的接上了：设置项、缓存比例（防闪烁）、以及随窗口尺寸重算。
func TestMainPageWiresUiScale(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)

	for _, want := range []string{
		`import {`,
		`initUiScale(`,
		`UI_SCALE_SETTING`,
		`from "./assets/ui-scale.js"`,
		`qka.uiScale`,                         // 首帧同步套用缓存比例
		`document.documentElement.style.zoom`, // 真正生效的机制
		`if (field.key === UI_SCALE_SETTING && uiScale) uiScale.setSetting(option.value)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("main.html 缺少界面缩放接线: %q", want)
		}
	}
}

// 设置项必须存在，否则设置页不会渲染出「界面缩放」，用户也无法调整。
func TestUiScaleSettingIsDeclared(t *testing.T) {
	var field *settings.Field
	for i := range settings.Schema {
		if settings.Schema[i].Key == "ui_scale" {
			field = &settings.Schema[i]
		}
	}
	if field == nil {
		t.Fatal("设置 schema 缺少 ui_scale")
	}
	if field.Type != "select" {
		t.Fatalf("ui_scale 应为 select，实际 %v", field.Type)
	}
	if field.Default != "auto" {
		t.Fatalf("ui_scale 默认值应为 auto，实际 %v", field.Default)
	}
	values := map[string]bool{}
	for _, option := range field.Options {
		values[option.Value] = true
	}
	for _, want := range []string{"auto", "1", "0.9", "0.85", "0.8"} {
		if !values[want] {
			t.Errorf("ui_scale 缺少选项 %q", want)
		}
	}
}
