// 性能模式（低配模式）：在软件渲染、虚拟机等低性能环境自动降级毛玻璃、
// 大面积阴影和 3D 宠物的渲染开销，同时允许在设置里强制开启或关闭。
//
// 本模块只做检测与状态判定，供 main.html / panel.html / settings.html 三个
// 页面共用；纯函数导出给 node --test 使用，不依赖 DOM 之外的环境。

export const PERFORMANCE_MODE_KEY = "performance_mode";
export const PERFORMANCE_MODES = ["auto", "on", "off"];
export const PERFORMANCE_LOW_CLASS = "perf-low";

// 软件渲染器的名字特征。WebView2（Chromium）在无 GPU 时通常报
// "Google SwiftShader"、"ANGLE (Microsoft Basic Render Driver Direct3D11 ...)"
// 一类字符串；Linux 上常见 llvmpipe / softpipe。
const SOFTWARE_RENDERER_HINTS = [
  "swiftshader",
  "swangle",
  "llvmpipe",
  "softpipe",
  "software",
  "microsoft basic render",
  "basic render driver",
  "microsoft basic display",
  "mesa offscreen",
];

// 虚拟机的虚拟 GPU 特征。虚拟机里即使有「显卡」，也常常只是半虚拟化或
// 软件模拟，模糊与 3D 依然很贵，因此自动模式一并降级。
const VIRTUAL_GPU_HINTS = [
  "vmware svga",
  "virtualbox",
  "hyper-v",
  "parallels",
  "qemu",
  "virtio",
  "vmwgfx",
];

// 低配模式的 3D 目标帧率：软件渲染下 60fps 的逐帧重绘会占满 CPU，
// 20fps 仍能保留宠物跟随与待机动作的观感。
export const PERFORMANCE_LOW_FPS = 20;

// 当前页面的低配状态。pokemon-3d.js 每帧读取 performanceLow() 决定是否限帧。
let currentLow = false;

// 渲染器检测结果在本页面内缓存：创建 WebGL 上下文有开销，没必要每帧做。
let cachedDetection = null;
let cachedResolved = null;

function defaultCanvas() {
  if (typeof document === "undefined" || !document.createElement) return null;
  return document.createElement("canvas");
}

// detectRenderer 创建一个临时 canvas 取 WebGL 上下文，读出显卡/渲染器名字。
// createCanvas 可注入，便于 node 单测不依赖真实浏览器。
export function detectRenderer(createCanvas) {
  const create = typeof createCanvas === "function" ? createCanvas : defaultCanvas;
  let canvas = null;
  try {
    canvas = create();
  } catch (_) {
    canvas = null;
  }
  if (!canvas || typeof canvas.getContext !== "function") {
    return { available: false, renderer: "", vendor: "", software: true };
  }
  let gl = null;
  try {
    const attributes = {
      alpha: true,
      antialias: false,
      depth: false,
      failIfMajorPerformanceCaveat: false,
      powerPreference: "low-power",
    };
    gl = canvas.getContext("webgl", attributes) ||
      canvas.getContext("experimental-webgl", attributes);
  } catch (_) {
    gl = null;
  }
  if (!gl) return { available: false, renderer: "", vendor: "", software: true };

  let renderer = "";
  let vendor = "";
  try {
    const debugInfo = gl.getExtension("WEBGL_debug_renderer_info");
    if (debugInfo) {
      renderer = String(gl.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL) || "");
      vendor = String(gl.getParameter(debugInfo.UNMASKED_VENDOR_WEBGL) || "");
    } else {
      renderer = String(gl.getParameter(gl.RENDERER) || "");
      vendor = String(gl.getParameter(gl.VENDOR) || "");
    }
  } catch (_) {
    renderer = "";
    vendor = "";
  }
  // 检测用的上下文立刻释放，避免多占一个 WebGL 上下文。
  try {
    const loseContext = gl.getExtension("WEBGL_lose_context");
    if (loseContext) loseContext.loseContext();
  } catch (_) {}

  // 名字匹配之外再问浏览器一次「这块 GPU 是否显著降级」：Chromium/WebView2
  // 在软件渲染（SwiftShader/WARP）时会让 failIfMajorPerformanceCaveat 的
  // 上下文创建失败，可兜住渲染器名被隐藏成 "WebKit WebGL" 的情况。
  let majorCaveat = false;
  try {
    const probe = create();
    if (probe && typeof probe.getContext === "function") {
      majorCaveat = !probe.getContext("webgl", { failIfMajorPerformanceCaveat: true });
    }
  } catch (_) {
    majorCaveat = false;
  }

  return {
    available: true,
    renderer,
    vendor,
    software: majorCaveat ||
      isLowPerformanceRenderer(renderer) ||
      isLowPerformanceRenderer(vendor),
  };
}

function containsHint(value, hints) {
  return hints.some(function (hint) { return value.indexOf(hint) >= 0; });
}

// isLowPerformanceRenderer 判断渲染器名字是否属于「低性能/软件渲染」。
export function isLowPerformanceRenderer(name) {
  const value = String(name || "").toLowerCase();
  if (!value) return false;
  return containsHint(value, SOFTWARE_RENDERER_HINTS) ||
    containsHint(value, VIRTUAL_GPU_HINTS);
}

// normalizePerformanceMode 把未知/缺失的设置值收敛成 auto。
export function normalizePerformanceMode(value) {
  return PERFORMANCE_MODES.indexOf(String(value || "")) >= 0 ? String(value) : "auto";
}

// resolvePerformanceMode 把设置值与渲染器检测结果合并成最终是否降级。
// 返回 { setting, low, renderer, vendor, available, reason }：
//   reason 取值 forced-on / forced-off / no-webgl / software-renderer / hardware，
//   便于日志与测试断言，也说明自动模式的判定依据。
export function resolvePerformanceMode(setting, detection) {
  const mode = normalizePerformanceMode(setting);
  const info = detection || {};
  const renderer = String(info.renderer || "");
  const vendor = String(info.vendor || "");
  const available = info.available !== false;
  if (mode === "on") {
    return { setting: mode, low: true, renderer, vendor, available, reason: "forced-on" };
  }
  if (mode === "off") {
    return { setting: mode, low: false, renderer, vendor, available, reason: "forced-off" };
  }
  if (!available) {
    return { setting: mode, low: true, renderer, vendor, available, reason: "no-webgl" };
  }
  // 自动模式只对**真正的软件渲染**降级（SwiftShader / llvmpipe / WARP 基本
  // 渲染驱动，或 failIfMajorPerformanceCaveat 失败的兜底标记）。虚拟机里的
  // 半虚拟化显卡（qemu/virtio/vmware svga）虽然性能一般，但毛玻璃与 3D 通常
  // 还能跑，不该默默把特效全关掉——用户会看到 Windows 上「没有 mac 的玻璃
  // 效果」。这类环境由用户在设置里手动选「低配优先」。
  if (info.software === true ||
      containsHint(renderer.toLowerCase(), SOFTWARE_RENDERER_HINTS) ||
      containsHint(vendor.toLowerCase(), SOFTWARE_RENDERER_HINTS)) {
    return { setting: mode, low: true, renderer, vendor, available, reason: "software-renderer" };
  }
  return { setting: mode, low: false, renderer, vendor, available, reason: "hardware" };
}

// performanceLow 返回本页当前的降级状态，供 3D 渲染限帧/降分辨率。
export function performanceLow() {
  return currentLow;
}

// applyPerformanceMode 把降级状态写到 body.perf-low 与 html[data-perf]，
// 并广播 qka-perf-changed 让已创建的 3D 视图重设分辨率/帧率。
// 对传入的 document 与自定义事件都做了防御，便于 node 单测注入假 document。
export function applyPerformanceMode(doc, low) {
  currentLow = !!low;
  const target = doc || (typeof document !== "undefined" ? document : null);
  if (!target || !target.documentElement) return currentLow;
  const root = target.documentElement;
  if (root.dataset) root.dataset.perf = currentLow ? "low" : "high";
  const body = target.body;
  if (body && body.classList) body.classList.toggle(PERFORMANCE_LOW_CLASS, currentLow);
  try {
    if (typeof target.dispatchEvent === "function" && typeof CustomEvent === "function") {
      target.dispatchEvent(new CustomEvent("qka-perf-changed", {
        detail: { low: currentLow },
      }));
    }
  } catch (_) {}
  return currentLow;
}

// initPerformanceMode 读取设置值、检测一次渲染器并应用降级状态。
// 返回解析结果（含 detection），调用方可在需要时展示检测到的渲染器。
export function initPerformanceMode(options) {
  const opts = options || {};
  const doc = opts.document || (typeof document !== "undefined" ? document : null);
  if (!cachedDetection) {
    cachedDetection = detectRenderer(opts.createCanvas);
  }
  cachedResolved = resolvePerformanceMode(opts.setting, cachedDetection);
  applyPerformanceMode(doc, cachedResolved.low);
  return Object.assign({}, cachedResolved, { detection: cachedDetection });
}

// resetPerformanceModeCache 清空页面级缓存，仅供单测使用。
export function resetPerformanceModeCache() {
  cachedDetection = null;
  cachedResolved = null;
  currentLow = false;
}

// lastResolvedPerformanceMode 返回最近一次 init/resolve 的结果，未初始化时为 null。
export function lastResolvedPerformanceMode() {
  return cachedResolved;
}
