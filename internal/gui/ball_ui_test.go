package gui

import (
	"os"
	"strings"
	"testing"
)

// 悬浮球替换掉旧的深色渐变胶囊：圆形卡、称号色进度环、滚动数字、可拖动、
// 球内迷你宠物（短按放技能 / 长按抛精灵球）、右键出自绘菜单。
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
		`id="ball-pet"`,
		`id="ball-pet-img"`,
		`id="ball-ball"`,
		`assets/ball-face.js`,
		`ballFaceState`,
		`face.petVisible`,
		`face.ballVisible`,
		`face.autoRelease`,
		`face.speedFontPx`,
		`updatePetSprite`,
		`assets/pokemon/`,
		`qka.pet.selected`,
		`qka.pet.caught.v1`,
		`assets/qka-ui.css`,
		`assets/ball.css`,
		`assets/odometer.js`,
		`assets/title-system.js`,
		`assets/bar-gesture.js`,
		`ringProgress`,
		`titleThresholds`,
		`titleThemeHues`,
		`Bound.BarMenu`,
		`Bound.BarStartDrag`,
		`Bound.BarToggle`,
		`shouldStartDrag`,
		`dragTargetOrigin`,
		`Events.On("tokenspeed-changed"`,
		`Events.On("pet-selected"`,
		`Events.On("pet-caught"`,
		`petId: pet.id`,
		`--wails-draggable: drag`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("悬浮球页面缺少 %q", want)
		}
	}
	// 贴边弹回是 BarToggle 的唯一正当调用点；BarStartDrag 只在拖动升级里用。
	for _, forbidden := range []string{
		`linear-gradient(150deg`,
		`--notch-bg`,
		`box-shadow: inset 0 .5px 0`,
		`assets/tray/`,
		// 球面精简：不再展示 tok/s 单位，也不承载投球/技能演出。
		`>tok/s<`,
		`viewer.throwBall`,
		// 球面不依赖 WebGL：小球里 3D 加载慢/失败会让宠物整块空白。
		`createPokemon3D`,
		`pokemon-3d-canvas`,
		`modelYaw`,
		`assets/pokemon-3d.js`,
		`petImg.hidden = true`,
		// SVG 没有 hidden IDL 属性：给精灵球设 .hidden 不会移除 hidden 属性，
		// 捕获态会变成「宠物隐藏 + 精灵球也不显示」的空白球。
		`ballIcon.hidden`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("悬浮球页面仍残留旧样式或旧入口 %q", forbidden)
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
		`petId: pet.id`,
		`writeCaught(pet.id, caught)`,
		// 短按放技能 / 长按投球：手势与结果横幅。
		`resolvePetGesture`,
		`sweepProgress`,
		`captureOutcome`,
		`id="pm-charge"`,
		`id="pm-popup"`,
		`捕获成功！`,
		`捕获失败`,
		`躲开了！`,
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
	// 真机反馈 64px 球太大：浮动态收到 52px、贴边收到 36px。
	// 这里锁定具体数值，避免以后被无意识改回去。
	if ballSize != 52 || ballDockSize != 36 {
		t.Errorf("球尺寸应为 52px / 贴边 36px，实际 %d / %d", ballSize, ballDockSize)
	}
}

// 球面比例：球缩到 52px 后，宠物反而稍大（26px）、数字更小（13px），
// 跑动幅度用 CSS 变量按速度放大。宠物用 2D 立绘，不依赖 WebGL 就绪——
// 这是「宠物根本显示不出来」的回归保护。
func TestBallFaceKeepsPetSmallAndSpeedVisible(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/assets/ball.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	for _, want := range []string{
		`.ball .core .pet-icon`,
		`width: 26px; height: 26px`,
		`.ball.has-pet .core b { font-size: 13px; }`,
		`.ball .core .pet-icon.running`,
		`@keyframes pet-run`,
		`@keyframes ball-shake`,
		`--run-duration`,
		`--run-bob`,
		`--run-tilt`,
		`--run-stride`,
		`--shake-duration`,
		// hidden 必须真正不占位：作者样式的 display 会压过 UA 规则。
		`.ball [hidden] { display: none !important; }`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("球面宠物/速度样式缺少 %q", want)
		}
	}
	for _, forbidden := range []string{
		`.pokemon-3d-canvas`,
		`.is-3d`,
		`.model-fallback`,
		`.ball.has-pet.n4`,
		`.ball.has-pet.n5`,
	} {
		if strings.Contains(css, forbidden) {
			t.Errorf("球面样式仍残留 3D/旧字号分档 %q", forbidden)
		}
	}

	// 两种模式的判定必须落在可单测的纯函数模块里，而不是散在页面中。
	faceData, err := frontendFS.ReadFile("frontend/assets/ball-face.js")
	if err != nil {
		t.Fatal(err)
	}
	face := string(faceData)
	for _, want := range []string{
		`export function ballFaceState`,
		`petVisible: !caught`,
		`ballVisible: caught`,
		`running: !reducedMotion && runSeconds > 0`,
		`shaking: !reducedMotion && shakeSeconds > 0`,
		`autoRelease: caught && shouldAutoRelease(speed)`,
		`speedFontPx: ballSpeedFontPx(source.speedText)`,
		`amplitude: runAmplitude(speed)`,
		`runDuration`,
		`shakeDuration`,
		`shouldAutoRelease`,
	} {
		if !strings.Contains(face, want) {
			t.Errorf("球面模式判定缺少 %q", want)
		}
	}
}

// 贴边态：36px 窗口有一半在屏幕外，16px 宠物/精灵球必须向屏幕内移半宽，
// 否则正好被窗口边界切一半。贴边侧由窗口矩形与工作区推断（纯函数可单测）。
func TestDockedBallContentMovesInsideScreen(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bar.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`assets/ball-dock.js`,
		`dockSideForWindow`,
		`dockContentShift`,
		`window.innerWidth < 44`,
		`width: window.innerWidth,`,
		`height: window.innerHeight,`,
		`ball.style.setProperty("--dock-x"`,
		`ball.style.setProperty("--dock-y"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("贴边态页面缺少 %q", want)
		}
	}

	cssData, err := frontendFS.ReadFile("frontend/assets/ball.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.ball.docked .core {`,
		`transform: translate(var(--dock-x, 0px), var(--dock-y, 0px));`,
	} {
		if !strings.Contains(string(cssData), want) {
			t.Errorf("贴边态样式缺少 %q", want)
		}
	}

	dockData, err := frontendFS.ReadFile("frontend/assets/ball-dock.js")
	if err != nil {
		t.Fatal(err)
	}
	dock := string(dockData)
	for _, want := range []string{
		`export const DOCK_CONTENT_SHIFT = 8`,
		`export function dockSideForWindow`,
		`export function dockContentShift`,
	} {
		if !strings.Contains(dock, want) {
			t.Errorf("贴边判定模块缺少 %q", want)
		}
	}
}

// 捕获态必须真的画出精灵球：SVG 元素没有 hidden IDL 属性，历史上用
// ballIcon.hidden 控制显隐，结果「宠物隐藏 + 精灵球也不显示」，球面只剩数字。
// 现在两种主体由 .show-pet / .show-ball 类互斥控制，CSS 里必须有 display 规则。
func TestBallCaughtModeShowsPokeBall(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bar.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`classList.toggle("caught", caught)`,
		`classList.toggle("show-pet", face.petVisible)`,
		`classList.toggle("show-ball", face.ballVisible)`,
		`if (face.petVisible) updatePetSprite(pet)`,
		`id="ball-ball" viewBox="0 0 24 24" aria-hidden="true">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("捕获态渲染缺少 %q", want)
		}
	}

	cssData, err := frontendFS.ReadFile("frontend/assets/ball.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	for _, want := range []string{
		`display: none;`,
		`.ball.show-pet .core .pet-icon { display: block; }`,
		`.ball.show-ball .core .ball-icon { display: block; }`,
		`@keyframes ball-shake`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("捕获态样式缺少 %q", want)
		}
	}
}
