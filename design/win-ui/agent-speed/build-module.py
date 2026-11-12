import io
import re

ROOT = r"D:\003代码目录\quota-keepalive-app"
src = io.open(ROOT + r"\design\win-ui\agent-speed\led-effects.html", encoding="utf-8").read()
body = re.search(r"<script>\s*\n(.*?)\n</script>", src, re.S).group(1)


def cut(start_marker, end_marker):
    return body[body.index(start_marker):body.index(end_marker)]


palette_to_demo = cut("// ---------- palette ----------", "// ---------- demo timeline ----------")
tier_and_wave = cut("function tierOf(", "// ---------- simulation ----------")
simulation = cut("// ---------- simulation ----------", "function firePeak(")
fire_peak = cut("function firePeak(", "// ---------- rendering ----------")
rendering = cut("// ---------- rendering ----------", "  // ---------- controls ----------")

# ---- 适配 1：删掉原型写死的 TITLES / TIER_AT（header 按红线动态生成） ----
old_titles = (
    '  const TITLES = [[0, "步行"], [40, "自行車"], [60, "電動自行車"], [80, "摩托車"], [120, "汽車"], [160, "跑車"], [240, "高鐵"],\n'
    '    [340, "賽車"], [480, "磁浮列車"], [680, "噴射客機"], [1000, "戰鬥機"], [1400, "極音速飛機"], [2000, "火箭"]];\n'
)
old_tier_at = "  const TIER_AT = [0, .5, .15 * R, .375 * R, .75 * R];\n"
assert old_titles in palette_to_demo
palette_to_demo = palette_to_demo.replace(old_titles, "")
assert old_tier_at in palette_to_demo
palette_to_demo = palette_to_demo.replace(old_tier_at, "")

# ---- 适配 2：resize() 支持贴圆裁剪 + 字号下限 ----
old_resize = (
    "  function resize() {\n"
    "    const rect = stage.getBoundingClientRect();\n"
    "    st.S = Math.max(96, Math.min(st.size, rect.height * .56, rect.width * .62));\n"
    "    dpr = Math.min(2, window.devicePixelRatio || 1);\n"
    "    W = Math.round(st.S * 1.9);"
)
new_resize = chr(10).join([
    '  function resize() {',
    '    // clip 模式：画布就是圆本身（QKA 悬浮球窗口是圆形区域，外部看不见）。',
    '    // 非 clip 模式画布 = 1.9S，与 agent-speed 原型完全一致：外圈光环到',
    '    // 0.75S、彗星与火花更远，1.5S 会把它们裁掉。',
    '    const rect = (stage && stage.getBoundingClientRect) ? stage.getBoundingClientRect() : null;',
    '    const boxW = rect ? rect.width : st.size;',
    '    const boxH = rect ? rect.height : st.size;',
    '    // st.size 是**球径**（不是窗口）。曾经用 min(size, boxW) 把窗口尺寸当成',
    '    // 球径，232px 的画布塞进 122px 窗口，球被放大到窗外、只剩一片底色。',
    '    st.S = clip',
    '      ? Math.max(16, Math.min(st.size, boxW || st.size, boxH || st.size))',
    '      : Math.max(16, st.size);',
    '    dpr = Math.min(2, window.devicePixelRatio || 1);',
    '    W = Math.round(st.S * (clip ? 1 : 1.9));',
])
assert old_resize in palette_to_demo
palette_to_demo = palette_to_demo.replace(old_resize, new_resize)

old_fonts = (
    '    valueEl.style.fontSize = (.24 * S) + "px";\n'
    '    unitEl.style.fontSize = (.072 * S) + "px";\n'
    '    titleEl.style.fontSize = (.062 * S) + "px";\n'
    '    unitEl.style.marginTop = (.012 * S) + "px";\n'
    '    titleEl.style.marginTop = (.018 * S) + "px";\n'
    '    pillEl.style.fontSize = (.06 * S) + "px";\n'
    '    pillEl.style.padding = `${.012 * S}px ${.04 * S}px`;\n'
    '    pillEl.style.top = `${-.232 * S}px`;\n'
    "  }\n"
    "  new ResizeObserver(resize).observe(stage);"
)
new_fonts = (
    "    // 字号严格按规格比例（数值 0.24S / 单位 0.072S / 称号 0.062S），\n"
    "    // 只有小到 6px 以下才兜底——不再人为放大，否则 52px 球上称号会\n"
    "    // 占掉小半个球面，和原型完全不像。\n"
    '    if (valueEl) valueEl.style.fontSize = Math.max(8, .24 * S) + "px";\n'
    '    if (unitEl) unitEl.style.fontSize = Math.max(6, .072 * S) + "px";\n'
    '    if (titleEl) titleEl.style.fontSize = Math.max(6, .062 * S) + "px";\n'
    '    if (unitEl) unitEl.style.marginTop = Math.max(1, .012 * S) + "px";\n'
    '    if (titleEl) titleEl.style.marginTop = Math.max(1, .018 * S) + "px";\n'
    "    if (pillEl) {\n"
    '      pillEl.style.fontSize = (.06 * S) + "px";\n'
    "      pillEl.style.padding = `${.012 * S}px ${.04 * S}px`;\n"
    "      pillEl.style.top = `${-.232 * S}px`;\n"
    "    }\n"
    "  }\n"
    '  if (typeof ResizeObserver === "function" && stage) new ResizeObserver(resize).observe(stage);'
)
assert old_fonts in palette_to_demo
palette_to_demo = palette_to_demo.replace(old_fonts, new_fonts)

# ---- 适配 3：去掉演示时间轴 ----
start = simulation.index("    if (st.auto) {")
end = simulation.index("    st.v += (st.target - st.v)")
simulation = simulation[:start] + simulation[end:]

# ---- 适配 4：平滑放慢（数字不再每次采样都跳） ----
sim_old = "    st.v += (st.target - st.v) * (1 - Math.exp(-dt * 4.5));"
sim_new = (
    "    // 目标值跟随：原型 4.5/s（约 220ms），QKA 采样 200-750ms 且实时速率\n"
    "    // 本身带窗口抖动，放到 2.6/s（约 380ms）后数字不再每次采样都跳一下。\n"
    "    st.v += (st.target - st.v) * (1 - Math.exp(-dt * 2.6));"
)
assert sim_old in simulation
simulation = simulation.replace(sim_old, sim_new, 1)

shown_old = "    st.shown += (st.v - st.shown) * (1 - Math.exp(-dt * 10));"
shown_new = (
    "    // 显示值再平滑一层：原型的 10/s 太快，这里 5/s（约 200ms）。\n"
    "    st.shown += (st.v - st.shown) * (1 - Math.exp(-dt * 5));"
)
assert shown_old in simulation
simulation = simulation.replace(shown_old, shown_new, 1)

# ---- 适配 5：firePeak 回调 ----
old_pill = "    st.peakAge = 0;\n    burst();\n    pillEl.animate(["
new_pill = "    st.peakAge = 0;\n    burst();\n    if (opts.onPeak) opts.onPeak();\n    if (pillEl) pillEl.animate(["
assert old_pill in fire_peak
fire_peak = fire_peak.replace(old_pill, new_pill)

# ---- 适配 6：readout 改为回调 + 偏移可配 ----
i = rendering.index("    // readout\n")
readout_new = (
    "    // ---------- readout ----------\n"
    "    // 原型直接写 DOM；这里改为回调：QKA 用连滚数字渲染，预览页用纯文本。\n"
    "    const v = st.shown, tierNow = st.tier;\n"
    "    const text = fmt(v);\n"
    "    if (text !== lastText) {\n"
    "      if (fresh && valueEl) zap(text, lastText, st.forceZap);\n"
    "      if (opts.onText) opts.onText(text, lastText);\n"
    "      else if (valueEl) valueEl.textContent = text;\n"
    "      lastText = text;\n"
    "      st.forceZap = false;\n"
    "    }\n"
    '    let title = "待機";\n'
    "    if (st.v >= .5) for (const [at, name] of TITLES) if (st.v >= at) title = name;\n"
    "    if (titleEl && !opts.hostTitle) titleEl.textContent = title;\n"
    "    const hc = heat(h);\n"
    "    const label = dark ? hc : darken(hc, .18);\n"
    '    const ink = dark ? "rgba(255,255,255,.96)" : "rgba(11,13,18,.92)";\n'
    "    if (valueEl) {\n"
    '      valueEl.style.color = st.v < .5 ? (dark ? "rgba(255,255,255,.6)" : "rgba(11,13,18,.5)") : ink;\n'
    "      const shadows = [];\n"
    "      if (tierNow >= 2) {\n"
    "        const blur = .035 * S * (fresh ? 1 + .8 * o : 1);\n"
    "        shadows.push(`0 0 ${blur}px ${rgba(hc, dark ? .85 : .45)}`);\n"
    "        if (fresh && tierNow >= 3) shadows.push(`0 0 ${blur * 2.4}px ${rgba(hc, dark ? .45 : .2)}`);\n"
    "      }\n"
    "      if (fresh && tierNow >= 4 && dark) {\n"
    "        const off = (.6 + 1.6 * o) * S / 300;\n"
    "        shadows.push(`${-off}px 0 0 rgba(255,60,140,.55)`, `${off}px 0 0 rgba(60,210,255,.55)`);\n"
    "      }\n"
    "      if (fresh && dark && st.v >= .5) shadows.push(`0 0 ${.025 * S}px rgba(0,0,0,.7)`, `0 ${.006 * S}px ${.012 * S}px rgba(0,0,0,.6)`);\n"
    '      valueEl.style.textShadow = shadows.join(",") || "none";\n'
    "      const skew = tierNow >= 3 ? -6 * h * (fresh ? 1.25 : 1) : 0;\n"
    "      valueEl.style.transform = `translate(${st.jitter[0]}px, ${st.jitter[1]}px) skewX(${skew}deg)`;\n"
    "    }\n"
    '    if (unitEl) unitEl.style.color = dark ? "rgba(255,255,255,.55)" : "rgba(0,0,0,.5)";\n'
    '    if (titleEl && !opts.hostTitle) {\n'
    '      titleEl.style.color = st.v < .5\n'
    '        ? (dark ? "rgba(255,255,255,.45)" : "rgba(0,0,0,.4)") : rgba(label);\n'
    '    }\n'
    "    if (readout) readout.style.transform = `translate(-50%, ${opts.readoutOffset || -43}%)`;\n"
    "    if (opts.onFrame) opts.onFrame({ value: st.v, heat: h, overdrive: o, tier: tierNow, text });\n"
    "  }\n"
)
rendering = rendering[:i] + readout_new

header = '''// agent-speed 的悬浮球渲染核，**逐字移植**自官方规范参考原型
// `spec/prototypes/led-effects.html`（zoefix/agent-speed，MIT License (c) zoefix）。
//
// spec/DESIGN.md 第 2.1 节原文：该原型的片元着色器与粒子、电弧、闪电代码是
// 「规范参考实现」，「port those functions instead of re-deriving them」。
// 因此本文件保留原型的着色器源码、粒子发射参数、电弧/闪电几何、热度弹簧、
// 层次与配色；只做了六处必要的容器化改造：
//
//   1. DOM 从「页面里按 id 取」改为从 options 传入，且全部可选；
//   2. 演示时间轴与控制面板移除，改为 setState/pulse/peak 三个入口；
//   3. 新增 clip 模式：QKA 悬浮球窗口本身就是圆形区域，画布按 1xS 渲染；
//   4. 字号加可读下限（52px 球上 .072S 只有 3.7px）；
//   5. 目标值与显示值的跟随放慢到 2.6/s 与 5/s（采样 200-750ms 下更稳）；
//   6. readout 改为 onText 回调，交给调用方决定用哪种数字动效。
//
// 除这些之外没有重写任何视觉逻辑——要改效果请先改上游原型再同步。

/**
 * @param {{
 *   stage?: HTMLElement,
 *   glCanvas: HTMLCanvasElement,
 *   fxCanvas: HTMLCanvasElement,
 *   readout?: HTMLElement, valueEl?: HTMLElement, unitEl?: HTMLElement,
 *   titleEl?: HTMLElement, pillEl?: HTMLElement,
 *   size?: number, redline?: number, dark?: boolean, clip?: boolean,
 *   level?: number, reduceMotion?: boolean, readoutOffset?: number,
 *   onText?: (text: string, prev: string) => void,
 *   onPeak?: () => void,
 *   onFrame?: (state: object) => void,
 * }} options
 */
export function createAgentSpeedOrb(options) {
  "use strict";
  const opts = options || {};
  const clip = opts.clip !== false;
  const stage = opts.stage || null;
  const glCanvas = opts.glCanvas;
  const fxCanvas = opts.fxCanvas;
  const readout = opts.readout || null;
  const valueEl = opts.valueEl || null;
  const unitEl = opts.unitEl || null;
  const titleEl = opts.titleEl || null;
  const pillEl = opts.pillEl || null;
  const reduce = opts.reduceMotion !== undefined
    ? !!opts.reduceMotion
    : (typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches);

  // ---------- 红线与称号阶梯（原型写死 R=2000，这里默认 500） ----------
  // 红线默认 500（与 Go 侧 DefaultRedline 一致）；原型写死 2000，
  // 对 QKA 的日常速度偏高。
  let R = Number(opts.redline) > 0 ? Number(opts.redline) : 500;
  const TITLE_RATIOS = [0, .02, .03, .04, .06, .08, .12, .17, .24, .34, .5, .7, 1];
  const TITLE_NAMES = ["步行", "自行車", "電動自行車", "摩托車", "汽車", "跑車", "高鐵",
    "賽車", "磁浮列車", "噴射客機", "戰鬥機", "極音速飛機", "火箭"];

  const st = {
    size: Number(opts.size) > 0 ? Number(opts.size) : 148,
    S: Number(opts.size) > 0 ? Number(opts.size) : 148,
    dark: opts.dark !== false, fresh: true, level: Number(opts.level) > 0 ? Number(opts.level) : 1,
    target: 0, v: 0, shown: 0, h: 0, o: 0, tier: 0,
    time: 0, comet: 0, flow: 0, phase: 0, phase2: 0, warp: 0, corona: 0,
    flash: 0, peakAge: 99, peakFired: false, waves: [], nextPulse: .8,
    jitter: [0, 0], jitterT: 0, arcT: 0, textT: 0
  };
  let TIER_AT = [0, .5, .15 * R, .375 * R, .75 * R];
  let TITLES = TITLE_RATIOS.map((ratio, index) => [index === 0 ? 0 : Math.round(ratio * R), TITLE_NAMES[index]]);

'''

tail = '''
  // ---------- 对外 API ----------
  let lastFrame = 0, rafId = 0, started = false;

  function frame(now) {
    const dt = Math.min(.05, (now - lastFrame) / 1000);
    lastFrame = now;
    step(dt);
    render();
    rafId = requestAnimationFrame(frame);
  }

  function start() {
    if (started) return;
    started = true;
    lastFrame = performance.now();
    rafId = requestAnimationFrame(frame);
  }
  function stop() {
    started = false;
    if (rafId) cancelAnimationFrame(rafId);
    rafId = 0;
  }

  function applyRedline(value) {
    const next = Number(value);
    if (!(next > 0) || next === R) return;
    R = next;
    TIER_AT = [0, .5, .15 * R, .375 * R, .75 * R];
    TITLES = TITLE_RATIOS.map((ratio, index) => [index === 0 ? 0 : Math.round(ratio * R), TITLE_NAMES[index]]);
    if (gl) gl.uniform4f(loc.uTicks, Math.pow(.5 / R, .6), Math.pow(.15, .6), Math.pow(.375, .6), Math.pow(.75, .6));
  }

  resize();
  lastText = fmt(st.shown);

  return {
    /** 更新目标速度；immediate=true 时就位不滚（首次渲染用）。 */
    setState(next) {
      const state = next || {};
      const value = Math.max(0, Number(state.value) || 0);
      if (state.redline) applyRedline(state.redline);
      if (state.smooth === false) {
        st.target = st.v = st.shown = value;
        st.h = Math.min(1, Math.pow(value / R, .6));
        st.o = Math.min(1, Math.max(0, (value - R) / R));
        st.tier = tierOf(value, 4);
        render();
        if (value < .05) stop();
        return;
      }
      st.target = value;
      if (state.immediate) {
        st.v = st.shown = value;
        st.h = Math.min(1, Math.pow(value / R, .6));
        st.o = Math.min(1, Math.max(0, (value - R) / R));
        st.tier = tierOf(value, 4);
        render();
      }
      start();
    },
    /** 响应完成：扫一圈冲击波（原型的 addWave）。 */
    pulse(strength) {
      if (reduce) return;
      addWave(Math.max(.2, Math.min(1, Number(strength) || .6)));
      start();
    },
    /** 破纪录：金色光芒 + 粒子爆发（原型的 firePeak）。 */
    peak() {
      if (reduce) return;
      firePeak();
      start();
    },
    /** 特效强度：0 关 / 1 标准 / 1.6 极致。 */
    setLevel(level) {
      st.level = Number(level) >= 0 ? Number(level) : 1;
      if (st.level === 0) {
        parts.length = 0;
        arcs.length = 0;
        bolts.length = 0;
        zaps.length = 0;
        st.waves.length = 0;
        st.peakAge = 99;
        render();
        stop();
      } else {
        start();
      }
    },
    setRedline: applyRedline,
    /** 切换深色/浅色球体（agent-speed 的 dark / porcelain 变体）。 */
    setDark(dark) {
      st.dark = !!dark;
      render();
    },
    /** 改变球径（窗口缩放/贴边时用），内部重算画布尺寸与字号。 */
    setSize(size) {
      const next = Number(size);
      if (next > 0) st.size = next;
      resize();
    },
    resize,
    /** 全部静止时停帧，避免置顶小窗常驻 60fps 重绘。 */
    idle() {
      if (st.v < .05 && !st.waves.length && st.peakAge > 1.3 &&
          !parts.length && !bolts.length && !zaps.length && !arcs.length) {
        stop();
      }
    },
    destroy: stop,
    get state() { return st; },
  };
}
'''

module = header + palette_to_demo + tier_and_wave + simulation + fire_peak + rendering + tail
out = ROOT + r"\internal\gui\frontend\assets\orb-agent-speed.js"
io.open(out, "w", encoding="utf-8").write(module)
print("module written:", len(module), "chars")
