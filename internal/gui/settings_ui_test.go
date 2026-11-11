package gui

import (
	"strings"
	"testing"
)

func TestSettingsPageRendersThreeWayAppearance(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/settings.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`f.type === "select"`,
		"select-control",
		"select-control-shell",
		"appearance: none",
		`group.setAttribute("role", "radiogroup")`,
		`Bound.SettingsSetString`,
		`if (f.key === "theme") applyTheme(option.value)`,
		`Events.On("theme-changed"`,
		`document.documentElement.dataset.theme = theme`,
		"background: var(--bg)",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("设置页缺少三档外观或主题响应 %q", want)
		}
	}
}

func TestSettingsPageUsesFullProductNameAndAlignedSections(t *testing.T) {
	mainData, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	settingsData, err := frontendFS.ReadFile("frontend/settings.html")
	if err != nil {
		t.Fatal(err)
	}
	mainPage := string(mainData)
	settingsPage := string(settingsData)

	requireStrings(t, mainPage, "主窗口设置页",
		`<span class="brand-name">Quota Keepalive</span>`,
		`id="settings-appearance-title">外观与启动</b>`,
		`class="settings-section-head"`,
		`row.appendChild(settingsCopy(field));`,
		`row.appendChild(wrap);`,
		`class="select-shell`,
		`appearance: none`,
		`.select-shell::after`,
		`@media (max-width: 700px)`,
		`.brand { display: none; }`,
		// 设置页的操作按钮不再套灰色分段槽：描边按钮明确可点。
		`class="settings-choice settings-actions"`,
		`.settings-choice.settings-actions button {`,
		`border: 1px solid var(--line);`,
		// 软件更新卡片里的「国内镜像源」开关（schema 隐藏字段，专用界面呈现）。
		`id="settings-update-mirror"`,
		`Bound.SettingsSet("update_domestic_mirror", next)`,
		`if (field.hidden) return;`,
		`已开启国内镜像源`,
		// 镜像开关是软件更新标题右侧的紧凑胶囊，不再单独占一行。
		`class="settings-inline-toggle"`,
		`<span>国内镜像</span>`,
		`.settings-inline-toggle .settings-switch {`,
		// GitHub 超时给出可操作提示。
		`function updateErrorMessage(`,
		`连接 GitHub 超时；可打开下方「国内镜像源」后重试。`,
	)
	requireStrings(t, settingsPage, "独立设置页",
		"Quota Keepalive 使用同一份声明式配置",
		`row.appendChild(copyBlock(f));`,
		`row.appendChild(switchWrap);`,
		`shell.className = "select-control-shell";`,
		`appearance: none`,
		`.select-control-shell::after`,
	)
	for pageName, page := range map[string]string{
		"main.html":     mainPage,
		"settings.html": settingsPage,
	} {
		if strings.Contains(page, ">QKA<") {
			t.Errorf("%s 仍把 QKA 缩写作为产品名称展示", pageName)
		}
	}
}
