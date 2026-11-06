package gui

import (
	"os"
	"strings"
	"testing"
)

// 悬浮球替换掉旧的深色渐变胶囊：圆形卡、称号色进度环、滚动数字、可拖动、
// 左键开迷你宠物 / 右键出自绘菜单。中央固定为 Q 水印，不随宠物切换。
func TestFloatingBallUsesPrototypeStructure(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bar.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`class="ball idle"`,
		`class="ring"`,
		`r="30.5"`,
		`stroke-dasharray="191.6"`,
		`id="speed-value"`,
		`assets/qka-ui.css`,
		`assets/ball.css`,
		`assets/odometer.js`,
		`assets/title-system.js`,
		`ringProgress`,
		`titleThresholds`,
		`titleThemeHues`,
		`Bound.BarToggle`,
		`Bound.BarMenu`,
		`Events.On("tokenspeed-changed"`,
		`--wails-draggable: drag`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("悬浮球页面缺少 %q", want)
		}
	}
	for _, forbidden := range []string{
		`linear-gradient(150deg`,
		`--notch-bg`,
		`box-shadow: inset 0 .5px 0`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("悬浮球页面仍残留旧胶囊样式 %q", forbidden)
		}
	}
}

// 精简状态面板：只保留状态速览（生成状态 / tok/s / 称号与下一级 / 今日合计），
// 底部「打开主界面」，不再塞签到/宠物/模型管理。
func TestBallPanelShowsCompactStatusOnly(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/ballpanel.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`class="ball-panel idle"`,
		`id="live-text"`,
		`id="panel-speed"`,
		`id="rank-level"`,
		`id="rank-name"`,
		`id="rank-next"`,
		`id="today"`,
		`今日合计`,
		`id="open-main"`,
		`Events.Emit("open-main")`,
		`tokenspeed-changed`,
		`titleThresholds`,
		`assets/odometer.js`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("精简面板缺少 %q", want)
		}
	}
	for _, forbidden := range []string{
		`保活`,
		`data-panel-tab`,
		`pet-stage`,
		`Bound.Run()`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("精简面板不应包含 %q", forbidden)
		}
	}
}

func TestBallPanelOptionsAreCompactAndFixed(t *testing.T) {
	opts := ballPanelOptions("dark")
	if opts.Width != ballPanelWidth || opts.Height != ballPanelHeight ||
		opts.MinWidth != ballPanelWidth || opts.MinHeight != ballPanelHeight ||
		opts.MaxWidth != ballPanelWidth || opts.MaxHeight != ballPanelHeight {
		t.Fatalf("精简面板尺寸应固定为 %d×%d，实际 %+v", ballPanelWidth, ballPanelHeight, opts)
	}
	if !opts.Frameless || !opts.AlwaysOnTop || !opts.DisableResize || !opts.HideOnEscape {
		t.Errorf("精简面板缺少弹出层行为: %+v", opts)
	}
	if opts.URL != "/ballpanel.html?theme=dark" {
		t.Errorf("精简面板应带主题 URL，实际 %q", opts.URL)
	}
}

// 迷你宠物：只渲染当前伙伴（3D，失败回退 2D），带等级与当日 Token；
// 不放名单/解锁/签到。左键打开，Esc 收起。
func TestPetMiniRendersSelectedCompanionOnly(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/petmini.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`id="pet-mini"`,
		`id="pm-stage"`,
		`id="pm-avatar"`,
		`id="pm-name"`,
		`id="pm-level"`,
		`id="pm-today"`,
		`id="pm-speed"`,
		`createPokemon3D`,
		`qka.pet.selected`,
		`qka.pet.caught.v1`,
		`POKEMON_CATALOG`,
		`assets/pet-gaze.js`,
		`initPerformanceMode`,
		`pokemon-3d-canvas`,
		`pokemon-stage-image[data-stage="1"]`,
		`collapse-petmini`,
		`pet-selected`,
		`pet-caught`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("迷你宠物缺少 %q", want)
		}
	}
	for _, forbidden := range []string{
		`pet-unlocks`,
		`data-pet-id`,
		`Bound.Run()`,
		`保活`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("迷你宠物不应包含 %q", forbidden)
		}
	}
}

// 右键菜单按 magpie 的 .row-menu：11px 圆角、4px 内边距、1px 行距、28px 行、
// 7px 行圆角、13px 图标、分隔线与危险色。三个动作不再用原生菜单。
func TestBallMenuUsesMagpieRowMenu(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/ballmenu.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`data-action="main"`,
		`data-action="settings"`,
		`data-action="hide-ball"`,
		`data-action="quit"`,
		`打开主界面`,
		`设置`,
		`隐藏悬浮球`,
		`退出 Quota Keepalive`,
		`SettingsSet("show_ball", false)`,
		`border-radius: 11px`,
		`height: 28px`,
		`border-radius: 7px`,
		`gap: 1px`,
		`class="sep"`,
		`class="danger"`,
		`collapse-ballmenu`,
		`"open-main"`,
		`"open-settings"`,
		`"quit"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("右键菜单缺少 %q", want)
		}
	}
}

// 「显示悬浮球」设置：schema 有 show_ball 布尔项，关闭时球与浮层都不出现。
func TestShowBallSettingWiredToEndToEnd(t *testing.T) {
	schemaData, err := os.ReadFile("../settings/schema.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schemaData), `Key:         "show_ball"`) {
		t.Error("settings schema 缺少 show_ball 布尔项")
	}
	appData, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	boundData, err := os.ReadFile("bound.go")
	if err != nil {
		t.Fatal(err)
	}
	wiring := string(appData) + string(boundData)
	for _, want := range []string{
		`current.Get("show_ball")`,
		`deps.BallVisible = result.setBallPreference`,
		`if key == "show_ball" && s.deps.BallVisible != nil`,
		`if !a.ballShow.Load() {`,
	} {
		if !strings.Contains(wiring, want) {
			t.Errorf("show_ball 接线缺少 %q", want)
		}
	}
}

func testReadFile(name string) []byte {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	return data
}

func TestPetMiniAndMenuOptionsAreFixed(t *testing.T) {
	mini := petMiniOptions("light")
	if mini.Width != petMiniWidth || mini.Height != petMiniHeight ||
		mini.MinWidth != petMiniWidth || mini.MaxWidth != petMiniWidth ||
		mini.MinHeight != petMiniHeight || mini.MaxHeight != petMiniHeight {
		t.Fatalf("迷你宠物尺寸应固定为 %d×%d，实际 %+v", petMiniWidth, petMiniHeight, mini)
	}
	if !mini.Frameless || !mini.AlwaysOnTop || !mini.DisableResize || !mini.HideOnEscape {
		t.Errorf("迷你宠物缺少浮层行为: %+v", mini)
	}
	if mini.URL != "/petmini.html?theme=light" {
		t.Errorf("迷你宠物应带主题 URL，实际 %q", mini.URL)
	}

	menu := ballMenuOptions("system")
	if menu.Width != ballMenuWidth || menu.Height != ballMenuHeight ||
		menu.MinWidth != ballMenuWidth || menu.MaxWidth != ballMenuWidth ||
		menu.MinHeight != ballMenuHeight || menu.MaxHeight != ballMenuHeight {
		t.Fatalf("右键菜单尺寸应固定为 %d×%d，实际 %+v", ballMenuWidth, ballMenuHeight, menu)
	}
	if !menu.Frameless || !menu.AlwaysOnTop || !menu.DisableResize || !menu.HideOnEscape {
		t.Errorf("右键菜单缺少浮层行为: %+v", menu)
	}
	if menu.URL != "/ballmenu.html" {
		t.Errorf("system 主题不应在 URL 中强制主题，实际 %q", menu.URL)
	}
}

func TestBallWindowOptionsKeepDragAndDockSizes(t *testing.T) {
	opts := barWindowOptions("system")
	if opts.Width != ballSize || opts.Height != ballSize {
		t.Fatalf("悬浮球窗口应为 %d×%d，实际 %d×%d", ballSize, ballSize, opts.Width, opts.Height)
	}
	if opts.MinWidth != ballSize || opts.MaxWidth != ballSize {
		t.Errorf("初始尺寸应被 Min/Max 锁死，实际 %+v", opts)
	}
	if opts.DisableResize {
		t.Error("DisableResize 必须为 false，否则 --wails-draggable 拖不动窗口")
	}
	if !opts.Frameless || !opts.AlwaysOnTop || !opts.Hidden {
		t.Errorf("悬浮球窗口缺少置顶/无边框/初始隐藏: %+v", opts)
	}
	if opts.URL != "/bar.html" {
		t.Errorf("system 主题不应在 URL 中强制主题，实际 %q", opts.URL)
	}
	if got := barWindowOptions("light").URL; got != "/bar.html?theme=light" {
		t.Errorf("light 主题应进 URL，实际 %q", got)
	}
	if ballDockSize >= ballSize || ballDockSize%2 != 0 {
		t.Errorf("贴边尺寸 %d 应小于浮动态且能被 2 整除（一半露出）", ballDockSize)
	}
}
