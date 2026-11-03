package gui

import (
	"strings"
	"testing"

	"qka/internal/settings"
)

func readFrontendAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := frontendFS.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(data)
}

// 「整个界面都卡」在软件渲染下主要来自每帧都要重新合成的毛玻璃，以及
// 750ms 一次的速度事件重建隐藏页 DOM。这里确认三个页面都接上了性能模式，
// 且降级规则、检测逻辑、3D 限帧都真实存在。
func TestPerformanceModeWiredIntoAllWindows(t *testing.T) {
	mainPage := readFrontendAsset(t, "frontend/main.html")
	panelPage := readFrontendAsset(t, "frontend/panel.html")
	settingsPage := readFrontendAsset(t, "frontend/settings.html")
	panelScript := readFrontendAsset(t, "frontend/assets/panel.js")
	settingsScript := settingsPage

	requireStrings(t, mainPage, "主窗口性能模式",
		`href="assets/perf-mode.css"`,
		`from "./assets/perf-mode.js"`,
		`initPerformanceMode`,
		`applyPerformanceSetting`,
		`PERFORMANCE_MODE_KEY`,
		`Events.On("performance-changed"`,
		`initPerformanceMode({ setting: next })`,
	)
	requireStrings(t, panelPage, "菜单栏面板性能模式",
		`href="assets/perf-mode.css"`,
	)
	requireStrings(t, panelScript, "菜单栏面板性能模式脚本",
		`from "./perf-mode.js"`,
		`initPerformanceMode({ setting: values[PERFORMANCE_MODE_KEY] })`,
		`Events.On("performance-changed"`,
	)
	requireStrings(t, settingsScript, "独立设置页性能模式",
		`href="assets/perf-mode.css"`,
		`import { initPerformanceMode, PERFORMANCE_MODE_KEY } from "./assets/perf-mode.js";`,
		`initPerformanceMode({ setting: values && values[PERFORMANCE_MODE_KEY] })`,
		`Events.On("performance-changed"`,
	)
}

func TestPerformanceModeDetectionAndDegradeRules(t *testing.T) {
	js := readFrontendAsset(t, "frontend/assets/perf-mode.js")
	requireStrings(t, js, "渲染器检测",
		`WEBGL_debug_renderer_info`,
		`UNMASKED_RENDERER_WEBGL`,
		`failIfMajorPerformanceCaveat`,
		`export function detectRenderer(`,
		`export function isLowPerformanceRenderer(`,
		`export function resolvePerformanceMode(`,
		`export function performanceLow(`,
		`swiftshader`,
		`llvmpipe`,
		`microsoft basic render`,
		`vmware svga`,
		`perf-low`,
		`data-perf`,
		`qka-perf-changed`,
	)
	css := readFrontendAsset(t, "frontend/assets/perf-mode.css")
	requireStrings(t, css, "低配样式规则",
		`body.perf-low .topbar`,
		`body.perf-low .window .card`,
		`backdrop-filter: none !important`,
		`-webkit-backdrop-filter: none !important`,
		`body.perf-low dialog::backdrop`,
		`body.perf-low .liquid-surface`,
		`body.perf-low .main-pet-viewer`,
		`transition-duration: 1ms !important`,
	)
}

// 750ms 的速度事件原本会无条件重建速度页两张隐藏表格；低配下只保留最新
// 快照，切回速度页时再补一次实时值。
func TestTokenSpeedEventSkipsHiddenPages(t *testing.T) {
	page := readFrontendAsset(t, "frontend/main.html")
	requireStrings(t, page, "速度快照按可见页刷新",
		`function speedTabVisible()`,
		`function applySpeedToPet(view)`,
		`pendingSpeedSnapshot`,
		`if (speedTabVisible()) {`,
		`applySpeedToPet(asJSON(event.data));`,
	)
	if strings.Contains(page, `if (event && event.data) renderTokenSpeed(event.data);`) {
		t.Fatal("速度快照不能在速度页不可见时也重建隐藏表格")
	}
}

func TestPokemon3DRespectsPerformanceMode(t *testing.T) {
	script := readFrontendAsset(t, "frontend/assets/pokemon-3d.js")
	requireStrings(t, script, "3D 宠物限帧",
		`from "./perf-mode.js"`,
		`performanceLow`,
		`PERFORMANCE_LOW_FPS`,
		`pixelRatioCap`,
		`qka-perf-changed`,
		`antialias: !performanceLow()`,
	)
}

func TestPerformanceModeSchemaDefaultsToOff(t *testing.T) {
	var field *settings.Field
	for i := range settings.Schema {
		if settings.Schema[i].Key == "performance_mode" {
			field = &settings.Schema[i]
			break
		}
	}
	if field == nil {
		t.Fatal("设置 schema 缺少 performance_mode")
	}
	if field.Type != settings.FieldSelect {
		t.Fatalf("performance_mode 应为 select，实际 %v", field.Type)
	}
	if field.Type != settings.FieldSelect || field.DefaultString() != "off" {
		t.Fatalf("performance_mode 默认值应为 off（默认保留全部特效），实际 %v", field.Default)
	}
	var values []string
	for _, option := range field.Options {
		values = append(values, option.Value)
	}
	if strings.Join(values, ",") != "auto,on,off" {
		t.Fatalf("performance_mode 选项应为 auto,on,off，实际 %v", values)
	}
	if !strings.Contains(field.Description, "软件渲染") && !strings.Contains(field.Description, "低配") {
		t.Fatalf("performance_mode 描述应说明低配/软件渲染含义，实际 %q", field.Description)
	}
}
