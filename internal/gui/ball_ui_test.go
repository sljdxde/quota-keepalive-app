package gui

import (
	"os"
	"runtime"
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
		`assets/ball-effects.js`,
		`ballFaceState`,
		`ballEffectsState`,
		`id="ball-fx"`,
		`ball.dataset.fxTier`,
		`ball.dataset.effect`,
		`--hop-duration`,
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
	// 悬浮球窗口只在 Windows 创建：非 Windows 的 barWindowOptions 是空实现，
	// 这里断言的是 Windows 窗口选项，别在 macOS/Linux 上误报。
	if runtime.GOOS != "windows" {
		t.Skip("悬浮球窗口选项仅在 Windows 生效")
	}
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

// 球面比例：球缩到 52px 后，宠物反而稍大（26px）、数字换成等宽字体且再收一档
// （11px 基准）；4 位（"1.2k"）10px、5 位（"18.3k"）9px 都能在 44px 球面内放下。
// 跑动按档位切换跑姿 + 速度线。宠物用 2D 立绘，不依赖 WebGL 就绪——
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
		// 球面数字 = 等宽字体 + 11px 基准 + 1ch 列宽（与 ".fix" 固定字符等宽，
		// "1.2k" 这类 4 字符形态不会串位）。
		`font-family: ui-monospace,`,
		`font-size: 11px;`,
		`.ball .core .roll .cell { width: 1ch; }`,
		`.ball .core .pet-icon.running`,
		`@keyframes pet-run`,
		`@keyframes ball-shake`,
		`--run-duration`,
		`--run-bob`,
		`--run-tilt`,
		`--run-stride`,
		`--shake-duration`,
		// 分档跑姿：踱步/快跑/冲刺各有不同 keyframes 与速度线，
		// 这是「速度变化不大、看不出区别」的回归保护。
		`@keyframes pet-stroll`,
		`@keyframes pet-sprint`,
		`.ball[data-run-tier="1"]`,
		`.ball[data-run-tier="3"]`,
		`.ball[data-run-tier="4"]`,
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
		`runTier: runTier(speed)`,
		`runDuration`,
		`shakeDuration`,
		`shouldAutoRelease`,
	} {
		if !strings.Contains(face, want) {
			t.Errorf("球面模式判定缺少 %q", want)
		}
	}
}

// 蛙跳 + 速度特效（设计原型 v6 落地）：球面立绘水平翻转成 2D 侧视，
// 用青蛙式「上下 + 前后」跳跃表现速度，100/300 两档四宠特效收在圆内。
// 静止、精灵球、减少动效、贴边态都不播放连续动画（置顶小窗闪屏教训）。
func TestBallFrogHopAndSpeedEffects(t *testing.T) {
	cssData, err := frontendFS.ReadFile("frontend/assets/ball.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	for _, want := range []string{
		// 2D 侧视：只翻转立绘，不裁切、不拼接额外腿部
		`transform: scaleX(-1);`,
		// 青蛙式跳跃：一个周期含蹲伏/蹬地/顶点悬停/落地压扁/收重心
		`@keyframes frog-hop`,
		`animation: frog-hop var(--hop-duration, .5s) linear infinite;`,
		`46%, 52%`,
		`76% { transform: translate(4.5px, 1px) rotate(7deg) scaleY(.84)`,
		// 特效层圆形裁剪 = 复刻 SetWindowRgn 的正圆窗口
		`border-radius: 50%; overflow: hidden;`,
		`.ball[data-fx-tier="0"] .fx`,
		// 落地那一刻才扬尘（周期 76%）
		`@keyframes fx-puff`,
		`0%, 70% { transform: translate(0, 0) scale(.5); opacity: 0; }`,
		`76% { transform: translate(0, 0) scale(.7); opacity: .78; }`,
		// 四宠两档特效
		`.ball[data-effect="smoke"]`,
		`.ball[data-effect="flame"]`,
		`.ball[data-effect="spark"]`,
		`.ball[data-effect="arc"]`,
		`.ball[data-effect="seed"]`,
		`.ball[data-effect="vine"]`,
		`.ball[data-effect="drop"]`,
		`.ball[data-effect="jet"]`,
		// 贴边 / 减少动效都不播连续动画
		`.ball.docked .fx { display: none; }`,
		`@media (prefers-reduced-motion: reduce)`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("球面蛙跳/特效样式缺少 %q", want)
		}
	}

	// 防回归：翻转只能由 .pet-fallback 做一次。frog-hop 里若再写 scaleX(-1)，
	// 会和立绘自身的翻转抵消 —— 表现为「静止朝右、跑起来朝左」。
	if start := strings.Index(css, "@keyframes frog-hop {"); start >= 0 {
		if end := strings.Index(css[start:], "\n}"); end >= 0 {
			if block := css[start : start+end]; strings.Contains(block, "scaleX(-1)") {
				t.Error("frog-hop 不应再包含 scaleX(-1)（会与立绘翻转叠加成朝左）")
			}
		}
	}

	// 判定必须落在可单测的纯函数模块里。
	effectsData, err := frontendFS.ReadFile("frontend/assets/ball-effects.js")
	if err != nil {
		t.Fatal(err)
	}
	effects := string(effectsData)
	for _, want := range []string{
		`export function hopDuration`,
		`export function effectTierForSpeed`,
		`export function ballEffectsState`,
		`export const PET_EFFECTS`,
		`EFFECT_TIER_STARTS = [0, 100, 300]`,
		`0.84 - 0.64 * t`,
		`!caught && !reducedMotion && seconds > 0`,
	} {
		if !strings.Contains(effects, want) {
			t.Errorf("速度特效判定缺少 %q", want)
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
		// 档位必须真的写到 DOM 上，CSS 的 [data-run-tier] 才生效。
		`ball.dataset.runTier = String(face.runTier)`,
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
