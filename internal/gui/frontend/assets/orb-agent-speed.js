// agent-speed 的悬浮球渲染核，**逐字移植**自官方规范参考原型
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

import { PERFORMANCE_LOW_FPS, performanceLow } from "./perf-mode.js";

// 静止判定：速度与目标值都已归零、显示值也收敛到 0，且没有任何待播特效
// 时，帧循环可以自停。抽成纯函数，node --test 无需 WebGL/DOM 也能覆盖。
// state 即渲染核内部的 st；waves/parts/bolts/zaps/arcs 是各类特效队列。
export function orbSettled(state) {
  const s = state || {};
  const v = Number(s.v);
  const target = Number(s.target);
  const peakAge = s.peakAge === undefined ? 99 : Number(s.peakAge);
  const shown = s.shown === undefined ? 0 : Number(s.shown);
  if (!Number.isFinite(v) || !Number.isFinite(target) ||
      !Number.isFinite(peakAge) || !Number.isFinite(shown)) return false;
  const has = list => !!list && list.length > 0;
  return v < 0.05 && target < 0.05 && shown < 0.5 && peakAge > 1.3 &&
    !has(s.waves) && !has(s.parts) && !has(s.bolts) && !has(s.zaps) && !has(s.arcs);
}

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

// ---------- palette ----------
  const STOPS = [[0, "#7C8798"], [.10, "#2DD4BF"], [.30, "#38BDF8"], [.48, "#818CF8"], [.64, "#E879F9"], [.78, "#FB7185"], [.90, "#FB923C"], [1, "#FACC15"]]
    .map(([t, hex]) => [t, [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)]);
  const lin = c => c <= .04045 ? c / 12.92 : Math.pow((c + .055) / 1.055, 2.4);
  const srgb = c => c <= .0031308 ? c * 12.92 : 1.055 * Math.pow(c, 1 / 2.4) - .055;
  function heat(h) {
    h = Math.min(1, Math.max(0, h));
    for (let i = 1; i < STOPS.length; i++) {
      if (h <= STOPS[i][0]) {
        const [t0, a] = STOPS[i - 1], [t1, b] = STOPS[i], k = (h - t0) / (t1 - t0);
        return a.map((x, j) => srgb(lin(x) + (lin(b[j]) - lin(x)) * k));
      }
    }
    return STOPS[STOPS.length - 1][1];
  }
  const HOT = [1, 247 / 255, 214 / 255], AMBER = [1, 176 / 255, 32 / 255];
  const mix = (a, b, k) => a.map((x, i) => x + (b[i] - x) * k);
  const rgba = (c, a = 1) => `rgba(${c.map(x => Math.round(Math.min(1, Math.max(0, x)) * 255)).join(",")},${a})`;
  const darken = (c, k) => c.map(x => x * (1 - k));

  const TIER_NAMES = ["待機", "巡航", "加速", "狂飆", "極限"];
  const fmt = v => v < 1000 ? String(Math.round(v)) : (v / 1000).toFixed(2) + "k";
  const ease = t => 1 - Math.pow(1 - Math.min(1, Math.max(0, t)), 3);

  // ---------- WebGL ----------
  const gl = glCanvas.getContext("webgl2", { premultipliedAlpha: true, antialias: false, alpha: true });
  const VS = `#version 300 es
in vec2 p; void main(){ gl_Position = vec4(p, 0., 1.); }`;
  const FS = `#version 300 es
precision highp float;
out vec4 outColor;
uniform vec2 uRes; uniform float uS, uTime, uH, uO, uDark, uNew, uLevel, uFlash, uPeak, uSweep, uTier;
uniform float uComet, uFlow, uPhase, uPhase2, uWarp, uCorona;
uniform vec4 uWaves[4];
uniform vec4 uTicks;

const vec3 HOT = vec3(1.0, 0.969, 0.839);
vec3 toLin(vec3 c){ return pow(max(c, 0.0), vec3(2.2)); }
vec3 toSrgb(vec3 c){ return pow(max(c, 0.0), vec3(1.0 / 2.2)); }
vec3 heat(float h){
  h = clamp(h, 0.0, 1.0);
  vec3 c0 = vec3(.486,.529,.596), c1 = vec3(.176,.831,.749), c2 = vec3(.220,.741,.973), c3 = vec3(.506,.549,.973),
       c4 = vec3(.910,.475,.976), c5 = vec3(.984,.443,.522), c6 = vec3(.984,.573,.235), c7 = vec3(.980,.800,.082);
  vec3 a; vec3 b; float t0; float t1;
  if (h < .10) { a = c0; b = c1; t0 = 0.; t1 = .10; } else if (h < .30) { a = c1; b = c2; t0 = .10; t1 = .30; }
  else if (h < .48) { a = c2; b = c3; t0 = .30; t1 = .48; } else if (h < .64) { a = c3; b = c4; t0 = .48; t1 = .64; }
  else if (h < .78) { a = c4; b = c5; t0 = .64; t1 = .78; } else if (h < .90) { a = c5; b = c6; t0 = .78; t1 = .90; }
  else { a = c6; b = c7; t0 = .90; t1 = 1.; }
  return toSrgb(mix(toLin(a), toLin(b), (h - t0) / (t1 - t0)));
}
// porcelain: deepen without muddying (gold stays gold, not khaki)
vec3 ink(vec3 c){ return pow(c, vec3(1.55)); }

// 3D simplex noise: Ashima Arts / Stefan Gustavson (MIT)
vec3 mod289(vec3 x){ return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 mod289(vec4 x){ return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 permute(vec4 x){ return mod289(((x * 34.0) + 1.0) * x); }
vec4 taylorInvSqrt(vec4 r){ return 1.79284291400159 - 0.85373472095314 * r; }
float snoise(vec3 v){
  const vec2 C = vec2(1.0 / 6.0, 1.0 / 3.0); const vec4 D = vec4(0.0, 0.5, 1.0, 2.0);
  vec3 i = floor(v + dot(v, C.yyy)); vec3 x0 = v - i + dot(i, C.xxx);
  vec3 g = step(x0.yzx, x0.xyz); vec3 l = 1.0 - g; vec3 i1 = min(g.xyz, l.zxy); vec3 i2 = max(g.xyz, l.zxy);
  vec3 x1 = x0 - i1 + C.xxx; vec3 x2 = x0 - i2 + C.yyy; vec3 x3 = x0 - D.yyy;
  i = mod289(i);
  vec4 p = permute(permute(permute(i.z + vec4(0.0, i1.z, i2.z, 1.0)) + i.y + vec4(0.0, i1.y, i2.y, 1.0)) + i.x + vec4(0.0, i1.x, i2.x, 1.0));
  float n_ = 0.142857142857; vec3 ns = n_ * D.wyz - D.xzx;
  vec4 j = p - 49.0 * floor(p * ns.z * ns.z); vec4 x_ = floor(j * ns.z); vec4 y_ = floor(j - 7.0 * x_);
  vec4 x = x_ * ns.x + ns.yyyy; vec4 y = y_ * ns.x + ns.yyyy; vec4 h = 1.0 - abs(x) - abs(y);
  vec4 b0 = vec4(x.xy, y.xy); vec4 b1 = vec4(x.zw, y.zw);
  vec4 s0 = floor(b0) * 2.0 + 1.0; vec4 s1 = floor(b1) * 2.0 + 1.0; vec4 sh = -step(h, vec4(0.0));
  vec4 a0 = b0.xzyw + s0.xzyw * sh.xxyy; vec4 a1 = b1.xzyw + s1.xzyw * sh.zzww;
  vec3 p0 = vec3(a0.xy, h.x); vec3 p1 = vec3(a0.zw, h.y); vec3 p2 = vec3(a1.xy, h.z); vec3 p3 = vec3(a1.zw, h.w);
  vec4 norm = taylorInvSqrt(vec4(dot(p0, p0), dot(p1, p1), dot(p2, p2), dot(p3, p3)));
  p0 *= norm.x; p1 *= norm.y; p2 *= norm.z; p3 *= norm.w;
  vec4 m = max(0.6 - vec4(dot(x0, x0), dot(x1, x1), dot(x2, x2), dot(x3, x3)), 0.0); m = m * m;
  return 42.0 * dot(m * m, vec4(dot(p0, x0), dot(p1, x1), dot(p2, x2), dot(p3, x3)));
}
float fbm(vec3 p){ float a = .5; float s = 0.; for (int i = 0; i < 4; i++) { s += a * snoise(p); p *= 2.03; a *= .5; } return s; }
float hash11(float n){ return fract(sin(n * 127.1) * 43758.5453); }
float hash21(vec2 p){ p = fract(p * vec2(123.34, 456.21)); p += dot(p, p + 45.32); return fract(p.x * p.y); }

vec4 acc;
// dark glass: light adds up; porcelain: colors are laid on like ink
void glow(vec3 c, float a){
  a = clamp(a, 0., 1.);
  if (uDark > .5) { acc.rgb += c * a; acc.a = max(acc.a, a * .35); }
  else { acc.rgb = c * a + acc.rgb * (1. - a); acc.a = a + acc.a * (1. - a); }
}
void paint(vec3 c, float a){ a = clamp(a, 0., 1.); acc.rgb = c * a + acc.rgb * (1. - a); acc.a = a + acc.a * (1. - a); }
float easeOut(float t){ return 1. - pow(1. - clamp(t, 0., 1.), 3.); }

// Silk core, drawn over the glass body and under the gauge.
// p: orb coords (rim at r = .5, y up), r = length(p); h: heat 0..1; o: overdrive 0..1 past the redline;
// act: 0 at idle, 1 once moving; lvl: effect level (1 standard, 1.6 max); waveField: response-pulse ring field.
// Silk: a satin height field of long diagonal folds; a key light and a rim light in the speed palette glide over it.
float silk_box(vec2 p, vec2 b, float rad){
  vec2 d = abs(p) - b + rad;
  return length(max(d, 0.)) + min(max(d.x, d.y), 0.) - rad;
}

float silk_fold(float ph){ return sin(ph + .25 * sin(ph)); }
float silk_bell(float x){ return exp(-x * x); }

float silk_height(vec2 p, float t, float rip, float wave){
  float X = dot(p, vec2(.80, .60));
  float Y = dot(p, vec2(-.60, .80)) * (1. + .45 * X)
          + .03 * snoise(vec3(p * 1.1, t * .07)) + .025 * sin(X * 5. + t * .3) + .03 * sin(X * 1.6 - t * .17);
  float hgt = .5 * sin(Y * 6. + .25 * sin(X * 3.3 + t * .2) + t * .15)
            + .6 * (.7 + .3 * sin(X * 2.8 + 1. + t * .1)) * silk_fold(Y * 16.5 + .35 * sin(X * 4.1 - t * .35) + t * .6)
            + .22 * (.5 + .5 * sin(X * 3.6 - t * .25 + 2.)) * silk_fold(Y * 27. + .4 * sin(X * 4.7 + t * .4) - t * .8 + 2.);
  if (rip > 0.) {
    float packet = max(sin(Y * 4. - uTime * 2.6 + 1.3 * sin(X * 2.7)), 0.);
    hgt += rip * .05 * packet * packet * sin(Y * 44. + X * 6. - uTime * 16.);
  }
  hgt += wave * .11 * sin(length(p) * 36. - uTime * 8.);
  return hgt;
}

void silkCore(vec2 p, float r, float h, float o, float act, float lvl, bool dark, float waveField){
  float fade = smoothstep(.475, .385, r);
  if (fade <= 0.) return;
  float t = uPhase;
  float rip = min(1., o * 2.5);
  vec2 sp = p - p / max(r, 1e-3) * .03 * waveField;
  float e = .008;
  vec2 ac = vec2(-.6, .8), al = vec2(.8, .6);
  float h0 = silk_height(sp, t, rip, waveField);
  float hA = silk_height(sp + ac * e, t, rip, waveField);
  float hB = silk_height(sp - ac * e, t, rip, waveField);
  float hL = silk_height(sp + al * e, t, rip, waveField);
  vec2 g = ac * (hA - hB) / (2. * e) + al * (hL - h0) / e;
  vec3 n = normalize(vec3(-g * .05, 1.));
  // positive where the fold curves over a crest
  float crest = smoothstep(-.15, .75, -(hA - 2. * h0 + hB) / (e * e) * .006);

  float tx = min(silk_box(p - vec2(0., .03), vec2(.27, .07), .07), silk_box(p - vec2(0., -.17), vec2(.13, .06), .06));
  float textMask = mix(.3, 1., smoothstep(-.02, .1, tx));

  float az = 2.2 + .3 * sin(uPhase2 * .35);
  vec2 ld = vec2(cos(az), sin(az));
  float sl = dot(n.xy, ld);
  float ao = .2 + .8 * smoothstep(-1.6, 1.1, h0);
  float body = pow(smoothstep(-.12, .5, sl), 1.6) * ao;
  float sheen = silk_bell((sl - .3) / .11) * ao;
  float glint = silk_bell((sl - .33) / .045) * (.35 + .65 * crest) * ao;
  float rim = silk_bell((sl + .42) / .1) * (.4 + .6 * ao);

  float X = dot(p, vec2(.8, .6));
  float pk = uPeak < 1.4 ? 1. - smoothstep(.7, 1.4, uPeak) : 0.;
  float sweep = pk * silk_bell((X - mix(-.55, .6, easeOut(uPeak / 1.2))) / .13);
  float amp = (.3 + .65 * act + .3 * h + .25 * o) * (1. + .35 * (lvl - 1.)) * (1. + .3 * uFlash + .5 * waveField + 2.2 * sweep + .4 * pk);
  float k = fade * textMask * amp;

  float glide = .05 * sin(X * 3.2 - uPhase2 * .8);
  float spread = mix(.08, 1., act);
  float hotK = clamp(smoothstep(0., .3, o) + sweep, 0., 1.);
  vec3 gold = vec3(1., .84, .45);
  vec3 cBody = heat(h + (-.22 + .3 * smoothstep(-.1, .5, sl) + glide) * spread);
  vec3 cSheen0 = heat(h + (.06 + glide) * spread);
  vec3 cSheen = mix(cSheen0, mix(gold, HOT, .35), hotK);
  vec3 cGlint = mix(cSheen, HOT, .55 + .3 * hotK);
  vec3 cRim = heat(h + (-.32 - glide) * spread);

  if (dark) {
    paint(vec3(.008, .01, .016), .7 * fade);
    glow(cBody, body * .32 * k);
    glow(cSheen, sheen * .75 * k);
    glow(cGlint, glint * .7 * k);
    glow(cRim, rim * .35 * k);
    glow(mix(gold, HOT, .4), sweep * (.12 + .6 * body + .8 * sheen) * fade * textMask);
  } else {
    float tm = .4 + .6 * textMask;
    paint(vec3(.99, .985, .978), .55 * fade);
    paint(vec3(.66, .67, .72), (pow(smoothstep(.15, -.45, sl), 1.3) * .38 + (1. - ao) * .12) * fade * tm);
    glow(ink(cRim), rim * .22 * k);
    glow(mix(ink(cSheen0), vec3(.96, .66, .16), hotK), sheen * (.3 + .15 * hotK) * k);
    paint(vec3(1.), (glint * .75 + sheen * .35) * fade * tm);
    glow(ink(gold), sweep * (.1 + .4 * body) * fade * tm);
  }
}

void main(){
  float px = 1.0 / uS;
  vec2 p = (gl_FragCoord.xy - uRes * .5) / uS;
  float r = length(p);
  float ang = degrees(atan(-p.y, p.x)); if (ang < 0.) ang += 360.;
  float u = mod(ang - 135., 360.) / 270.;
  float h = uH; float o = uO; float act = smoothstep(0., .08, h);
  float lvl = uLevel;
  bool dark = uDark > .5; bool fresh = uNew > .5;
  acc = vec4(0.);
  vec3 hc = heat(h);
  vec3 hcI = dark ? hc : ink(hc);
  vec3 hot = mix(hc, HOT, .55 + .45 * o);

  float waveField = 0.;
  for (int i = 0; i < 4; i++) {
    vec4 w = uWaves[i];
    if (w.x < 0. || w.x > 1.) continue;
    float rr = .1 + .4 * easeOut(w.x);
    waveField += w.y * (1. - w.x) * exp(-pow((r - rr) / .05, 2.));
  }

  // drop shadow
  float sh = smoothstep(.6, .46, length(p - vec2(0., -.03))) * (dark ? .45 : .16);
  paint(vec3(0.), sh * smoothstep(.49, .52, r));

  // aura
  float breathe = 1. + .15 * sin(uTime * mix(1.57, 9., h));
  float aura = smoothstep(.75, .5, r) * smoothstep(.46, .52, r) * (dark ? .55 : .32) * h * breathe;
  glow(fresh ? mix(hcI, HOT, .25 * o) : hcI, aura * (1. + .6 * uFlash) * (fresh ? .8 : 1.));

  if (fresh) {
    // corona: flame tongues licking off the rim
    float cAmt = smoothstep(.45, .85, h) * (.45 + .55 * o) * lvl;
    if (cAmt > .001 && r > .47 && r < .82) {
      vec2 dir = p / max(r, 1e-4);
      float rise = (r - .5) * 9. - uCorona;
      float warpN = snoise(vec3(dir * 3., rise * .5));
      float ridge = 1. - abs(snoise(vec3(dir * 7. + warpN * .35, rise)));
      ridge = pow(ridge, 5.);
      float body = .5 + .5 * snoise(vec3(dir * 2.2, uCorona * .35));
      float reach = .02 + (.06 * h + .12 * o) * pow(body, 1.4) * 1.7;
      float d = (r - .499) / reach;
      float fall = exp(-d * 2.4) * smoothstep(.49, .503, r) * smoothstep(.81, .7, r);
      float tongues = smoothstep(.45, .8, .5 + .5 * snoise(vec3(dir * 4.5, rise * .35)));
      float flame = ridge * fall * mix(.12, 1.25, tongues) * (.45 + .7 * body);
      float base = exp(-max(r - .5, 0.) / .012) * smoothstep(.49, .503, r) * .35;
      vec3 fc = mix(HOT, hc, clamp(d * .9, 0., 1.));
      glow(dark ? fc : ink(hc), (flame + base) * cAmt * (dark ? .95 : .95));
    }
    // warp streaks rushing into the orb
    float wAmt = smoothstep(.32, .62, h) * lvl;
    if (wAmt > .001 && r > .5 && r < .9) {
      float bins = 96.;
      float bi = floor(ang / 360. * bins);
      float seed = hash11(bi + 3.);
      if (seed < .18 + .42 * h) {
        float sp = .55 + seed * .9;
        float head = fract(seed * 7.3 + uWarp * sp);
        float rh = mix(.9, .51, head);
        float len = (.02 + .08 * h + .07 * o) * (.55 + seed);
        float along = (r - rh) / len;
        float acrossDeg = abs(fract(ang / 360. * bins) - .5) * 360. / bins;
        float across = radians(acrossDeg) * r;
        float line = smoothstep(.0018 + .001 * h, 0., across) * pow(smoothstep(1., 0., along), 1.6) * step(0., along);
        float fade = smoothstep(0., .3, head) * smoothstep(1., .82, head);
        vec3 wc = mix(hc, HOT, .45);
        glow(dark ? wc : ink(hc), line * fade * wAmt * (dark ? .8 : .75));
      }
    }
  }

  // body
  float disc = smoothstep(.5 + px, .5 - px, r);
  if (disc > 0.) {
    // 球体本身提亮一档：64px 小件在浅色桌面上要能一眼认出轮廓，原来的
    // 深色渐变在浅底上只剩一个黑洞。
    vec3 top = dark ? vec3(.13, .15, .21) : vec3(1.);
    vec3 bot = dark ? vec3(.05, .06, .085) : vec3(.906, .922, .953);
    paint(mix(bot, top, clamp(.5 + p.y, 0., 1.)), disc * (dark ? .94 : .96));
    paint(vec3(1.), smoothstep(.36, 0., length(p - vec2(-.16, .19))) * (dark ? .08 : .3) * disc);

    if (fresh && r < .475) silkCore(p, r, h, o, act, lvl, dark, waveField);

    float rim = smoothstep(.5, .49, r) * smoothstep(.473, .495, r) * clamp(.5 + p.y * 1.4, 0., 1.);
    paint(vec3(1.), rim * (dark ? .18 : .55));
  }

  // gauge
  // 36 段 LED 环是这颗球唯一的「进度条」，64px 球上原来每段只有 2.9×2.4px、
  // 未点亮处 7% 白，肉眼几乎看不见。这里把段加长加粗（.03/.062 半径）并把
  // 未点亮亮度提到 22%，贴边看是一圈清楚的刻度环，跑起来才有对比度。
  float segR = .41; float segHalfLen = .03;
  float lit36 = h * uSweep * 36.;
  if (u <= 1. && abs(r - segR) < .1) {
    float iNear = clamp(floor(u * 36.), 0., 35.);
    float best = 1e3; float bi = iNear;
    for (int k = -1; k <= 1; k++) {
      float j = clamp(iNear + float(k), 0., 35.);
      float ca = 135. + (j + .5) * 7.5;
      float dAng = radians(mod(ang - ca + 540., 360.) - 180.);
      vec2 lp = vec2(dAng * r, r - segR);
      vec2 hb = vec2(segR * radians(6.2) * .5, segHalfLen);
      vec2 dq = abs(lp) - hb + .006;
      float sdf = length(max(dq, 0.)) + min(max(dq.x, dq.y), 0.) - .006;
      if (sdf < best) { best = sdf; bi = j; }
    }
    float seg = smoothstep(px * 1.2, -px * 1.2, best);
    float litF = clamp(lit36 - bi, 0., 1.);
    float tipI = floor(max(lit36 - .001, 0.));
    vec3 sc = heat(bi / 35.);
    if (!dark) sc = ink(sc);
    float shimmer = uTier > 3.5 ? .75 + .25 * hash11(bi * 13.1 + floor(uTime * 12.)) : 1.;
    float flow = 0.;
    if (fresh) {
      float ph = fract(bi / 36. * 2.2 - uFlow);
      flow = pow(1. - ph, 9.) * act * (.5 + .9 * h);
    }
    vec3 lit = mix(sc, dark ? HOT : sc * 1.15, flow * .75);
    if (bi == tipI && litF > .01) lit = dark ? HOT : sc;
    float bright = litF * shimmer * (1. + .25 * uFlash + flow * .9);
    paint(dark ? vec3(1.) : vec3(0.), seg * (dark ? .22 : .14) * (1. - litF));
    if (dark) glow(lit, seg * bright); else paint(lit, seg * min(1., bright));
  }
  if (abs(r - segR) < .17 && act > 0.) {
    float gapA = u > 1. ? min(u - 1., 4. / 3. - u) * 270. : 0.;
    float uu = u > 1. ? (u - 1. < 4. / 3. - u ? 1. : 0.) : u;
    float inLit = smoothstep(lit36 + .5, lit36 - .5, uu * 36.);
    float dRad = max(abs(r - segR) - segHalfLen, 0.) + radians(gapA) * r;
    float g = exp(-dRad / (.014 + .018 * h + .02 * o)) * inLit * act * smoothstep(.17, .1, abs(r - segR));
    float gAmt = (.16 + .5 * h + .45 * o) * (fresh ? 1.3 : .8) * lvl;
    if (uTier > 3.5) gAmt *= .85 + .3 * sin(uTime * (7. + 6. * o));
    vec3 gc = heat(uu);
    glow(dark ? gc : ink(gc), g * gAmt * (dark ? .55 : .22));
  }

  // tier tick dots
  for (int t = 0; t < 4; t++) {
    float th = uTicks[t];
    float ta = radians(135. + th * 270.);
    vec2 tp = vec2(cos(ta), -sin(ta)) * .463;
    float on = step(th, h + .0005);
    vec3 tc = on > .5 ? (dark ? heat(th) : ink(heat(th))) : (dark ? vec3(1.) : vec3(0.));
    paint(tc, smoothstep(.0072, .0042, length(p - tp)) * (on > .5 ? .95 : .34));
  }

  // tip flare
  if (h > .02) {
    float ta = radians(135. + min(h * uSweep, 1.) * 270.);
    vec2 tp = vec2(cos(ta), -sin(ta)) * segR;
    vec2 d = p - tp;
    vec2 tang = vec2(-sin(ta), -cos(ta));
    vec2 rad = normalize(tp);
    float bloom = exp(-length(d) / (.01 + .016 * h));
    glow(dark ? HOT : hcI, bloom * (.3 + .35 * h) * (dark ? 1. : .5));
    if (fresh) {
      float star = exp(-abs(dot(d, tang)) / (.05 + .16 * o)) * exp(-abs(dot(d, rad)) / .0032)
                 + exp(-abs(dot(d, rad)) / (.035 + .09 * o)) * exp(-abs(dot(d, tang)) / .0028);
      float fAmt = smoothstep(.5, .85, h) * (.55 + .9 * o) * lvl;
      glow(dark ? HOT : hcI, star * fAmt * (dark ? .9 : .4));
    }
  }

  // comets
  if (h > .01) {
    int nC = (fresh && h > .55) ? 2 : 1;
    for (int c = 0; c < 2; c++) {
      if (c >= nC) break;
      float head = mod(uComet * 360. + float(c) * 180., 360.);
      float behind = mod(head - ang + 360., 360.);
      float tail = fresh ? 40. + 60. * h : 40.;
      float rc = .455;
      float band = exp(-pow((r - rc) / (.0035 + .0045 * h), 2.));
      float along = clamp(1. - behind / tail, 0., 1.);
      float cA = step(behind, tail) * along * along * band;
      float hr = radians(head);
      float headGlow = exp(-length(p - vec2(cos(hr), -sin(hr)) * rc) / (.01 + .006 * h));
      float op = (.25 + .6 * h) * (c == 1 ? .7 : 1.);
      vec3 cc = mix(hc, HOT, along);
      glow(dark ? cc : ink(hc), (cA + headGlow * .9) * op * (dark ? 1. : .6));
    }
  }

  // shockwaves
  for (int i = 0; i < 4; i++) {
    vec4 w = uWaves[i];
    if (w.x < 0. || w.x > 1.) continue;
    float rr = .5 + .24 * easeOut(w.x);
    float wid = (.005 + .005 * w.y) * (1. - w.x) + .001;
    float ring = exp(-pow((r - rr) / wid, 2.)) * (1. - w.x) * .7 * w.y;
    vec3 wc = mix(heat(w.z), HOT, .3);
    glow(dark ? wc : ink(heat(w.z)), ring * (dark ? 1. : .6));
    if (fresh) {
      float rr2 = .5 + .16 * easeOut(w.x * 1.3);
      glow(dark ? heat(w.z) : ink(heat(w.z)), exp(-pow((r - rr2) / (wid * 3.5), 2.)) * (1. - w.x) * .3 * w.y);
    }
  }

  // new peak: the whole disc flashes and a gold ring blows outward
  if (uPeak < 1.4) {
    float f = exp(-uPeak * 4.5);
    glow(dark ? HOT : vec3(1., .69, .13), f * (dark ? .75 : .35) * smoothstep(.6, .15, r));
    float ringR = .5 + .5 * easeOut(uPeak / 1.4);
    glow(vec3(1., .78, .3), exp(-pow((r - ringR) / (.012 + .02 * uPeak), 2.)) * (1. - uPeak / 1.4) * (fresh ? 1. : .5));
  }

  outColor = acc;
}`;

  function compile(type, src) {
    const s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s));
    return s;
  }
  let prog, loc = {};
  if (gl) {
    prog = gl.createProgram();
    gl.attachShader(prog, compile(gl.VERTEX_SHADER, VS));
    gl.attachShader(prog, compile(gl.FRAGMENT_SHADER, FS));
    gl.bindAttribLocation(prog, 0, "p");
    gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(prog));
    gl.useProgram(prog);
    const buf = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
    gl.enableVertexAttribArray(0); gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
    for (const n of ["uRes", "uS", "uTime", "uH", "uO", "uDark", "uNew", "uLevel", "uFlash", "uPeak", "uSweep", "uTier",
      "uComet", "uFlow", "uPhase", "uPhase2", "uWarp", "uCorona", "uWaves", "uTicks"]) loc[n] = gl.getUniformLocation(prog, n);
    gl.uniform4f(loc.uTicks, Math.pow(.5 / R, .6), Math.pow(.15, .6), Math.pow(.375, .6), Math.pow(.75, .6));
  }
  const fx = fxCanvas.getContext("2d");

  // ---------- sizing ----------
  let dpr = 1, W = 0;
  function resize() {
    // clip 模式：画布就是圆本身（QKA 悬浮球窗口是圆形区域，外部看不见）。
    // 非 clip 模式画布 = 1.9S，与 agent-speed 原型完全一致：外圈光环到
    // 0.75S、彗星与火花更远，1.5S 会把它们裁掉。
    const rect = (stage && stage.getBoundingClientRect) ? stage.getBoundingClientRect() : null;
    const boxW = rect ? rect.width : st.size;
    const boxH = rect ? rect.height : st.size;
    // st.size 是**球径**（不是窗口）。曾经用 min(size, boxW) 把窗口尺寸当成
    // 球径，232px 的画布塞进 122px 窗口，球被放大到窗外、只剩一片底色。
    st.S = clip
      ? Math.max(16, Math.min(st.size, boxW || st.size, boxH || st.size))
      : Math.max(16, st.size);
    dpr = Math.min(2, window.devicePixelRatio || 1);
    W = Math.round(st.S * (clip ? 1 : 1.9));
    const px = Math.round(W * dpr);
    for (const c of [glCanvas, fxCanvas]) {
      c.style.width = c.style.height = W + "px";
      if (c.width !== px) c.width = c.height = px;   // reassigning clears the canvas
    }
    if (gl) gl.viewport(0, 0, glCanvas.width, glCanvas.height);
    const S = st.S;
    // 字号按规格比例放大一档（数值 0.25S / 单位 0.086S / 称号 0.076S），并给
    // 小尺寸留「看得清」的硬下限：64px 球上 0.072S 只有 4.6px，中文与
    // "tok/s" 会糊成一团灰点。8px 是这颗常驻小件的最低可读线（贴边 40px
    // 球只显示数字，用 10px 下限）。数值取 0.25S 而不是更大：5 字符的
    // "1.23k" 恰好还能收在 36 段环内侧，再大就会压到刻度上。
    if (valueEl) valueEl.style.fontSize = Math.max(10, .25 * S) + "px";
    if (unitEl) unitEl.style.fontSize = Math.max(8, .086 * S) + "px";
    if (titleEl) titleEl.style.fontSize = Math.max(8, .076 * S) + "px";
    if (unitEl) unitEl.style.marginTop = Math.max(1, .012 * S) + "px";
    if (titleEl) titleEl.style.marginTop = Math.max(1, .018 * S) + "px";
    if (pillEl) {
      pillEl.style.fontSize = (.06 * S) + "px";
      pillEl.style.padding = `${.012 * S}px ${.04 * S}px`;
      pillEl.style.top = `${-.232 * S}px`;
    }
  }
  if (typeof ResizeObserver === "function" && stage) new ResizeObserver(resize).observe(stage);

  // ---------- particles ----------
  const parts = [];
  const MAX = 900;
  function spawn(pt) { if (parts.length < MAX) parts.push(pt); }
  function polar(a, r) { return [Math.cos(a) * r, -Math.sin(a) * r]; }   // screen y-down, angle in radians (y-down sense)
  function emitSpark(fromTip, fresh) {
    const S = st.S, h = st.h;
    const pos = fromTip ? h : Math.random() * h;
    const a = (135 + pos * 270) * Math.PI / 180;
    const [x, y] = [Math.cos(a) * .41 * S, Math.sin(a) * .41 * S];       // y-down
    const out = [Math.cos(a), Math.sin(a)], tan = [-Math.sin(a), Math.cos(a)];
    const sp = (.3 + Math.random() * .6) * S * (1 + st.o * .8) * (fresh ? 1.3 : 1);
    const tg = (Math.random() - .35) * .6 * S * (fresh ? 1.3 : 1);
    spawn({ x, y, vx: out[0] * sp + tan[0] * tg, vy: out[1] * sp + tan[1] * tg, life: .5 + Math.random() * .7, age: 0,
      size: 1.2 + Math.random() * 1.6, heat: Math.min(1, pos + .1), kind: "spark", drag: 2.2, streak: fresh });
  }
  function emitEmber(fresh) {
    const S = st.S;
    const a = (200 + Math.random() * 140) * Math.PI / 180;
    spawn({ x: Math.cos(a) * .47 * S, y: Math.sin(a) * .47 * S, vx: (Math.random() - .5) * .1 * S, vy: -(.12 + Math.random() * .2) * S,
      life: .9 + Math.random() * 1.1, age: 0, size: 1.2 + Math.random() * 1.8, heat: .85 + Math.random() * .15, kind: "ember",
      drag: .4, seed: Math.random() * 100, streak: false, fresh });
  }
  function burst() {
    const S = st.S, n = st.fresh ? 220 : 0;
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, sp = (.5 + Math.random() * 1.3) * S;
      spawn({ x: Math.cos(a) * .48 * S, y: Math.sin(a) * .48 * S, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, life: .7 + Math.random() * .9,
        age: 0, size: 1.4 + Math.random() * 2.2, heat: .9 + Math.random() * .1, kind: Math.random() < .3 ? "gold" : "spark", drag: 2.4, streak: true });
    }
  }

  // ---------- electric arcs (redline, new) ----------
  const arcs = [];
  function makeArc() {
    const S = st.S;
    const a0 = (135 + Math.random() * st.h * 270) * Math.PI / 180;
    const a1 = a0 + (Math.random() - .5) * .9;
    const r0 = .41 * S, r1 = (.5 + Math.random() * .1) * S;
    arcs.push({ a0, a1, r0, r1, age: 0, life: .09 + Math.random() * .1, seed: Math.random() * 1000 });
  }
  function drawArc(ctx, arc, color) {
    const steps = 9;
    const pts = [];
    for (let i = 0; i <= steps; i++) {
      const k = i / steps;
      const a = arc.a0 + (arc.a1 - arc.a0) * k, rr = arc.r0 + (arc.r1 - arc.r0) * k;
      const j = (i === 0 || i === steps) ? 0 : (Math.random() - .5) * st.S * .045;
      pts.push([Math.cos(a) * rr + j, Math.sin(a) * rr + j]);
    }
    const fade = 1 - arc.age / arc.life;
    ctx.lineJoin = "round";
    for (const [w, c, a] of [[st.S * .022, color, .18], [st.S * .008, color, .5], [st.S * .0032, [1, 1, 1], .95]]) {
      ctx.strokeStyle = rgba(c, a * fade); ctx.lineWidth = w;
      ctx.beginPath(); pts.forEach(([x, y], i) => i ? ctx.lineTo(x, y) : ctx.moveTo(x, y)); ctx.stroke();
    }
  }

  // ---------- number lightning: bolts crackle off digits as they change ----------
  const bolts = [], zaps = [], lastZap = [];
  let lastText = "";
  function jag(x0, y0, x1, y1, rough, depth) {
    let pts = [[x0, y0], [x1, y1]], disp = Math.hypot(x1 - x0, y1 - y0) * rough;
    for (let d = 0; d < depth; d++) {
      const next = [pts[0]];
      for (let i = 1; i < pts.length; i++) {
        const [ax, ay] = pts[i - 1], [bx, by] = pts[i], len = Math.hypot(bx - ax, by - ay) || 1, off = (Math.random() - .5) * disp;
        next.push([(ax + bx) / 2 - (by - ay) / len * off, (ay + by) / 2 + (bx - ax) / len * off], pts[i]);
      }
      pts = next; disp *= .55;
    }
    return pts;
  }
  function shapeBolt(b) {
    b.main = jag(b.x0, b.y0, b.x1, b.y1, .3, 5);
    b.forks = [];
    for (let i = 0; i < b.nf; i++) {
      const [sx, sy] = b.main[3 + Math.floor(Math.random() * (b.main.length - 8))];
      const a = Math.atan2(b.y1 - b.y0, b.x1 - b.x0) + (Math.random() - .5) * 1.9, l = b.len * (.2 + Math.random() * .3);
      b.forks.push(jag(sx, sy, sx + Math.cos(a) * l, sy + Math.sin(a) * l, .35, 4));
    }
  }
  function charBoxes() {
    const node = valueEl.firstChild, out = [];
    if (!node) return out;
    const c = fxCanvas.getBoundingClientRect(), cx = c.left + c.width / 2, cy = c.top + c.height / 2;
    const range = document.createRange();
    for (let i = 0; i < node.length; i++) {
      range.setStart(node, i); range.setEnd(node, i + 1);
      const r = range.getBoundingClientRect();
      out.push({ x: r.left + r.width / 2 - cx, y: r.top + r.height / 2 - cy, w: r.width, h: r.height });
    }
    return out;
  }
  function makeBolt(boxes, i, power) {
    const S = st.S, bx = boxes[i], gh = bx.h * .36, gw = bx.w * .42, side = Math.random() < .5 ? -1 : 1;
    const x0 = bx.x + (Math.random() - .5) * gw * 1.4, y0 = bx.y + side * gh;
    const nb = boxes[i + (Math.random() < .5 ? -1 : 1)], roll = Math.random();
    let x1, y1;
    if (st.tier >= 4 && roll < .3) {
      const a = Math.atan2(y0, x0 + (Math.random() - .5) * S * .3);
      x1 = Math.cos(a) * .39 * S; y1 = Math.sin(a) * .39 * S;
    } else if (nb && /[0-9]/.test(valueEl.textContent[boxes.indexOf(nb)]) && roll < .6) {
      x1 = nb.x + (Math.random() - .5) * gw; y1 = nb.y + side * gh * (.6 + Math.random() * .5);
    } else {
      const a = side * Math.PI / 2 + (Math.random() - .5) * 1.7, l = S * (.05 + .07 * power);
      x1 = x0 + Math.cos(a) * l; y1 = y0 + Math.sin(a) * l;
    }
    const b = { x0, y0, x1, y1, len: Math.hypot(x1 - x0, y1 - y0), nf: power > .9 ? 2 : 1, age: 0,
      life: .14 + .1 * Math.random() + .05 * power, w: S * .0032 * (.7 + .5 * power), regen: .045 };
    shapeBolt(b);
    bolts.push(b);
  }
  function zap(text, prev, force) {
    if (reduce || st.v < .5) return;
    const boxes = charBoxes(), h = st.h, o = st.o, same = text.length === prev.length;
    for (let i = 0; i < text.length; i++) {
      if (!/[0-9]/.test(text[i]) || (same && text[i] === prev[i]) || !boxes[i]) continue;
      const sig = text.slice(i + 1).replace(/[^0-9]/g, "").length;
      if (!force && (st.time - (lastZap[i] ?? -1) < .11 || Math.random() > Math.min(1, .3 + .3 * sig + .4 * h))) continue;
      lastZap[i] = st.time;
      const power = (.55 + .22 * sig) * (.6 + .6 * h + .6 * o) * st.level;
      zaps.push({ x: boxes[i].x, y: boxes[i].y, r: boxes[i].w * .9, age: 0, life: .16 + .06 * power, power });
      const n = 1 + (h > .55 ? 1 : 0) + (st.tier >= 4 && Math.random() < .6 ? 1 : 0);
      for (let k = 0; k < n && bolts.length < 16; k++) makeBolt(boxes, i, power);
    }
  }
  function drawBolt(path, w, glowC, coreC, a, dark) {
    for (const [lw, c, k] of [[w * 5, glowC, .16], [w * 2, glowC, .45], [w * .8, coreC, .95]]) {
      fx.strokeStyle = rgba(c, k * a * (dark ? 1 : .9)); fx.lineWidth = lw;
      fx.beginPath(); path.forEach(([x, y], i) => i ? fx.lineTo(x, y) : fx.moveTo(x, y)); fx.stroke();
    }
  }

  function tierOf(v, prev) {
    let t = 0;
    for (let i = 1; i < 5; i++) {
      const at = TIER_AT[i];
      if (v >= at || (prev >= i && v >= at * .9)) t = i;
    }
    return t;
  }

  function addWave(strength) {
    st.waves.push({ age: 0, strength, heat: st.h });
    if (st.waves.length > 4) st.waves.shift();
  }

  // ---------- simulation ----------
  function step(dt) {
    st.time += dt;
    // 目标值跟随：原型 4.5/s（约 220ms），QKA 采样 200-750ms 且实时速率
    // 本身带窗口抖动，放到 2.6/s（约 380ms）后数字不再每次采样都跳一下。
    st.v += (st.target - st.v) * (1 - Math.exp(-dt * 2.6));
    if (st.v < .3) st.v = 0;
    const h = Math.min(1, Math.pow(st.v / R, .6));
    st.h += (h - st.h) * (1 - Math.exp(-dt * 7));
    st.o = Math.min(1, Math.max(0, (st.v - R) / R));
    const newTier = tierOf(st.v, st.tier);
    if (newTier !== st.tier) { st.flash = 1; st.tier = newTier; }
    st.flash = Math.max(0, st.flash - dt / .4);

    const fresh = st.fresh, lvl = st.level, hh = st.h, o = st.o;
    if (hh > .005) st.comet += dt / (6 + (.7 - 6) * hh) * (fresh ? 1 + .6 * o : 1);
    st.flow += dt * (.35 + 1.9 * hh + 1.6 * o);
    st.phase += dt * (.12 + 1.4 * hh + 2.2 * o);
    st.phase2 += dt * (.05 + .5 * hh + 1.2 * o);
    st.warp += dt * (.2 + 1.1 * hh + 1.2 * o);
    st.corona += dt * (.5 + 1.7 * hh + 1.8 * o);
    st.peakAge += dt;

    // response pulses while active
    if (st.v > 2) {
      st.nextPulse -= dt;
      if (st.nextPulse <= 0) { addWave(.45 + Math.random() * .55); st.nextPulse = .45 + Math.random() * (1.4 - .9 * hh); }
    }
    for (const w of st.waves) w.age += dt / (fresh ? .85 : .7);
    st.waves = st.waves.filter(w => w.age < 1);

    // particles
    const sparkRate = hh >= .45 ? 120 * ((hh - .45) / .55) * lvl * (fresh ? 1.6 : 1) : 0;
    let n = sparkRate * dt;
    while (n > 0) { if (Math.random() < n) emitSpark(Math.random() < .6, fresh); n -= 1; }
    if (st.tier >= 4) {
      let m = 90 * Math.max(o, .3) * lvl * (fresh ? 1.4 : 1) * dt;
      while (m > 0) { if (Math.random() < m) emitEmber(fresh); m -= 1; }
    }
    const S = st.S;
    for (const p of parts) {
      p.age += dt;
      if (p.kind === "ember") {
        p.vx += Math.sin(st.time * 3 + p.seed) * .3 * S * dt;
        p.vy -= .12 * S * dt;
      }
      const k = Math.exp(-p.drag * dt);
      p.vx *= k; p.vy *= k;
      p.x += p.vx * dt; p.y += p.vy * dt;
    }
    for (let i = parts.length - 1; i >= 0; i--) if (parts[i].age >= parts[i].life) parts.splice(i, 1);

    // arcs
    if (fresh && st.tier >= 4) {
      st.arcT -= dt;
      if (st.arcT <= 0) { makeArc(); st.arcT = (.28 - .2 * o) / lvl * (.5 + Math.random()); }
    }
    for (const a of arcs) a.age += dt;
    for (let i = arcs.length - 1; i >= 0; i--) if (arcs[i].age >= arcs[i].life) arcs.splice(i, 1);

    for (const b of bolts) { b.age += dt; b.regen -= dt; if (b.regen <= 0) { shapeBolt(b); b.regen = .045; } }
    for (let i = bolts.length - 1; i >= 0; i--) if (bolts[i].age >= bolts[i].life) bolts.splice(i, 1);
    for (const z of zaps) z.age += dt;
    for (let i = zaps.length - 1; i >= 0; i--) if (zaps[i].age >= zaps[i].life) zaps.splice(i, 1);

    // readout motion
    st.jitterT -= dt;
    if (st.jitterT <= 0) {
      st.jitterT = 1 / 30;
      const j = st.tier >= 4 && !reduce ? (fresh ? .9 + 1.2 * o : .6) : 0;
      st.jitter = [(Math.random() - .5) * 2 * j, (Math.random() - .5) * 2 * j];
    }
    // 显示值再平滑一层：原型的 10/s 太快，这里 5/s（约 200ms）。
    st.shown += (st.v - st.shown) * (1 - Math.exp(-dt * 5));
  }

  function firePeak() {
    st.peakAge = 0;
    burst();
    if (opts.onPeak) opts.onPeak();
    if (pillEl) pillEl.animate([
      { transform: "translate(-50%,-50%) scale(.2)", opacity: 0 },
      { transform: "translate(-50%,-50%) scale(1.12)", opacity: 1, offset: .12 },
      { transform: "translate(-50%,-50%) scale(1)", opacity: 1, offset: .22 },
      { transform: "translate(-50%,-50%) scale(1)", opacity: 1, offset: .85 },
      { transform: "translate(-50%,-80%) scale(.9)", opacity: 0 }
    ], { duration: 1800, easing: "cubic-bezier(.2,.8,.2,1)" });
  }

  // ---------- rendering ----------
  function render() {
    const S = st.S, h = st.h, o = st.o, dark = st.dark, fresh = st.fresh;
    if (gl) {
      gl.clearColor(0, 0, 0, 0); gl.clear(gl.COLOR_BUFFER_BIT);
      gl.uniform2f(loc.uRes, glCanvas.width, glCanvas.height);
      gl.uniform1f(loc.uS, S * dpr);
      gl.uniform1f(loc.uTime, st.time);
      gl.uniform1f(loc.uH, h); gl.uniform1f(loc.uO, o);
      gl.uniform1f(loc.uDark, dark ? 1 : 0); gl.uniform1f(loc.uNew, fresh ? 1 : 0); gl.uniform1f(loc.uLevel, st.level);
      gl.uniform1f(loc.uFlash, st.flash); gl.uniform1f(loc.uPeak, st.peakAge);
      gl.uniform1f(loc.uSweep, st.peakAge < .5 ? ease(st.peakAge / .5) : 1);
      gl.uniform1f(loc.uTier, st.tier);
      gl.uniform1f(loc.uComet, st.comet); gl.uniform1f(loc.uFlow, st.flow); gl.uniform1f(loc.uPhase, st.phase); gl.uniform1f(loc.uPhase2, st.phase2);
      gl.uniform1f(loc.uWarp, st.warp); gl.uniform1f(loc.uCorona, st.corona);
      const w = new Float32Array(16).fill(-1);
      st.waves.slice(-4).forEach((wv, i) => { w[i * 4] = wv.age; w[i * 4 + 1] = wv.strength; w[i * 4 + 2] = wv.heat; w[i * 4 + 3] = 0; });
      gl.uniform4fv(loc.uWaves, w);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
    }

    // particles + arcs + rays
    fx.setTransform(1, 0, 0, 1, 0, 0);
    fx.clearRect(0, 0, fxCanvas.width, fxCanvas.height);
    fx.setTransform(dpr, 0, 0, dpr, W * dpr / 2, W * dpr / 2);
    fx.globalCompositeOperation = dark ? "lighter" : "source-over";
    if (st.peakAge < 1.3) {
      const k = st.peakAge / 1.3, rot = st.peakAge * .4;
      for (let i = 0; i < 24; i++) {
        const a = rot + i / 24 * Math.PI * 2, r0 = .5 * S, r1 = (.55 + .35 * ease(k * 1.4)) * S;
        const g = fx.createLinearGradient(Math.cos(a) * r0, Math.sin(a) * r0, Math.cos(a) * r1, Math.sin(a) * r1);
        g.addColorStop(0, rgba([1, .85, .35], .0));
        g.addColorStop(.3, rgba([1, .8, .3], .75 * (1 - k)));
        g.addColorStop(1, rgba([1, .7, .2], 0));
        fx.strokeStyle = g; fx.lineWidth = S * (fresh ? .014 : .008) * (1 - k * .5);
        fx.beginPath(); fx.moveTo(Math.cos(a) * r0, Math.sin(a) * r0); fx.lineTo(Math.cos(a) * r1, Math.sin(a) * r1); fx.stroke();
      }
    }
    for (const p of parts) {
      const life = 1 - p.age / p.life;
      let c;
      if (p.kind === "gold") c = [1, .82, .35];
      else if (dark) c = mix(heat(p.heat), HOT, Math.min(1, (1 - life) * .3 + .4));
      else c = p.heat > .92 ? AMBER : darken(heat(p.heat), .1);
      const a = Math.min(1, life * 1.4) * (p.kind === "ember" ? .8 : 1);
      if (p.streak) {
        const v = Math.hypot(p.vx, p.vy), fadeV = Math.min(1, v / (.12 * S));
        const len = Math.max(.02, Math.min(.06, .06 * fadeV)), aa = a * (.25 + .75 * fadeV);
        const tx = p.x - p.vx * len, ty = p.y - p.vy * len;
        const g = fx.createLinearGradient(tx, ty, p.x, p.y);
        g.addColorStop(0, rgba(c, 0)); g.addColorStop(1, rgba(c, aa));
        fx.strokeStyle = g; fx.lineCap = "round";
        fx.lineWidth = p.size * (dark ? .75 : 1); fx.beginPath(); fx.moveTo(tx, ty); fx.lineTo(p.x, p.y); fx.stroke();
        if (dark) {
          fx.fillStyle = rgba(mix(c, [1, 1, 1], .6), aa);
          fx.beginPath(); fx.arc(p.x, p.y, p.size * .55, 0, Math.PI * 2); fx.fill();
        }
      } else {
        const rr = p.size * (dark ? .8 : 1) * (p.kind === "ember" ? (0.55 + .45 * Math.sin(p.age * 18 + (p.seed || 0))) : 1);
        const g = fx.createRadialGradient(p.x, p.y, 0, p.x, p.y, rr * 2.2);
        g.addColorStop(0, rgba(mix(c, [1, 1, 1], dark ? .5 : 0), a)); g.addColorStop(.3, rgba(c, a * .55)); g.addColorStop(1, rgba(c, 0));
        fx.fillStyle = g; fx.beginPath(); fx.arc(p.x, p.y, rr * 2.2, 0, Math.PI * 2); fx.fill();
      }
    }
    const arcColor = dark ? mix(heat(h), [.75, .85, 1], .5) : darken(heat(h), .15);
    for (const arc of arcs) drawArc(fx, arc, arcColor);
    const bh = heat(Math.max(h, .3));
    const boltGlow = dark ? mix(bh, [.7, .82, 1], .45) : [.42, .5, 1], boltCore = dark ? [1, 1, 1] : [.2, .24, .85];
    for (const z of zaps) {
      const k = 1 - z.age / z.life, g = fx.createRadialGradient(z.x, z.y, 0, z.x, z.y, z.r * 1.6);
      g.addColorStop(0, rgba(dark ? [1, 1, 1] : boltGlow, k * k * Math.min(.8, .35 * z.power) * (dark ? 1 : .5)));
      g.addColorStop(.4, rgba(boltGlow, k * k * .3 * Math.min(1, z.power)));
      g.addColorStop(1, rgba(boltGlow, 0));
      fx.fillStyle = g; fx.beginPath(); fx.arc(z.x, z.y, z.r * 1.6, 0, Math.PI * 2); fx.fill();
    }
    fx.lineJoin = "round"; fx.lineCap = "round";
    for (const b of bolts) {
      const a = Math.min(1, (1 - b.age / b.life) * 1.6) * (Math.random() < .22 ? .4 : 1);
      drawBolt(b.main, b.w, boltGlow, boltCore, a, dark);
      for (const f of b.forks) drawBolt(f, b.w * .6, boltGlow, boltCore, a * .8, dark);
    }

    // ---------- readout ----------
    // 原型直接写 DOM；这里改为回调：QKA 用连滚数字渲染，预览页用纯文本。
    const v = st.shown, tierNow = st.tier;
    const text = fmt(v);
    if (text !== lastText) {
      if (fresh && valueEl) zap(text, lastText, st.forceZap);
      if (opts.onText) opts.onText(text, lastText);
      else if (valueEl) valueEl.textContent = text;
      lastText = text;
      st.forceZap = false;
    }
    let title = "待機";
    if (st.v >= .5) for (const [at, name] of TITLES) if (st.v >= at) title = name;
    if (titleEl && !opts.hostTitle) titleEl.textContent = title;
    const hc = heat(h);
    const label = dark ? hc : darken(hc, .18);
    // 读数配色：静息（待机）不是「灰到看不见」——球底是近黑的深蓝，白字
    // 掉到 60% 就只有 3:1 左右，浅色桌面上整颗球看起来是空的。待机也保持
   // 94% 白，并用一圈很淡的白光把数字从球体渐变里托出来。
    const idleInk = dark ? "rgba(255,255,255,.94)" : "rgba(11,13,18,.68)";
    const ink = dark ? "rgba(255,255,255,.98)" : "rgba(11,13,18,.92)";
    if (valueEl) {
      valueEl.style.color = st.v < .5 ? idleInk : ink;
      const shadows = [];
      if (st.v < .5) shadows.push(`0 0 ${.05 * S}px rgba(255,255,255,.4)`);
      if (tierNow >= 2) {
        const blur = .035 * S * (fresh ? 1 + .8 * o : 1);
        shadows.push(`0 0 ${blur}px ${rgba(hc, dark ? .85 : .45)}`);
        if (fresh && tierNow >= 3) shadows.push(`0 0 ${blur * 2.4}px ${rgba(hc, dark ? .45 : .2)}`);
      }
      if (fresh && tierNow >= 4 && dark) {
        const off = (.6 + 1.6 * o) * S / 300;
        shadows.push(`${-off}px 0 0 rgba(255,60,140,.55)`, `${off}px 0 0 rgba(60,210,255,.55)`);
      }
      if (fresh && dark && st.v >= .5) shadows.push(`0 0 ${.025 * S}px rgba(0,0,0,.7)`, `0 ${.006 * S}px ${.012 * S}px rgba(0,0,0,.6)`);
      valueEl.style.textShadow = shadows.join(",") || "none";
      const skew = tierNow >= 3 ? -6 * h * (fresh ? 1.25 : 1) : 0;
      valueEl.style.transform = `translate(${st.jitter[0]}px, ${st.jitter[1]}px) skewX(${skew}deg)`;
    }
    if (unitEl) unitEl.style.color = dark ? "rgba(255,255,255,.82)" : "rgba(0,0,0,.62)";
    if (titleEl && !opts.hostTitle) {
      titleEl.style.color = st.v < .5
        ? (dark ? "rgba(255,255,255,.72)" : "rgba(0,0,0,.55)") : rgba(label);
    }
    if (readout) readout.style.transform = `translate(-50%, ${opts.readoutOffset || -43}%)`;
    if (opts.onFrame) opts.onFrame({ value: st.v, heat: h, overdrive: o, tier: tierNow, text });
  }

  // ---------- 对外 API ----------
  // 正常模式用 rAF；低配模式（performanceLow）改用 20fps 的 setTimeout，
  // 把每秒 60 次唤醒降到 20 次，静止/低性能窗口都更省 CPU。
  const LOW_FRAME_MS = 1000 / PERFORMANCE_LOW_FPS;
  let lastFrame = 0, rafId = 0, started = false, timerMode = false;

  function nowMs() {
    return typeof performance !== "undefined" && performance.now
      ? performance.now()
      : Date.now();
  }

  function scheduleNext() {
    if (performanceLow()) {
      timerMode = true;
      rafId = setTimeout(function () { rafId = 0; frame(nowMs()); }, LOW_FRAME_MS);
    } else {
      timerMode = false;
      rafId = requestAnimationFrame(frame);
    }
  }

  function frame(now) {
    if (!started) return;
    const dt = Math.min(.05, Math.max(0, (now - lastFrame) / 1000));
    lastFrame = now;
    step(dt);
    render();
    // 静止自停：必须同时复位 started，否则之后 setState/pulse/peak 调
    // start() 会被挡在门外，速度数字不再更新。
    if (orbSettled(st)) {
      started = false;
      rafId = 0;
      timerMode = false;
      return;
    }
    scheduleNext();
  }

  function start() {
    if (started) return;
    started = true;
    lastFrame = nowMs();
    scheduleNext();
  }
  function stop() {
    started = false;
    if (rafId) {
      if (timerMode) clearTimeout(rafId);
      else cancelAnimationFrame(rafId);
    }
    rafId = 0;
    timerMode = false;
  }

  // ---------- 可见性 ----------
  // 隐藏窗口不跑帧；回到前台时只有未静止才重启（静止画面已经画好）。
  const doc = typeof document !== "undefined" ? document : null;
  function onVisibilityChange() {
    if (!doc) return;
    if (doc.hidden) stop();
    else if (!orbSettled(st)) start();
  }
  if (doc && typeof doc.addEventListener === "function") {
    doc.addEventListener("visibilitychange", onVisibilityChange);
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
      if (orbSettled(st)) stop();
    },
    destroy() {
      stop();
      if (doc && typeof doc.removeEventListener === "function") {
        doc.removeEventListener("visibilitychange", onVisibilityChange);
      }
    },
    get state() { return st; },
  };
}
