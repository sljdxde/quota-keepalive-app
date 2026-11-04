// 诊断区块（issue #6）：用户反馈「卡」时自助取证。
//
// 数据分两半，复制时在前端合并成一段纯文本，方便直接贴进 issue：
//   后端（Bound.DiagnosticsGet）：版本、平台、Go 版本、配置/日志/运行时目录、
//     tokenspeed 慢轮询环形记录；
//   前端（perf-mode.js，只存在页面内存里）：UNMASKED_RENDERER_WEBGL 渲染器、
//     vendor、软件渲染标志、性能模式解析结果与 3D 宠物目标帧率。
// 红线：只收集性能与版本元数据，绝不包含凭据、提示词或 token 内容。
//
// 纯函数导出（petTargetFPS/formatBytes/buildDiagnosticsText/parseDiagnostics）
// 便于 node 单测；initDiagnostics 负责把两个设置页（main.html 的设置页签与
// 独立 settings.html）上的同构 DOM 填满。

import * as Bound from "../bindings/qka/internal/gui/boundservice.js";
import {
  initPerformanceMode,
  lastResolvedPerformanceMode,
  performanceLow,
  PERFORMANCE_LOW_FPS,
  PERFORMANCE_MODE_KEY,
} from "./perf-mode.js";

// 高配模式跟随 requestAnimationFrame（通常 60fps）；低配模式由
// pokemon-3d.js 限到 PERFORMANCE_LOW_FPS（20fps）。
export const PET_FULL_FPS = 60;

// petTargetFPS 返回 3D 宠物当前目标帧率。low 缺省时读本页实时状态。
export function petTargetFPS(low) {
  const isLow = low === undefined ? performanceLow() : !!low;
  return isLow ? PERFORMANCE_LOW_FPS : PET_FULL_FPS;
}

// formatBytes 把字节数格式化成人类可读的 MB/KB，诊断文本用。
export function formatBytes(bytes) {
  const value = Number(bytes) || 0;
  if (value >= 1024 * 1024) return (value / (1024 * 1024)).toFixed(1) + " MB";
  if (value >= 1024) return (value / 1024).toFixed(1) + " KB";
  return value + " B";
}

function asJSON(v) {
  if (typeof v === "string") return JSON.parse(v);
  return v;
}

function text(view, key, fallback) {
  const value = view && view[key];
  return value === undefined || value === null || value === "" ? (fallback || "—") : String(value);
}

// rendererLines 把 perf-mode 的检测结果整理成展示与复制都用得上的文案。
// resolved 是 initPerformanceMode 的返回值（含 detection：UNMASKED_RENDERER_
// WEBGL 的渲染器/vendor 与软件渲染标志）。
function rendererLines(resolved) {
  if (!resolved) {
    return {
      renderer: "尚未初始化",
      software: "—",
      mode: "尚未初始化",
      detail: "",
    };
  }
  const rendererName = resolved.renderer || "（未读到 UNMASKED_RENDERER_WEBGL）";
  const vendorName = resolved.vendor ? "，vendor " + resolved.vendor : "";
  const reasonText = {
    "forced-on": "手动低配",
    "forced-off": "手动高性能",
    "no-webgl": "无 WebGL，自动低配",
    "software-renderer": "软件渲染，自动低配",
    hardware: "硬件加速",
  }[resolved.reason] || resolved.reason;
  // software 标志优先取检测结果（detection.software），它不被手动覆盖影响；
  // 检测缺失时退回按 reason 推断。
  const detection = resolved.detection || {};
  let software;
  if (detection.available === false || resolved.available === false) {
    software = "无 WebGL";
  } else if (typeof detection.software === "boolean") {
    software = detection.software ? "是" : "否";
  } else {
    software = /software-renderer/.test(resolved.reason) ? "是" : "否";
  }
  return {
    renderer: rendererName + vendorName,
    software: software,
    mode: (resolved.setting || "auto") + " → " + (resolved.low ? "低配" : "高性能"),
    detail: reasonText,
  };
}

// buildDiagnosticsText 生成一键复制的纯文本。backend 是 DiagnosticsGet 的视图，
// resolved 是 lastResolvedPerformanceMode() 的结果（可为 null）。
export function buildDiagnosticsText(backend, resolved, atText) {
  const info = backend || {};
  const lines = [];
  lines.push("Quota Keepalive 诊断信息（仅性能与版本元数据，不含凭据）");
  lines.push("生成时间: " + (atText || text(info, "generated_at")));
  lines.push("版本: " + text(info, "version"));
  lines.push("平台: " + text(info, "goos") + "/" + text(info, "goarch") +
    "，" + text(info, "go_version"));
  const runtime = info.runtime_dir
    ? info.runtime_dir + (info.runtime_available
      ? "（占用 " + formatBytes(info.runtime_bytes) + "，" + info.runtime_files + " 个文件）"
      : "（目录不存在，可能未内嵌运行时）")
    : "—";
  lines.push("运行时目录: " + runtime);
  const render = rendererLines(resolved);
  lines.push("渲染器: " + render.renderer);
  lines.push("软件渲染: " + render.software);
  lines.push("性能模式: " + render.mode + (render.detail ? "（" + render.detail + "）" : ""));
  lines.push("3D 宠物目标帧率: " + petTargetFPS(resolved ? resolved.low : undefined) + " fps");
  const polls = Array.isArray(info.slow_polls) ? info.slow_polls : [];
  const threshold = Number(info.slow_poll_threshold_ms) || 150;
  lines.push("tokenspeed 慢轮询（阈值 " + threshold + "ms，最近 " + polls.length + " 次）:");
  if (!info.token_speed_available) {
    lines.push("  （tokenspeed 服务未运行）");
  } else if (!polls.length) {
    lines.push("  无——最近没有超过阈值的轮询");
  } else {
    polls.forEach(function (sample) {
      lines.push("  " + String(sample.at || "") + "  " + Number(sample.elapsed_ms || 0) + "ms");
    });
  }
  lines.push("日志目录: " + text(info, "log_dir"));
  return lines.join("\n");
}

// renderSlowPolls 把慢轮询环形记录整理成一行文案（旧→新，最多展示全部记录）。
function renderSlowPolls(info) {
  const polls = Array.isArray(info.slow_polls) ? info.slow_polls : [];
  const threshold = Number(info.slow_poll_threshold_ms) || 150;
  if (!info.token_speed_available) return "服务未运行";
  if (!polls.length) return "无（阈值 " + threshold + "ms）";
  return polls.slice(-10).map(function (sample) {
    return String(sample.at || "").replace("T", " ").slice(5, 19) + " " +
      Number(sample.elapsed_ms || 0) + "ms";
  }).join("；") + "（阈值 " + threshold + "ms）";
}

function renderRuntime(info) {
  if (!info.runtime_dir) return "—";
  if (!info.runtime_available) return info.runtime_dir + "（不存在）";
  return info.runtime_dir + "（" + formatBytes(info.runtime_bytes) + "，" +
    info.runtime_files + " 个文件）";
}

let cachedBackendInfo = null;
let cachedResolved = null;
let cachedAtText = "";

export function cachedDiagnostics() {
  return { backend: cachedBackendInfo, resolved: cachedResolved, atText: cachedAtText };
}

async function copyPlainText(value) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch (_) { /* 落到 execCommand 兜底 */ }
  try {
    const helper = document.createElement("textarea");
    helper.value = value;
    helper.setAttribute("readonly", "");
    helper.style.position = "fixed";
    helper.style.opacity = "0";
    document.body.appendChild(helper);
    helper.select();
    const ok = document.execCommand("copy");
    helper.remove();
    return ok;
  } catch (_) {
    return false;
  }
}

// initDiagnostics 填充两个设置页共用的诊断 DOM（id 前缀 diagnostics-）并接好
// 「复制诊断信息」「打开日志目录」两个按钮。渲染器检测在页面里已有缓存的
// 情况下直接复用；没有则用后端设置值重新 resolve 一次（不创建第二个 WebGL
// 上下文，detectRenderer 结果在 perf-mode 内有页面级缓存）。
export async function initDiagnostics() {
  // 两个页面的状态徽标类不同（main.html 是 pill，settings.html 是
  // sched-state），保留初始类名前缀，只追加状态修饰类。
  const state = document.getElementById("diagnostics-state");
  const stateBase = state ? String(state.className || "").split(/\s+/)[0] : "";
  const stateGoodSuffix = stateBase === "pill" ? " good" : " on";
  const setStatus = function (label, warn) {
    if (!state) return;
    state.textContent = label;
    state.className = stateBase +
      (warn === undefined ? "" : warn ? " warn" : stateGoodSuffix);
  };

  let backend = null;
  try {
    backend = asJSON(await Bound.DiagnosticsGet());
    cachedBackendInfo = backend;
  } catch (_) {
    if (state) setStatus("读取失败", true);
  }

  // 性能模式解析：优先用当前设置值 resolve，保证与页面实际生效状态一致。
  try {
    const values = asJSON(await Bound.SettingsGet());
    cachedResolved = initPerformanceMode({
      setting: values && values[PERFORMANCE_MODE_KEY],
    });
  } catch (_) {
    cachedResolved = lastResolvedPerformanceMode();
  }
  cachedAtText = new Date().toLocaleString("zh-CN", { hour12: false });
  if (!backend) return;

  const resolved = cachedResolved;
  const render = rendererLines(resolved);
  const set = function (id, value) {
    const el = document.getElementById(id);
    if (el) el.textContent = value;
  };
  set("diagnostics-version",
    text(backend, "version") + " · " + text(backend, "goos") + "/" + text(backend, "goarch") +
    " · " + text(backend, "go_version"));
  set("diagnostics-runtime", renderRuntime(backend));
  set("diagnostics-renderer", render.renderer + " · 软件渲染: " + render.software);
  set("diagnostics-perf-mode", render.mode + (render.detail ? " · " + render.detail : ""));
  set("diagnostics-pet-fps",
    petTargetFPS(resolved ? resolved.low : undefined) + " fps");
  set("diagnostics-slow-polls", renderSlowPolls(backend));
  set("diagnostics-logdir", text(backend, "log_dir"));
  if (state) setStatus("已就绪");

  const copyButton = document.getElementById("diagnostics-copy");
  if (copyButton) {
    copyButton.onclick = async function () {
      copyButton.disabled = true;
      try {
        const ok = await copyPlainText(buildDiagnosticsText(
          cachedBackendInfo, cachedResolved, cachedAtText));
        copyButton.textContent = ok ? "已复制" : "复制失败";
        window.setTimeout(function () {
          copyButton.textContent = "复制诊断信息";
        }, 1600);
      } finally {
        copyButton.disabled = false;
      }
    };
  }

  const openButton = document.getElementById("diagnostics-open-logdir");
  if (openButton) {
    openButton.onclick = async function () {
      openButton.disabled = true;
      try {
        await Bound.DiagnosticsOpenLogDir();
      } catch (_) {
        openButton.textContent = "打开失败";
        window.setTimeout(function () {
          openButton.textContent = "打开日志目录";
        }, 1600);
        if (state) {
          setStatus("打开失败", true);
        }
      } finally {
        openButton.disabled = false;
      }
    };
  }
}
