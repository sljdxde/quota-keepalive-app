import assert from "node:assert/strict";
import test from "node:test";

import {
  applyPerformanceMode,
  detectRenderer,
  initPerformanceMode,
  isLowPerformanceRenderer,
  lastResolvedPerformanceMode,
  normalizePerformanceMode,
  performanceLow,
  resetPerformanceModeCache,
  resolvePerformanceMode,
  PERFORMANCE_LOW_FPS,
  PERFORMANCE_LOW_CLASS,
  PERFORMANCE_MODE_KEY,
  PERFORMANCE_MODES,
} from "../frontend/assets/perf-mode.js";

// fakeGL 模拟 WebGL 上下文：可指定 renderer/vendor 与是否提供调试扩展。
function fakeGL(options) {
  const opts = options || {};
  const state = { lost: 0, rendererQueries: 0 };
  const debug = opts.debug === false ? null : {
    UNMASKED_RENDERER_WEBGL: 0x9246,
    UNMASKED_VENDOR_WEBGL: 0x9245,
  };
  const gl = {
    RENDERER: 0x1f01,
    VENDOR: 0x1f00,
    getExtension(name) {
      if (name === "WEBGL_debug_renderer_info") return debug;
      if (name === "WEBGL_lose_context") {
        return { loseContext() { state.lost += 1; } };
      }
      return null;
    },
    getParameter(parameter) {
      state.rendererQueries += 1;
      const renderer = opts.renderer === undefined ? "ANGLE (NVIDIA GeForce RTX 3060)" : opts.renderer;
      const vendor = opts.vendor === undefined ? "Google Inc. (NVIDIA)" : opts.vendor;
      switch (parameter) {
        case 0x9246:
        case 0x1f01:
          return renderer;
        case 0x9245:
        case 0x1f00:
          return vendor;
        default:
          return "";
      }
    },
  };
  return { gl, state };
}

// fakeCanvas 返回一个「每次调用都产出新 canvas」的工厂，模拟检测与
// failIfMajorPerformanceCaveat 探针各创建一次上下文。
function fakeCanvas(options) {
  const opts = options || {};
  const { gl, state } = fakeGL(opts);
  const result = { factory: null, gl, state, created: 0 };
  result.factory = function () {
    result.created += 1;
    return {
      getContext(type, attributes) {
        if (opts.throwOnContext) throw new Error("context unavailable");
        if (opts.caveat && attributes && attributes.failIfMajorPerformanceCaveat) {
          return null;
        }
        return type === "webgl" ? gl : null;
      },
    };
  };
  return result;
}

function fakeDocument() {
  const classes = new Set();
  const events = [];
  return {
    documentElement: { dataset: {} },
    body: {
      classList: {
        contains(name) { return classes.has(name); },
        toggle(name, force) {
          if (force) classes.add(name);
          else classes.delete(name);
        },
      },
    },
    dispatchEvent(event) { events.push(event); return true; },
    classes,
    events,
  };
}

test("detectRenderer reads the unmasked renderer string and releases the context", function () {
  const fake = fakeCanvas({ renderer: "Google SwiftShader", vendor: "Google Inc." });
  const detection = detectRenderer(fake.factory);
  assert.equal(detection.available, true);
  assert.equal(detection.renderer, "Google SwiftShader");
  assert.equal(detection.vendor, "Google Inc.");
  assert.equal(detection.software, true);
  // 检测与探针各取一次、正式上下文必须释放，避免多占 WebGL 上下文。
  assert.equal(fake.created, 2);
  assert.equal(fake.state.lost, 1);
});

test("detectRenderer falls back to RENDERER when the debug extension is hidden", function () {
  const fake = fakeCanvas({ renderer: "llvmpipe (LLVM 15.0.7, 256 bits)", debug: false });
  const detection = detectRenderer(fake.factory);
  assert.equal(detection.renderer, "llvmpipe (LLVM 15.0.7, 256 bits)");
  assert.equal(detection.vendor, "Google Inc. (NVIDIA)");
  assert.equal(detection.software, true);
});

test("detectRenderer reports unavailable when WebGL cannot be created", function () {
  const none = detectRenderer(function () { return null; });
  assert.deepEqual(none, { available: false, renderer: "", vendor: "", software: true });

  const throwing = fakeCanvas({ throwOnContext: true });
  assert.deepEqual(detectRenderer(throwing.factory),
    { available: false, renderer: "", vendor: "", software: true });

  const noFactory = detectRenderer(function () { throw new Error("no canvas"); });
  assert.equal(noFactory.available, false);
  assert.equal(noFactory.software, true);
});

test("detectRenderer treats failIfMajorPerformanceCaveat failure as software rendering", function () {
  // 渲染器名被隐藏/伪装成通用值时，靠上下文属性仍能识别软件渲染。
  const fake = fakeCanvas({ renderer: "WebKit WebGL", caveat: true });
  const detection = detectRenderer(fake.factory);
  assert.equal(detection.available, true);
  assert.equal(detection.renderer, "WebKit WebGL");
  assert.equal(detection.software, true);
  assert.equal(resolvePerformanceMode("auto", detection).low, true);
});

test("isLowPerformanceRenderer catches software and virtual adapters only", function () {
  for (const name of [
    "Google SwiftShader",
    "SwiftShader Device (Subzero)",
    "ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device (Subzero)))",
    "llvmpipe (LLVM 15.0.7, 256 bits)",
    "softpipe",
    "Microsoft Basic Render Driver",
    "ANGLE (Microsoft, Microsoft Basic Render Driver Direct3D11 vs_5_0 ps_5_0)",
    "VMware SVGA 3D",
    "VirtualBox Graphics Adapter",
    "Microsoft Hyper-V Video",
    "Parallels Display Adapter",
    "virgl (virtio)",
  ]) {
    assert.equal(isLowPerformanceRenderer(name), true, name + " 应判为低性能");
  }
  for (const name of [
    "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0)",
    "Apple M2",
    "Intel(R) UHD Graphics 630",
    "AMD Radeon RX 6800 XT",
    "",
  ]) {
    assert.equal(isLowPerformanceRenderer(name), false, name + " 不应判为低性能");
  }
});

test("normalizePerformanceMode only allows auto/on/off", function () {
  assert.deepEqual(PERFORMANCE_MODES, ["auto", "on", "off"]);
  assert.equal(normalizePerformanceMode("on"), "on");
  assert.equal(normalizePerformanceMode("off"), "off");
  assert.equal(normalizePerformanceMode("auto"), "auto");
  for (const value of ["", undefined, null, "low", "ON", "1"]) {
    assert.equal(normalizePerformanceMode(value), "auto");
  }
});

test("resolvePerformanceMode maps setting plus renderer detection to low mode", function () {
  const software = { available: true, renderer: "Google SwiftShader", vendor: "" };
  const hardware = { available: true, renderer: "ANGLE (NVIDIA GeForce RTX 3060)", vendor: "" };

  const autoSoftware = resolvePerformanceMode("auto", software);
  assert.equal(autoSoftware.low, true);
  assert.equal(autoSoftware.reason, "software-renderer");

  const autoHardware = resolvePerformanceMode("auto", hardware);
  assert.equal(autoHardware.low, false);
  assert.equal(autoHardware.reason, "hardware");

  // 没有 WebGL 的机器大概率也没有硬件加速，自动模式按低配处理。
  const noWebGL = resolvePerformanceMode("auto", { available: false });
  assert.equal(noWebGL.low, true);
  assert.equal(noWebGL.reason, "no-webgl");

  // 检测结果里的 software 标记（failIfMajorPerformanceCaveat 兜底）同样生效。
  const flagged = resolvePerformanceMode("auto", {
    available: true, renderer: "WebKit WebGL", software: true,
  });
  assert.equal(flagged.low, true);
  assert.equal(flagged.reason, "software-renderer");

  // 手动覆盖优先于检测结果。
  const forcedOn = resolvePerformanceMode("on", hardware);
  assert.equal(forcedOn.low, true);
  assert.equal(forcedOn.reason, "forced-on");

  const forcedOff = resolvePerformanceMode("off", software);
  assert.equal(forcedOff.low, false);
  assert.equal(forcedOff.reason, "forced-off");

  // 未知设置值按 auto 处理。
  assert.equal(resolvePerformanceMode("bogus", software).low, true);
  assert.equal(resolvePerformanceMode(undefined, hardware).low, false);
});

test("applyPerformanceMode toggles body.perf-low, html data-perf and broadcasts", function () {
  const doc = fakeDocument();
  assert.equal(performanceLow(), false);

  assert.equal(applyPerformanceMode(doc, true), true);
  assert.equal(performanceLow(), true);
  assert.equal(doc.documentElement.dataset.perf, "low");
  assert.equal(doc.classes.has(PERFORMANCE_LOW_CLASS), true);
  assert.equal(doc.events.length, 1);
  assert.equal(doc.events[0].type, "qka-perf-changed");
  assert.equal(doc.events[0].detail.low, true);

  applyPerformanceMode(doc, false);
  assert.equal(performanceLow(), false);
  assert.equal(doc.documentElement.dataset.perf, "high");
  assert.equal(doc.classes.has(PERFORMANCE_LOW_CLASS), false);
  assert.equal(doc.events.length, 2);
});

test("initPerformanceMode detects once and reuses the cached renderer", function () {
  resetPerformanceModeCache();
  const doc = fakeDocument();
  const fake = fakeCanvas({ renderer: "Microsoft Basic Render Driver" });

  const first = initPerformanceMode({ setting: "auto", document: doc, createCanvas: fake.factory });
  assert.equal(first.low, true);
  assert.equal(first.reason, "software-renderer");
  assert.equal(first.detection.renderer, "Microsoft Basic Render Driver");
  assert.equal(lastResolvedPerformanceMode().low, true);
  const createdAfterFirst = fake.created;

  const second = initPerformanceMode({ setting: "off", document: doc, createCanvas: fake.factory });
  assert.equal(second.low, false);
  assert.equal(fake.created, createdAfterFirst, "同一页面只应创建一次检测用的 canvas");

  resetPerformanceModeCache();
  initPerformanceMode({ setting: "auto", document: doc, createCanvas: fake.factory });
  assert.equal(fake.created, createdAfterFirst * 2);
});

test("performance mode export stays in the documented 20-24fps band", function () {
  assert.ok(PERFORMANCE_LOW_FPS >= 20 && PERFORMANCE_LOW_FPS <= 24,
    "低配模式帧率应在 20-24fps 之间，实际 " + PERFORMANCE_LOW_FPS);
  assert.equal(PERFORMANCE_MODE_KEY, "performance_mode");
});

test("applyPerformanceMode tolerates a missing document", function () {
  resetPerformanceModeCache();
  assert.equal(applyPerformanceMode(null, true), true);
  assert.equal(performanceLow(), true);
  resetPerformanceModeCache();
});
