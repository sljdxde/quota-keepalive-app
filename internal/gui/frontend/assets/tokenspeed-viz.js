// 速度可视化的共享常量与纯函数，参数取自 agent-speed 的 spec/DESIGN.md §1
// 与 spec/METRICS.md §7：热谱色板、红线缩放的比例阶梯、称号迟滞、数字格式。
// 悬浮球（bar.html）、速度页（main.html）与菜单栏面板（panel.js）共用，
// 避免三处各写一套导致「同一个速度看起来不一样」。

// HEAT_STOPS 是热谱色板：h=0 的板岩灰（怠速）到 h=1 的金色（红线）。
// 插值在 sRGB 空间做（比 OKLab 简单，52px 的球面上看不出差别）。
export const HEAT_STOPS = [
  { at: 0.0, hex: "#7C8798" }, // slate，怠速
  { at: 0.1, hex: "#2DD4BF" }, // teal
  { at: 0.3, hex: "#38BDF8" }, // sky
  { at: 0.48, hex: "#818CF8" }, // indigo
  { at: 0.64, hex: "#E879F9" }, // fuchsia
  { at: 0.78, hex: "#FB7185" }, // rose
  { at: 0.9, hex: "#FB923C" }, // orange
  { at: 1.0, hex: "#FACC15" }, // gold
];

// WHITE_HOT 是高光的白热点：越过红线后高光朝它混合。
export const WHITE_HOT = "#FFF7D6";

// SPEED_TITLE_RATIOS 是 13 档称号相对红线的比例；红线 400 时即 QKA 原有的
// 0.5/8/12/…/400 阶梯，因此改红线只是整体缩放，档位含义不变。
// DEFAULT_REDLINE 是红线默认值（tok/s）。与 Go 侧 tokenspeed.DefaultRedline
// 保持一致：500 让 10–60 tok/s 的日常会话也能看见仪表点亮。
export const DEFAULT_REDLINE = 500;

export const SPEED_TITLE_RATIOS = [
  0, 0.02, 0.03, 0.04, 0.06, 0.08, 0.12, 0.17, 0.24, 0.34, 0.5, 0.7, 1.0,
];

// EFFECT_TIER_RATIOS 是五档特效（怠速/巡航/加速/狂飙/极限）的进入阈值。
export const EFFECT_TIER_RATIOS = [0.15, 0.375, 0.75];

export function clamp01(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return 0;
  return Math.max(0, Math.min(1, number));
}

function hexToRgb(hex) {
  return {
    r: parseInt(hex.slice(1, 3), 16),
    g: parseInt(hex.slice(3, 5), 16),
    b: parseInt(hex.slice(5, 7), 16),
  };
}

const HEAT_RGB = HEAT_STOPS.map((stop) => ({ at: stop.at, rgb: hexToRgb(stop.hex) }));

// heatRgb 返回热谱在 h 处的 RGB 分量，供 canvas 拼 rgba()。
export function heatRgb(h) {
  const value = clamp01(h);
  let lower = HEAT_RGB[0];
  let upper = HEAT_RGB[HEAT_RGB.length - 1];
  for (let i = 0; i < HEAT_RGB.length - 1; i += 1) {
    if (value >= HEAT_RGB[i].at && value <= HEAT_RGB[i + 1].at) {
      lower = HEAT_RGB[i];
      upper = HEAT_RGB[i + 1];
      break;
    }
  }
  const span = upper.at - lower.at;
  const t = span <= 0 ? 0 : (value - lower.at) / span;
  return {
    r: Math.round(lower.rgb.r + (upper.rgb.r - lower.rgb.r) * t),
    g: Math.round(lower.rgb.g + (upper.rgb.g - lower.rgb.g) * t),
    b: Math.round(lower.rgb.b + (upper.rgb.b - lower.rgb.b) * t),
  };
}

// heatColor 返回热谱在 h 处的 CSS 颜色。
export function heatColor(h, alpha = 1) {
  const rgb = heatRgb(h);
  if (alpha >= 1) {
    return "rgb(" + rgb.r + " " + rgb.g + " " + rgb.b + ")";
  }
  return "rgba(" + rgb.r + "," + rgb.g + "," + rgb.b + "," + clamp01(alpha) + ")";
}

// mixWithWhiteHot 把热谱颜色朝白热点混合，用于「当前段」与红线高光。
export function mixWithWhiteHot(h, amount) {
  const rgb = heatRgb(h);
  const target = hexToRgb(WHITE_HOT);
  const t = clamp01(amount);
  return (
    "rgb(" +
    Math.round(rgb.r + (target.r - rgb.r) * t) + " " +
    Math.round(rgb.g + (target.g - rgb.g) * t) + " " +
    Math.round(rgb.b + (target.b - rgb.b) * t) + ")"
  );
}

// heatForSpeed 返回速度在红线下的热度 h = min(1,(v/R)^0.6)。
export function heatForSpeed(value, redline) {
  const v = Math.max(0, Number(value) || 0);
  const r = Number(redline) > 0 ? Number(redline) : DEFAULT_REDLINE;
  if (v <= 0) return 0;
  return Math.min(1, Math.pow(v / r, 0.6));
}

// titleThresholdsFor 返回按红线缩放的 13 档称号阈值（与 Go 侧同源）。
export function titleThresholdsFor(redline) {
  const r = Number(redline) > 0 ? Number(redline) : DEFAULT_REDLINE;
  return SPEED_TITLE_RATIOS.map((ratio, index) =>
    index === 0 ? 0.5 : Math.round(ratio * r * 100) / 100);
}

// formatSpeed 是 agent-speed 的数字格式（METRICS §7）：<10 一位小数、
// <1000 整数、<10000 两位小数 k、<1e6 一位小数 k / 整数 k、M、B。
export function formatSpeed(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) return "0";
  if (number < 10) {
    const rounded = Math.round(number * 10) / 10;
    return rounded >= 10 ? "10" : String(rounded);
  }
  if (number < 1000) return String(Math.round(number));
  if (number < 10000) return (number / 1000).toFixed(2) + "k";
  if (number < 1000000) {
    const scaled = number / 1000;
    return (scaled >= 100 ? String(Math.round(scaled)) : scaled.toFixed(1)) + "k";
  }
  if (number < 1000000000) {
    const scaled = number / 1000000;
    return (scaled >= 100 ? String(Math.round(scaled)) : scaled.toFixed(1)) + "M";
  }
  const scaled = number / 1000000000;
  return (scaled >= 100 ? String(Math.round(scaled)) : scaled.toFixed(1)) + "B";
}

// ballReadoutFontSize 返回悬浮球数值的字号（px）。基准 0.25×球径（64px 球
// 上是 16px），但 36 段环内侧只有约 0.60×球径 的横向余量：比例字体按
// 0.58em/字符 估算，超出就整体收字号，长数字（1.23k / 100.00k）不会顶出
// 刻度环。下限 9px 保证贴边态仍能读。
export function ballReadoutFontSize(ballSize, text) {
  const size = Math.max(16, Number(ballSize) || 64);
  const base = Math.max(10, 0.25 * size);
  const chars = Math.max(1, String(text == null ? "" : text).length);
  const maxWidth = 0.60 * size;
  const estimated = chars * 0.58 * base;
  const fitted = estimated <= maxWidth ? base : maxWidth / (chars * 0.58);
  return Math.round(Math.max(9, fitted) * 10) / 10;
}

// ballReadoutUnitSize 返回 tok/s 单位行的字号（px）：0.086×球径，最低 9px
// （64px 球只有 5.5px，8px 在 Windows 上会糊成一团灰点）。
export function ballReadoutUnitSize(ballSize) {
  const size = Math.max(16, Number(ballSize) || 64);
  return Math.round(Math.max(9, 0.086 * size) * 10) / 10;
}

// createLevelTracker 实现称号/档位的迟滞：升档在当前阈值即进入，降档要跌破
// 该档阈值的 90%，且候选档必须稳定 0.8s 才生效（DESIGN §1）。返回的 level
// 是 0（怠速）到 13。
export function createLevelTracker(thresholds, holdMs = 800) {
  const ladder = Array.isArray(thresholds) && thresholds.length ? thresholds : titleThresholdsFor(DEFAULT_REDLINE);
  let level = 0;
  let candidate = 0;
  let candidateSince = 0;

  function targetFor(value, active) {
    const v = Math.max(0, Number(value) || 0);
    if (!active || v < 0.5) return 0;
    let target = 1;
    for (let i = 0; i < ladder.length; i += 1) {
      if (v >= ladder[i]) target = i + 1;
    }
    return target;
  }

  return {
    // update 返回当前稳定档位；nowMs 用 performance.now()。
    update(value, active, nowMs) {
      const next = targetFor(value, active);
      if (next === level) {
        candidate = level;
        return level;
      }
      // 过迟滞：升档只要达到目标；降档要跌到当前档阈值的 90% 以下。
      if (next < level) {
        const floor = ladder[level - 1] * 0.9;
        if ((Number(value) || 0) >= floor) {
          candidate = level;
          return level;
        }
      }
      if (next !== candidate) {
        candidate = next;
        candidateSince = nowMs;
        return level;
      }
      if (nowMs - candidateSince >= holdMs) {
        level = next;
      }
      return level;
    },
    get level() {
      return level;
    },
    reset() {
      level = 0;
      candidate = 0;
      candidateSince = 0;
    },
  };
}

// effectTierFor 返回五档特效等级：0 怠速 / 1 巡航 / 2 加速 / 3 狂飙 / 4 极限。
export function effectTierFor(value, redline) {
  const v = Math.max(0, Number(value) || 0);
  const r = Number(redline) > 0 ? Number(redline) : DEFAULT_REDLINE;
  if (v < 0.5) return 0;
  if (v >= EFFECT_TIER_RATIOS[2] * r) return 4;
  if (v >= EFFECT_TIER_RATIOS[1] * r) return 3;
  if (v >= EFFECT_TIER_RATIOS[0] * r) return 2;
  return 1;
}

// drawLedStrip 在速度页画 36 段横向 LED 条：段色取该段位置的热谱，亮起
// 段数 = 热度 × 36，当前段白热；今日峰值处画一条细金线。
export function drawLedStrip(canvas, options) {
  if (!canvas) return;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  const dpr = Math.max(1, Math.min(3, window.devicePixelRatio || 1));
  const width = Math.max(1, canvas.clientWidth || 360);
  const height = Math.max(1, canvas.clientHeight || 14);
  if (canvas.width !== Math.round(width * dpr) || canvas.height !== Math.round(height * dpr)) {
    canvas.width = Math.round(width * dpr);
    canvas.height = Math.round(height * dpr);
  }
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, width, height);

  const segments = 36;
  const gap = 2;
  const segmentWidth = (width - gap * (segments - 1)) / segments;
  const radius = Math.min(2, segmentWidth / 2);
  const value = Math.max(0, Number(options && options.value) || 0);
  const redline = Number(options && options.redline) > 0 ? Number(options.redline) : DEFAULT_REDLINE;
  const heat = heatForSpeed(value, redline);
  const lit = value < 0.5 ? 0 : heat * segments;

  for (let i = 0; i < segments; i += 1) {
    const x = i * (segmentWidth + gap);
    const position = i / (segments - 1);
    const on = i < Math.ceil(lit);
    ctx.globalAlpha = on ? (i === Math.ceil(lit) - 1 ? 1 : 0.82) : 0.14;
    ctx.fillStyle = on
      ? (i === Math.ceil(lit) - 1 ? mixWithWhiteHot(position, 0.7) : heatColor(position))
      : heatColor(position);
    ctx.beginPath();
    ctx.roundRect(x, 2, segmentWidth, height - 4, radius);
    ctx.fill();
  }
  ctx.globalAlpha = 1;

  const peak = Number(options && options.peak);
  if (Number.isFinite(peak) && peak > 0) {
    const peakHeat = heatForSpeed(peak, redline);
    const x = Math.min(width - 1, Math.max(0, peakHeat * width));
    ctx.fillStyle = "rgba(255,208,107,.95)";
    ctx.fillRect(x - 1, 0, 2, height);
  }
}

// drawSparkline 画最近 N 个采样的实时速率曲线，并标出今日峰值虚线。
export function drawSparkline(canvas, series, options) {
  if (!canvas) return;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  const dpr = Math.max(1, Math.min(3, window.devicePixelRatio || 1));
  const width = Math.max(1, canvas.clientWidth || 360);
  const height = Math.max(1, canvas.clientHeight || 56);
  if (canvas.width !== Math.round(width * dpr) || canvas.height !== Math.round(height * dpr)) {
    canvas.width = Math.round(width * dpr);
    canvas.height = Math.round(height * dpr);
  }
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, width, height);

  const values = Array.isArray(series) ? series : [];
  const redline = Number(options && options.redline) > 0 ? Number(options.redline) : DEFAULT_REDLINE;
  const peak = Number(options && options.peak);
  let maxValue = Math.max(redline * 0.05, Number(peak) || 0);
  for (const value of values) maxValue = Math.max(maxValue, Number(value) || 0);
  maxValue *= 1.12;

  if (values.length >= 2) {
    const step = width / Math.max(1, values.length - 1);
    // 面积：热谱色从顶部 26% 渐隐到 0。
    const gradient = ctx.createLinearGradient(0, 0, 0, height);
    gradient.addColorStop(0, heatColor(heatForSpeed(values[values.length - 1], redline), 0.28));
    gradient.addColorStop(1, heatColor(heatForSpeed(values[values.length - 1], redline), 0));
    ctx.beginPath();
    values.forEach((value, index) => {
      const x = index * step;
      const y = height - 3 - (Math.max(0, Number(value) || 0) / maxValue) * (height - 8);
      if (index === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    const lastX = (values.length - 1) * step;
    ctx.lineTo(lastX, height);
    ctx.lineTo(0, height);
    ctx.closePath();
    ctx.fillStyle = gradient;
    ctx.fill();

    // 折线：按当前热度上色。
    ctx.beginPath();
    values.forEach((value, index) => {
      const x = index * step;
      const y = height - 3 - (Math.max(0, Number(value) || 0) / maxValue) * (height - 8);
      if (index === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    ctx.strokeStyle = heatColor(heatForSpeed(values[values.length - 1], redline), 0.95);
    ctx.lineWidth = 1.5;
    ctx.lineJoin = "round";
    ctx.stroke();
  }

  if (Number.isFinite(peak) && peak > 0) {
    const y = height - 3 - (peak / maxValue) * (height - 8);
    ctx.setLineDash([4, 4]);
    ctx.strokeStyle = "rgba(255,208,107,.5)";
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(width, y);
    ctx.stroke();
    ctx.setLineDash([]);
  }
}

/**
 * createSpeedThrottle 让速度数字最多每 minIntervalMs 变一次。
 *
 * 为什么需要：采样可以调到 200ms，实时速率本身也在窗口里连续变化。若每次都
 * 重画，数字每 200ms 抖一下、连滚动画永远重启，人眼来不及读。这里强制
 * 两次提交之间至少隔 minIntervalMs（默认 500ms），期间只记最新值，到点再
 * 提交，保证「看得清每一次变化」。
 *
 * 用法：
 *   const throttle = createSpeedThrottle(500, (text) => render(text));
 *   throttle.push("1.2k");
 *
 * @param {number} minIntervalMs 两次提交的最小间隔
 * @param {(text: string) => void} commit 真正重画的回调
 */
export function createSpeedThrottle(minIntervalMs, commit) {
  const interval = Number(minIntervalMs) > 0 ? Number(minIntervalMs) : 500;
  let lastAt = 0;
  let pending = null;
  let timer = 0;

  function flush() {
    timer = 0;
    if (pending === null) return;
    lastAt = performance.now();
    const text = pending;
    pending = null;
    commit(text);
  }

  return {
    push(text) {
      const value = String(text == null ? "0" : text);
      if (!lastAt) {
        // 首帧直接落位，避免启动时先等半秒。
        lastAt = performance.now();
        commit(value);
        return;
      }
      pending = value;
      const wait = interval - (performance.now() - lastAt);
      if (wait <= 0) {
        flush();
        return;
      }
      if (!timer) timer = setTimeout(flush, wait);
    },
    /** 立即提交挂起值（窗口隐藏/切页前调用，避免丢失最后一次更新）。 */
    flush() {
      if (timer) {
        clearTimeout(timer);
        timer = 0;
      }
      flush();
    },
    /** 数据源停顿后重置节流窗口（下一次 push 立即生效）。 */
    reset() {
      if (timer) clearTimeout(timer);
      timer = 0;
      pending = null;
      lastAt = 0;
    },
  };
}
