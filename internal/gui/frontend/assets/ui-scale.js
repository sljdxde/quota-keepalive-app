// 界面缩放：把整个界面按比例收缩，让 150% 缩放的笔记本也能一屏看全。
//
// 背景（实测）：1920×1080 屏在 Windows 150% 缩放下，主窗口内容是 1218×595
// CSS px，而界面是按 1240×820 设计的——高度差 225px，长页面必须滚动，用户会
// 描述成「字太大、界面展示不全」。
//
// 做法：给根元素设 CSS zoom（Chromium/WebView2 支持，会连带缩放布局、内边距与
// 字号），比例按「未缩放的视口」与设计尺寸算出。用 zoom 而不是逐个改字号，
// 是因为缩放必须同时作用于间距，否则字小了、留白不变，依然装不下。
//
// 纯函数（autoScale/parseStoredScale）导出以便 node 单测。

// UI_SCALE_SETTING 是设置项 key（见 internal/settings/schema.go）。
export const UI_SCALE_SETTING = "ui_scale";

// UI_SCALE_STORAGE_KEY 缓存最近一次生效的比例：下次启动在 head 里同步应用，
// 避免「先按 100% 画一帧、再缩回去」的闪烁。
export const UI_SCALE_STORAGE_KEY = "qka.uiScale";

// 设计目标尺寸（CSS px）：界面在这个内容区尺寸下排版正好，不需要缩放。
// 取值是「默认窗口（见 window_size.go 的 mainIdealWidth/Height）减去标题栏
// 与边框后的实际内容区」——920×640 DIP 的窗口内容区约 906×603，所以取
// 900×600：默认窗口下 autoScale 正好返回 1（原生字号，最清晰），只有窗口被
// 拖小或屏幕更矮时才收缩。
export const DESIGN_WIDTH = 900;
export const DESIGN_HEIGHT = 600;

// 自动缩放的下限：再小字号就影响可读性了，宁可让用户滚动。
export const MIN_AUTO_SCALE = 0.85;

// autoScale 按「未缩放的视口尺寸」算自动缩放比例。
// 返回 0.85~1 之间的两位小数（0.05 步长），视口未知时返回 1。
export function autoScale(width, height) {
  const w = Number(width);
  const h = Number(height);
  if (!(w > 0) || !(h > 0)) return 1;
  const raw = Math.min(1, w / DESIGN_WIDTH, h / DESIGN_HEIGHT);
  // 向下取 0.05 步长：0.87 → 0.85，避免出现 0.87/0.93 这类怪异比例。
  const stepped = Math.floor(raw * 20) / 20;
  if (stepped >= 1) return 1;
  return Math.max(MIN_AUTO_SCALE, Number(stepped.toFixed(2)));
}

// resolveScale 把设置值解析成实际比例。
// setting 为 "auto" 或空时走自动；数字字符串按固定值（超出 0.5~1.5 视为无效）。
export function resolveScale(setting, rawWidth, rawHeight) {
  const value = String(setting === undefined || setting === null ? "" : setting).trim();
  if (value === "" || value === "auto") {
    return autoScale(rawWidth, rawHeight);
  }
  const fixed = Number(value);
  if (!Number.isFinite(fixed) || fixed < 0.5 || fixed > 1.5) {
    return autoScale(rawWidth, rawHeight);
  }
  return fixed;
}

// readViewport 返回用于计算缩放的视口尺寸。
//
// 关键：Chromium 的 CSS zoom（作用在根元素上）不会改变 window.innerWidth /
// innerHeight——它们始终是未缩放的布局视口。所以这里直接用原值，不能乘回
// 当前 zoom（那会多缩一次，比例来回漂移）。缩放生效后，子元素可用空间变为
// innerWidth / zoom，这正是「缩了之后能装下更多内容」的来源。
export function readViewport(innerWidth, innerHeight) {
  const width = Number(innerWidth);
  const height = Number(innerHeight);
  return {
    width: width > 0 ? width : 0,
    height: height > 0 ? height : 0,
  };
}

// applyZoom 把比例写到根元素；1 表示不缩放（清掉内联样式）。
export function applyZoom(root, scale) {
  if (!root || !root.style) return;
  if (!(scale > 0) || Math.abs(scale - 1) < 0.001) {
    root.style.zoom = "";
    return;
  }
  root.style.zoom = String(scale);
}

// initUiScale 持续维护缩放：读设置 → 应用 → 窗口尺寸变化后重算（自动模式）。
// readSetting 返回 Promise<string>；onScale 用于上报诊断/测试。
export function initUiScale(options) {
  const opts = options || {};
  const root = opts.root || document.documentElement;
  const readSetting = opts.readSetting;
  const onScale = opts.onScale || function () {};
  let setting = opts.initialSetting || "auto";
  let applied = 1;
  let timer = 0;

  function refresh() {
    const viewport = readViewport(window.innerWidth, window.innerHeight);
    const scale = resolveScale(setting, viewport.width, viewport.height);
    if (Math.abs(scale - applied) < 0.001) {
      onScale(applied, viewport);
      return applied;
    }
    applied = scale;
    applyZoom(root, scale);
    try {
      window.localStorage.setItem(UI_SCALE_STORAGE_KEY, String(scale));
    } catch (_) {}
    onScale(scale, viewport);
    return scale;
  }

  function schedule() {
    if (timer) window.clearTimeout(timer);
    timer = window.setTimeout(function () {
      timer = 0;
      refresh();
    }, 150);
  }

  window.addEventListener("resize", schedule);
  refresh();

  if (typeof readSetting === "function") {
    Promise.resolve()
      .then(readSetting)
      .then(function (value) {
        if (value === undefined || value === null || value === "") return;
        setting = value;
        refresh();
      })
      .catch(function (_) {});
  }

  return {
    // setSetting 供设置页切换后即时生效。
    setSetting: function (value) {
      setting = value;
      refresh();
    },
    current: function () {
      return applied;
    },
    refresh: refresh,
  };
}
