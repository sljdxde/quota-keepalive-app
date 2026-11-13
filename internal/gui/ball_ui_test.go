package gui

import (
	"math"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/settings"
	"qka/internal/tokenspeed"
)

// 悬浮球换成 agent-speed 的 LED 仪表（spec/DESIGN.md §2）：canvas 画 36 段
// 热谱环 + 彗星 + 火花/余烬 + 冲击波 + 破纪录金光；数字仍由 DOM 连滚。
// 手势、贴边、右键菜单保持不变。
func TestFloatingBallUsesLEDGauge(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bar.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`class="ball idle"`,
		`id="ball-gl"`,
		`id="ball-fx"`,
		`<canvas class="led"`,
		`<canvas class="orb-fx"`,
		`id="speed-value"`,
		`id="speed-unit"`,
		`>tok/s<`,
		`"Segoe UI Variable Display", "Segoe UI", system-ui, sans-serif`,
		`readoutOffset: -50`,
		`createSpeedThrottle(500`,
		`createSpeedThrottle`,
		`assets/orb-agent-speed.js`,
		`assets/tokenspeed-viz.js`,
		`createAgentSpeedOrb`,
		`clip: false`,
		`ballReadoutFontSize`,
		`layoutReadout`,
		`orb.setSize`,
		`onText: writeSpeedText`,
		`orb.setState`,
		`orb.pulse`,
		`orb.peak`,
		`orb.setState`,
		`response_seq`,
		`peak_seq`,
		`redline_tokens_per_second`,
		`assets/qka-ui.css`,
		`assets/ball.css`,
		`assets/odometer.js`,
		`assets/bar-gesture.js`,
		`Bound.BarMenu`,
		`Bound.BarStartDrag`,
		`Bound.BarToggle`,
		`shouldStartDrag`,
		`dragTargetOrigin`,
		`Events.On("tokenspeed-changed"`,
		`--wails-draggable: drag`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("悬浮球页面缺少 %q", want)
		}
	}
	// 球面只排两行：数值 + tok/s。称号长度不可控（「極音速飛機」五字）会顶
	// 出球外，且热谱配色在球底上对比不足，整行退役。
	for _, forbidden := range []string{
		`id="speed-title"`,
		`class="title"`,
		`titleEl:`,
		`hostTitle:`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("悬浮球页面不应再包含称号读数 %q", forbidden)
		}
	}
	// 球面不再承载宠物/精灵球：LED 仪表 + 数字是唯一内容（对齐 agent-speed）。
	for _, forbidden := range []string{
		`assets/pokemon/`,
		`pet-icon`,
		`ball-face.js`,
		`ball-effects.js`,
		`qka.pet.selected`,
		`createPokemon3D`,
		`assets/tray/`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("悬浮球页面仍残留旧内容 %q", forbidden)
		}
	}
}

// 64px 球上是「看得清」的硬指标：读数不能是灰的、字号要有可读下限、
// 36 段环未点亮时也要看得见轮廓，且数值宽度必须收在环内。这些都是真机
// 反馈，别为了「更还原原型」把它们调回 6px / 7% 白——原型默认球径 148，
// QKA 这颗只有 64。
func TestFloatingBallStaysLegibleAt64px(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/assets/orb-agent-speed.js")
	if err != nil {
		t.Fatal(err)
	}
	orb := string(data)
	for _, want := range []string{
		// 字号：数值 / 单位 / 称号的硬下限（64px 上分别是 16 / 8 / 8px）。
		`Math.max(10, .25 * S)`,
		`Math.max(8, .086 * S)`,
		`Math.max(8, .076 * S)`,
		// 待机读数 94% 白、单位 82%、称号 72%：球底近黑，再低就糊成灰点。
		`rgba(255,255,255,.94)`,
		`rgba(255,255,255,.82)`,
		`rgba(255,255,255,.72)`,
		// 未点亮刻度 22% 白（原型是 7%）：球缩到 64 后那是唯一的「边框」。
		`(dark ? .22 : .14)`,
	} {
		if !strings.Contains(orb, want) {
			t.Errorf("渲染核缺少可读性常量 %q", want)
		}
	}
	page, err := frontendFS.ReadFile("frontend/bar.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{
		// 数值纯白 + 暗晕；单位 92% 白，活性态球体变亮时也不会糊掉。
		`color: #fff;`,
		`rgba(255, 255, 255, .92) !important`,
		`filter: drop-shadow(0 0 1.5px rgba(0, 0, 0, .9))`,
		// 字号按文本长度自适应，长数字不出 36 段环。
		`ballReadoutFontSize`,
		`ballReadoutUnitSize`,
		`setProperty("font-size"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("bar.html 的读数可读性兜底缺少 %q", want)
		}
	}
	if strings.Contains(html, `class="title"`) {
		t.Error("球面不应再显示称号：长称号会顶出球外")
	}
}

// 悬浮球左键面板：加载与 macOS 菜单栏面板同一份 panel.html（签到 / 速度 /
// 宠物三 tab），固定尺寸、不透明背景、带主题参数；不再是旧精简卡片。
func TestBallPanelOptionsLoadSharedPanelPage(t *testing.T) {
	opts := ballPanelOptions("dark")
	if opts.Width != ballPanelWidth || opts.Height != ballPanelHeight ||
		opts.MinWidth != ballPanelWidth || opts.MinHeight != ballPanelHeight ||
		opts.MaxWidth != ballPanelWidth || opts.MaxHeight != ballPanelHeight {
		t.Fatalf("面板尺寸应固定为 %d×%d，实际 %+v", ballPanelWidth, ballPanelHeight, opts)
	}
	if !opts.Frameless || !opts.AlwaysOnTop || !opts.DisableResize || !opts.HideOnEscape {
		t.Errorf("面板缺少弹出层行为: %+v", opts)
	}
	if opts.URL != "/panel.html?theme=dark" {
		t.Errorf("面板应加载与菜单栏相同的 panel.html，实际 %q", opts.URL)
	}
	if opts.BackgroundType != application.BackgroundTypeSolid {
		t.Errorf("Windows 面板应使用不透明背景（panel.html 按 platform-opaque 铺底），实际 %+v", opts.BackgroundType)
	}
	if !opts.Windows.DisableFramelessWindowDecorations {
		t.Error("面板是无边框自绘窗口，不能保留原生装饰（会多一条系统标题栏）")
	}
	// 弹出层按 compact scale 折算：窗口尺寸必须等于逻辑版面 × 系数，
	// 且 panel.html 的 CSS zoom 用的是同一个值，否则会溢出或被裁切。
	if wantW := int(math.Round(392 * ballPanelCompactScale)); ballPanelWidth != wantW {
		t.Errorf("面板宽应按 %v 缩放：want %d, got %d", ballPanelCompactScale, wantW, ballPanelWidth)
	}
	if wantH := int(math.Round(620 * ballPanelCompactScale)); ballPanelHeight != wantH {
		t.Errorf("面板高应按 %v 缩放：want %d, got %d", ballPanelCompactScale, wantH, ballPanelHeight)
	}
	page, err := frontendFS.ReadFile("frontend/panel.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{`"platform-compact"`, "html.platform-compact { zoom: .86; }"} {
		if !strings.Contains(html, want) {
			t.Errorf("panel.html 缺少 Windows 缩放实现 %q", want)
		}
	}
	if ballPanelCompactScale != 0.86 {
		t.Errorf("ballPanelCompactScale=%v 必须与 panel.html 的 zoom 值一致（0.86）", ballPanelCompactScale)
	}
}

// bar.html 球面速度数字必须跟随主应用的显示方式设置：接入
// speed-display.js（roll/fade/direct）与采样频率自适应时长，禁止退回
// 固定时长连滚（500ms 推送下 520ms 动画永远落不了位）。
func TestBallSpeedFollowsDisplaySettings(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bar.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`"./assets/speed-display.js"`,
		`normalizeSpeedDisplay(values.token_speed_display)`,
		`rollDurationFor(speedPollMillis)`,
		`"token-speed-settings-changed"`,
		`rollDurationFor(speedPollMillis)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("球面速度渲染缺少设置接入 %q", want)
		}
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
	if !mini.Windows.DisableFramelessWindowDecorations {
		t.Error("迷你宠物不能保留原生装饰（会多一条系统标题栏）")
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
	if !menu.Windows.DisableFramelessWindowDecorations {
		t.Error("右键菜单不能保留原生装饰（会多一条系统标题栏）")
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
	// 必须全透明（Transparent），不能是半透明（Translucent）：Translucent 会
	// 额外向系统要一层背景材质（Win11 是 Mica，Win10 退化成 legacy
	// ACCENT_ENABLE_BLURBEHIND），那就是球四周那圈「背景框」的来源——它在
	// 页面底下，CSS 再怎么把 body 设成 transparent 也盖不住。
	if opts.BackgroundType != application.BackgroundTypeTransparent {
		t.Errorf("悬浮球窗口应使用全透明背景（Transparent），避免系统材质在球四周留下一圈背景框，实际 %v", opts.BackgroundType)
	}
	// 不能保留原生装饰：Wails 在每次 WM_ACTIVATE 都会把 DWM 边框重新延伸
	// 进客户区（DwmExtendFrameIntoClientArea(1)），透明页面上就是一层暗色
	// 圆角底板 + 右上角 ×。创建后清一次边距留不住，必须从这个开关上关掉。
	if !opts.Windows.DisableFramelessWindowDecorations {
		t.Error("悬浮球窗口必须关闭原生装饰（DisableFramelessWindowDecorations）")
	}
	if !opts.Windows.HiddenOnTaskbar {
		t.Error("悬浮球窗口不应出现在任务栏")
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
	// 窗口 = 1.9 × 球径（与 agent-speed 原型画布一致）：球 64 → 窗口 122，
	// 贴边球 40 → 窗口 76。外圈光环、彗星与火花必须画得出来。
	if ballDiscSize != 64 || ballSize != 122 {
		t.Errorf("球径应为 64、窗口 122，实际 %d / %d", ballDiscSize, ballSize)
	}
	if ballDockSize != 76 {
		t.Errorf("贴边窗口应为 76，实际 %d", ballDockSize)
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
		`window.innerWidth < 99`,
		`width: window.innerWidth,`,
		`height: window.innerHeight,`,
		`ball.style.setProperty("--dock-x"`,
		`ball.style.setProperty("--dock-y"`,
		`orb.setSize(ballDiameter())`,
		`layoutReadout(lastSpeedText)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("贴边态页面缺少 %q", want)
		}
	}

	cssData, err := frontendFS.ReadFile("frontend/assets/ball.css")
	if err != nil {
		t.Fatal(err)
	}
	// 贴边态：内容按 --dock-x/--dock-y 向屏内平移，画布跟着重算尺寸。
	// 球面样式在 bar.html 的内联 <style> 里（ball.css 只保留通用外壳）。
	_ = cssData
	for _, want := range []string{
		`.ball .led, .ball .orb-fx`,
		`transform: translate(-50%, -50%);`,
	} {
		if !strings.Contains(page, want) {
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

// 三个弹出层（面板/迷你宠物/右键菜单）必须在 WM_CLOSE 时取消销毁：窗口一旦
// 被销毁，App 缓存的指针悬空，之后的 Show 全部静默失败——左键就再也弹不出
// 面板了。悬浮球与主窗口早有同样的 RegisterHook，这里锁住三个弹出层不遗漏。
func TestPopoverWindowsCancelWindowClosing(t *testing.T) {
	data, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, ensure := range []string{
		"ballPanelOptions(a.theme)",
		"petMiniOptions(a.theme)",
		"ballMenuOptions(a.theme)",
	} {
		start := strings.Index(source, ensure)
		if start < 0 {
			t.Fatalf("app.go 缺少 %s 的窗口创建", ensure)
		}
		window := source[start : start+900]
		if !strings.Contains(window, "RegisterHook(events.Common.WindowClosing") ||
			!strings.Contains(window, "event.Cancel()") {
			t.Errorf("%s 创建的弹出窗口缺少 WindowClosing 取消销毁钩子", ensure)
		}
	}
}

// 红线默认值三处必须一致：Go 侧 DefaultRedline、设置项 Default、前端共享
// 常量 DEFAULT_REDLINE。任一处漂移都会让「仪表看着不亮」或「设置页显示与
// 实际生效值不符」这类问题重现。
func TestRedlineDefaultsStayInSync(t *testing.T) {
	if tokenspeed.DefaultRedline != 500 {
		t.Fatalf("Go 侧默认红线应为 500，实际 %v", tokenspeed.DefaultRedline)
	}
	var field *settings.Field
	for i := range settings.Schema {
		if settings.Schema[i].Key == settings.KeyTokenSpeedRedline {
			field = &settings.Schema[i]
		}
	}
	if field == nil {
		t.Fatal("设置 schema 缺少速度红线项")
	}
	if field.Default != "500" {
		t.Errorf("设置页默认红线应为 500，实际 %q", field.Default)
	}
	viz, err := frontendFS.ReadFile("frontend/assets/tokenspeed-viz.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(viz), "export const DEFAULT_REDLINE = 500;") {
		t.Error("前端 DEFAULT_REDLINE 应为 500")
	}
}
