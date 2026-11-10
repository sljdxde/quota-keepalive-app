import * as Bound from "../bindings/qka/internal/gui/boundservice.js";
import { Window, Events } from "/wails/runtime.js";
import {
  POKEMON_CATALOG,
  POKEMON_EVOLUTION_THRESHOLDS,
  POKEMON_MAX_DAILY_TOKENS,
  evolutionStageForDailyTokens,
  pokemonById,
} from "./pokemon-art.js";
import { nextGroupExpansion } from "./group-collapse.js";
import { aimForClientPoint } from "./pet-gaze.js";
import { resolvePetGesture } from "./bar-gesture.js";
import { quotaViewForBalance, quotaViewForReading } from "./account-quota.js";
import {
  CAPTURE_SWEEP,
  autoSkillDelay,
  captureOutcome,
  sweepProgress,
  timingAccuracy,
} from "./pet-motion.js";
import { createPokemon3D } from "./pokemon-3d.js";
import { initPerformanceMode, PERFORMANCE_MODE_KEY } from "./perf-mode.js";
import { populateProviderIcon, providerIconSpec } from "./provider-icons.js";
import {
  normalizeTitleTheme,
  titleIconMarkup,
  titleLevelForSpeed,
  titleLevelProgress,
  titleThemeOrder,
  titleThresholds,
} from "./title-system.js";

const panelTabIds = ["checkin", "speed", "pet"];
const panelTabStorageKey = "qka.panel.tab";
const petSelectedStorageKey = "qka.pet.selected";
// 与主窗口 Agent 详情共用同一个隐私开关：在哪边切了，另一边也一致。
const accountPrivacyStorageKey = "qka.accounts.hidden";
// 旧版本把面板开关存在自己的 key 里，读一次并迁移过去，不让用户重设。
const legacyPanelPrivacyStorageKey = "qka.panel.hideAccounts";
const opLabels = { checkin: "签到" };
const expanded = {};
const providerInfo = {};
const speedHistory = Array.from({ length: 24 }, function () { return 0; });
const latestPointer = { x: null, y: null };
const agentExpansion = new Map();

let activeTab = "checkin";
let currentAccounts = [];
let currentTargets = [];
let latestSpeedOutput = 0;
let latestSpeedActive = false;
let latestTodayTokens = 0;
// pendingSpeedSnapshot 暂存非速度页/窗口隐藏时到达的最新快照，
// 切回速度页时再渲染一次，避免每 750ms 重建隐藏页的 DOM。
let pendingSpeedSnapshot = null;
let animationFrame = 0;
let animatedOutput = 0;
let speedSeen = false;
let speedHistorySeeded = false;
let selectedPetId = readStoredString(petSelectedStorageKey, "bulbasaur");
let petMessageIndex = 0;
let refreshInFlight = null;
let lastStatusView = null;
let hideAccounts = readStoredString(accountPrivacyStorageKey, "") === "1";
if (readStoredString(accountPrivacyStorageKey, "") === "" &&
    readStoredString(legacyPanelPrivacyStorageKey, "") === "1") {
  hideAccounts = true;
  writeStoredValue(accountPrivacyStorageKey, "1");
}
let renderedPetId = "";
let renderedSkillTier = 0;
let petViewer = null;
let petViewerPetId = "";

const petCatalog = POKEMON_CATALOG;

window.addEventListener("keydown", function (event) {
  if (event.key === "Escape") hidePanel();
});

function readStoredString(key, fallback) {
  try {
    return localStorage.getItem(key) || fallback;
  } catch (_) {
    return fallback;
  }
}

function writeStoredValue(key, value) {
  try {
    localStorage.setItem(key, String(value));
  } catch (_) {}
}

function asJSON(value) {
  return typeof value === "string" ? JSON.parse(value) : value;
}

function compactTokenRate(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) return "0";
  if (number < 10) {
    const rounded = Math.round(number * 10) / 10;
    return rounded >= 10 ? "10" : rounded.toFixed(1);
  }
  if (number < 1000) return String(Math.round(number));
  const units = [
    { value: 1e9, suffix: "B" },
    { value: 1e6, suffix: "M" },
    { value: 1e3, suffix: "k" },
  ];
  for (const unit of units) {
    const scaled = number / unit.value;
    if (scaled >= 1) {
      return (scaled >= 100 ? scaled.toFixed(0) : scaled.toFixed(2)) + unit.suffix;
    }
  }
  return String(Math.round(number));
}

function compactTokenCount(value) {
  const number = Math.max(0, Number(value) || 0);
  if (number >= 1e8) {
    const scaled = number / 1e8;
    return (scaled >= 10 ? scaled.toFixed(1) : scaled.toFixed(2))
      .replace(/\.0+$/, "") + " 亿";
  }
  if (number >= 1e4) {
    const scaled = number / 1e4;
    return (scaled >= 100 ? scaled.toFixed(0) : scaled.toFixed(1))
      .replace(/\.0$/, "") + " 万";
  }
  return String(Math.round(number));
}

function renderPanelTitle(view) {
  const themeId = normalizeTitleTheme(view && view.title_theme_id);
  const level = titleLevelForSpeed(
    view && view.output_tokens_per_second,
    !!(view && view.active)
  );
  const progress = titleLevelProgress(
    view && view.output_tokens_per_second,
    !!(view && view.active)
  );
  document.getElementById("panel-speed-icon").innerHTML =
    titleIconMarkup(themeId, level, {
      active: !!(view && view.active),
      compact: true,
      progress: progress,
    });
  document.getElementById("panel-speed").style.setProperty(
    "--title-progress",
    progress.toFixed(1) + "%"
  );
}

function sourceLogoNode(source) {
  const value = String(source || "").toLowerCase();
  const logo = document.createElement("span");
  logo.className = "source-logo";
  logo.setAttribute("aria-hidden", "true");
  if (providerIconSpec(value)) {
    return populateProviderIcon(logo, { id: value });
    } else if (value === "dsh" || value.indexOf("deepseek") >= 0) {
      logo.classList.add("dsh");
      logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
        '<path d="M2.4 5.6c1.6-1.8 3.1-1.8 4.7 0 1.6 1.8 3.1 1.8 4.7 0 1-.1 1.5.3 1.8.7" ' +
        'stroke="currentColor" stroke-width="1.35" stroke-linecap="round"/>' +
        '<path d="M2.4 9.2c1.6-1.8 3.1-1.8 4.7 0 1.6 1.8 3.1 1.8 4.7 0" ' +
        'stroke="currentColor" stroke-width="1.35" stroke-linecap="round"/>' +
        '<path d="M2.4 12.6c1.6-1.6 3.1-1.6 4.7 0" ' +
        'stroke="currentColor" stroke-width="1.2" stroke-linecap="round" opacity=".55"/></svg>';
  } else if (value.indexOf("claude") >= 0) {
    logo.classList.add("claude");
    logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
      '<path d="M8 2.2v11.6M2.2 8h11.6M3.9 3.9l8.2 8.2M12.1 3.9 3.9 12.1" ' +
      'stroke="currentColor" stroke-width="1.35" stroke-linecap="round"/></svg>';
  } else if (value.indexOf("gemini") >= 0) {
    logo.classList.add("gemini");
    logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
      '<path d="M8 1.8c.6 3.2 2.4 5 5.6 5.6-3.2.6-5 2.4-5.6 5.6-.6-3.2-2.4-5-5.6-5.6C5.6 6.8 7.4 5 8 1.8Z" ' +
      'stroke="currentColor" stroke-width="1.25" stroke-linejoin="round"/></svg>';
  } else {
    logo.classList.add("codex");
    logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
      '<path d="m8 1.9 5.2 3v6.2L8 14.1l-5.2-3V4.9L8 1.9Z" stroke="currentColor" stroke-width="1.25"/>' +
      '<path d="m5.7 8 1.5 1.5L10.4 6" stroke="currentColor" stroke-width="1.35" ' +
      'stroke-linecap="round" stroke-linejoin="round"/></svg>';
  }
  return logo;
}

function providerMarkNode(provider) {
  const mark = document.createElement("span");
  mark.className = "provider-mark";
  mark.setAttribute("aria-hidden", "true");
  return populateProviderIcon(mark, provider);
}

function activateTab(tabId, options) {
  const selected = panelTabIds.indexOf(tabId) >= 0 ? tabId : "checkin";
  const settings = options || {};
  activeTab = selected;
  document.getElementById("refresh").hidden = selected === "speed";
  document.querySelectorAll("[data-panel-tab]").forEach(function (tab) {
    const active = tab.dataset.panelTab === selected;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", active ? "true" : "false");
    tab.tabIndex = active ? 0 : -1;
  });
  panelTabIds.forEach(function (id) {
    const page = document.getElementById("page-" + id);
    const active = id === selected;
    page.hidden = !active;
    page.classList.toggle("active", active);
  });
  if (petViewer) petViewer.setEnabled(selected === "pet");
  // 隐藏期间丢弃了速度事件：切回速度页时用最新快照补一次渲染。
  if (selected === "speed" && pendingSpeedSnapshot) {
    const pending = pendingSpeedSnapshot;
    pendingSpeedSnapshot = null;
    renderSpeed(pending);
  }
  if (selected === "pet") {
    resetPetGaze();
    renderPet();
  }
  schedulePanelAutoSkill();
  if (selected === "pet") schedulePetTalk();
  else stopPetTalk();
  if (settings.persist !== false) writeStoredValue(panelTabStorageKey, selected);
  updateFooter();
  if (settings.focus) {
    const tab = document.getElementById("tab-" + selected);
    if (tab) tab.focus();
  }
  if (settings.load === true) {
    if (selected === "speed") refreshSpeed();
    if (selected === "checkin") refresh();
  }
}

document.querySelectorAll("[data-panel-tab]").forEach(function (tab) {
  tab.addEventListener("click", function () {
    activateTab(tab.dataset.panelTab, { load: true });
  });
  tab.addEventListener("keydown", function (event) {
    const current = panelTabIds.indexOf(tab.dataset.panelTab);
    let next = current;
    if (event.key === "ArrowRight") next = (current + 1) % panelTabIds.length;
    else if (event.key === "ArrowLeft") next = (current - 1 + panelTabIds.length) % panelTabIds.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = panelTabIds.length - 1;
    else return;
    event.preventDefault();
    activateTab(panelTabIds[next], { load: true, focus: true });
  });
});

function stateClass(state) {
  if (!state) return "wait";
  if (state.indexOf("failed") >= 0) return "bad";
  if (state === "signed_confirmed" || state === "already" ||
      state === "completed" || state === "ineligible" || state === "inactive") return "good";
  return "warn";
}

function stateLabel(state) {
  const labels = {
    signed_confirmed: "已到账",
    signed_provisional: "待复核",
    already: "已领取",
    ineligible: "不符合活动资格",
    inactive: "活动未开始或已结束",
    manual_required: "需 App 签到",
    failed_retryable: "可重试",
    failed_final: "失败",
    completed: "已完成",
  };
  return labels[state] || "待执行";
}

function displayAccountIdentity(value) {
  const text = String(value || "").trim();
  if (!text) return "账号";
  return text.indexOf("\uFFFD") >= 0 ? "账号信息待刷新" : text;
}

// accountIdentityText 优先用供应商视图里的完整账号标识；状态投影只有脱敏值，
// 取不到完整值时回落脱敏值。「显示账号」要显示完整信息，而不是继续打码。
function accountIdentityText(account) {
  const masked = displayAccountIdentity(account.masked_identity || account.display_name);
  const provider = providerInfo[account.target];
  const accountKey = account.account_id || account.id || "";
  const known = provider && Array.isArray(provider.accounts)
    ? provider.accounts.find(function (item) { return item && item.key === accountKey; })
    : null;
  const full = known && known.identity ? displayAccountIdentity(known.identity) : "";
  return full || masked;
}

// 马赛克：按账号标识哈希生成固定宽度/深浅的小色块，刷新后图案不变；
// DOM 里不保留明文标识，避免「只是视觉遮住、复制/读屏还能拿到」。
function mosaicIdentityNode(text) {
  const span = document.createElement("span");
  span.className = "acct-mosaic";
  span.setAttribute("aria-hidden", "true");
  const source = String(text || "");
  let seed = 2166136261;
  for (let i = 0; i < source.length; i++) {
    seed ^= source.charCodeAt(i);
    seed = Math.imul(seed, 16777619) >>> 0;
  }
  const count = Math.max(6, Math.min(14, Math.round(source.length * 1.6)));
  for (let i = 0; i < count; i++) {
    const cell = document.createElement("i");
    const bits = (seed >>> ((i * 3) % 24)) & 7;
    cell.style.width = 3 + (bits & 3) + "px";
    cell.style.opacity = (0.35 + ((bits >> 2) & 3) * 0.18).toFixed(2);
    span.appendChild(cell);
  }
  return span;
}

function syncPrivacyToggle() {
  const page = document.getElementById("page-checkin");
  if (page) page.classList.toggle("is-private", hideAccounts);
  const button = document.getElementById("account-privacy");
  if (!button) return;
  button.setAttribute("aria-pressed", hideAccounts ? "true" : "false");
  const label = hideAccounts ? "显示账号" : "隐藏账号（马赛克）";
  button.setAttribute("aria-label", label);
  button.title = label;
  const text = document.getElementById("account-privacy-label");
  if (text) text.textContent = hideAccounts ? "显示账号" : "隐藏账号";
}

function toggleAccountPrivacy() {
  hideAccounts = !hideAccounts;
  writeStoredValue(accountPrivacyStorageKey, hideAccounts ? "1" : "0");
  syncPrivacyToggle();
  if (lastStatusView) render(lastStatusView);
}

function syncPanelCollapseButton() {
  const button = document.getElementById("checkin-collapse-all");
  if (!button) return;
  const allCollapsed = currentTargets.length > 0 &&
    currentTargets.every(function (target) { return expanded[target] === false; });
  button.textContent = allCollapsed ? "全部展开" : "全部收起";
  button.setAttribute("aria-pressed", allCollapsed ? "true" : "false");
  button.disabled = currentTargets.length === 0;
}

function openCheckin(payload) {
  Events.Emit("open-checkin", payload || {});
  hidePanel();
}

function primaryOperation(account) {
  const operations = account.operations || [];
  return operations.find(function (operation) {
    return operation.operation === "checkin";
  }) || null;
}

function summaryFor(accounts) {
  if (!accounts.length) return { cls: "warn", title: "暂无账号", sub: "打开主窗口添加" };
  let pending = 0;
  let failed = 0;
  let done = 0;
  accounts.forEach(function (account) {
    if (account.source_health === "reauth_required") {
      pending++;
      return;
    }
    const operation = primaryOperation(account);
    if (!operation) return;
    const cls = stateClass(operation.state);
    if (cls === "bad") failed++;
    else if (cls === "warn") pending++;
    else if (cls === "good") done++;
  });
  if (failed) return { cls: "bad", title: failed + " 个失败", sub: "需要处理" };
  if (pending) return { cls: "warn", title: pending + " 个待处理", sub: "需要确认" };
  if (done) return { cls: "good", title: "今日完成", sub: accounts.length + " 个账号" };
  return { cls: "warn", title: "待巡检", sub: accounts.length + " 个账号" };
}

function needsAttention(account) {
  if (account.source_health === "reauth_required") return true;
  const operation = primaryOperation(account);
  return !!operation && (operation.state === "manual_required" ||
    operation.state === "failed_retryable" || operation.state === "failed_final");
}

// 额度观测缓存：面板每次弹出都会重建账号卡，同一账号 2 分钟内只取一次历史，
// 避免频繁唤起供应商运行时。
const accountQuotaCache = new Map();
const ACCOUNT_QUOTA_CACHE_MS = 120000;

// applyQuotaLine 把纯函数算出的视图落到 DOM：文案 + 可选已用比例细条。
function applyQuotaLine(line, view) {
  line.replaceChildren();
  line.hidden = false;
  line.classList.toggle("none", !view);
  if (!view) {
    line.textContent = "暂无额度读数";
    return;
  }
  const text = document.createElement("span");
  text.textContent = view.text;
  line.appendChild(text);
  if (view.pct == null) return;
  const track = document.createElement("span");
  track.className = "quota-track";
  const bar = document.createElement("i");
  bar.className = "quota-bar" + (view.pct >= 100 ? " full" : "");
  bar.style.width = view.pct.toFixed(1) + "%";
  track.appendChild(bar);
  line.appendChild(track);
}

// fillAccountQuota 异步补账号的额度行：先给签到返回的余额兜底，再尝试读最近
// 一次额度观测（带缓存）。节点已被下一次渲染替换时不再写入。
async function fillAccountQuota(line, account, operation) {
  if (!line) return;
  const fallback = quotaViewForBalance(operation && operation.balance);
  if (fallback) applyQuotaLine(line, fallback);
  const providerId = account.target;
  const accountKey = account.account_id || account.id || "";
  if (!providerId || !accountKey) return;
  const cacheKey = providerId + "|" + accountKey;
  const cached = accountQuotaCache.get(cacheKey);
  let reading = null;
  if (cached && Date.now() - cached.at < ACCOUNT_QUOTA_CACHE_MS) {
    reading = cached.reading;
  } else {
    try {
      const page = asJSON(await Bound.ProvidersHistory(providerId, accountKey, 1));
      reading = (page.quota || [])[0] || null;
      accountQuotaCache.set(cacheKey, { at: Date.now(), reading: reading });
    } catch (_) {
      return;
    }
  }
  if (!line.isConnected) return;
  if (!reading) {
    if (!fallback) applyQuotaLine(line, null);
    return;
  }
  applyQuotaLine(line, quotaViewForReading(reading));
}

function accountNode(account) {
  const node = document.createElement("div");
  const operation = primaryOperation(account);
  const state = (operation && operation.state) || "";
  node.className = "account" + (stateClass(state) === "bad" ? " bad" : "");
  node.dataset.accountKey = account.account_id || account.id || "";
  node.tabIndex = 0;
  node.setAttribute("role", "link");
  const identity = accountIdentityText(account);
  node.setAttribute("aria-label", hideAccounts
    ? "打开主窗口查看账号（已隐藏）"
    : "打开主窗口查看账号 " + identity);

  const copy = document.createElement("div");
  const title = document.createElement("div");
  title.className = "acct-title";
  const dot = document.createElement("i");
  dot.className = "dot " +
    (account.source_health === "reauth_required" ? "warn" : stateClass(state));
  title.appendChild(dot);
  if (hideAccounts) {
    title.appendChild(mosaicIdentityNode(identity));
  } else {
    title.appendChild(document.createTextNode(identity));
  }
  copy.appendChild(title);

  const meta = document.createElement("div");
  meta.className = "acct-meta";
  if (account.source_health === "reauth_required") {
    meta.textContent = "登录已失效，需重新登录";
  } else if (account.source_health === "healthy") {
    meta.textContent = "受管来源 · 健康";
  } else {
    meta.textContent = "受管来源 · 待检查";
  }
  copy.appendChild(meta);
  // 额度行：与主窗口同一套口径（剩余 / 总量）。先渲染账号卡，再异步补最近
  // 一次额度观测；供应商只在用量窗口里给额度时也能显示出来。
  const quotaLine = document.createElement("div");
  quotaLine.className = "acct-quota";
  quotaLine.hidden = true;
  copy.appendChild(quotaLine);
  const operations = account.operations || [];
  if (operations.length) {
    const strip = document.createElement("div");
    strip.className = "acct-ops";
    operations.forEach(function (entry) {
      const chip = document.createElement("span");
      chip.className = "op-chip " + stateClass(entry.state);
      chip.textContent = (opLabels[entry.operation] || "操作") + " · " +
        stateLabel(entry.state);
      // 插件原文（英文实现细节）收进悬停提示，卡片上只说人话。
      if (entry.message) chip.title = entry.message;
      strip.appendChild(chip);
    });
    copy.appendChild(strip);
  }
  node.appendChild(copy);
  fillAccountQuota(quotaLine, account, operation);

  const action = document.createElement("button");
  action.className = "mini-action" + (stateClass(state) === "bad" ? " danger" : "");
  action.type = "button";
  action.textContent = account.source_health === "reauth_required" ? "处理" :
    state === "manual_required" ? "复查" : "证据";
  action.addEventListener("click", function (event) {
    event.stopPropagation();
    openCheckin({
      target: account.target,
      account_key: account.account_id || account.id || "",
      scope: needsAttention(account) ? "attention" : "account",
    });
  });
  node.appendChild(action);
  function activateAccount() {
    openCheckin({
      target: account.target,
      account_key: account.account_id || account.id || "",
      scope: needsAttention(account) ? "attention" : "account",
    });
  }
  node.addEventListener("click", function (event) {
    if (event.target.closest("button")) return;
    activateAccount();
  });
  node.addEventListener("keydown", function (event) {
    if (event.target !== node || (event.key !== "Enter" && event.key !== " ")) return;
    event.preventDefault();
    activateAccount();
  });
  return node;
}

function fillEmpty(container, title, detail) {
  container.innerHTML = "";
  const empty = document.createElement("div");
  empty.className = "empty";
  const heading = document.createElement("b");
  heading.textContent = title;
  empty.appendChild(heading);
  empty.appendChild(document.createTextNode(detail));
  container.appendChild(empty);
}

function providerForTarget(target) {
  return providerInfo[target] || { id: target, name: target, icon: "" };
}

function render(view) {
  lastStatusView = view;
  syncPrivacyToggle();
  currentAccounts = view.accounts || [];
  const box = document.getElementById("targets");
  box.innerHTML = "";

  const attention = currentAccounts.filter(needsAttention).length;
  const completed = currentAccounts.filter(function (account) {
    if (account.source_health === "reauth_required") return false;
    const operation = primaryOperation(account);
    return !!operation && stateClass(operation.state) === "good";
  }).length;
  const summaryData = {
    total: currentAccounts.length,
    done: completed,
    attention: attention,
  };
  document.getElementById("summary-total").innerHTML =
    summaryData.total + "<small>个</small>";
  document.getElementById("summary-done").innerHTML =
    summaryData.done + "<small>个</small>";
  document.getElementById("summary-attention").innerHTML =
    summaryData.attention + "<small>个</small>";
  const badge = document.getElementById("checkin-badge");
  badge.hidden = summaryData.attention === 0;
  badge.textContent = String(summaryData.attention);
  document.getElementById("checkin-updated").textContent =
    "更新于 " + new Date().toLocaleTimeString("zh-CN", { hour12: false });

  if (!currentAccounts.length) {
    box.classList.add("empty-list");
    fillEmpty(
      box,
      "还没有已启用账号",
      "打开主窗口，在「签到管理」中添加或启用供应商账号。"
    );
  } else {
    box.classList.remove("empty-list");
  }

  const grouped = {};
  currentAccounts.forEach(function (account) {
    (grouped[account.target] = grouped[account.target] || []).push(account);
  });

  currentTargets = Object.keys(grouped).sort();
  currentTargets.forEach(function (target) {
    const group = grouped[target];
    const provider = providerForTarget(target);
    // 默认收起：面板只展示到供应商层级，账号详情要手动展开。
    if (expanded[target] === undefined) expanded[target] = false;
    const app = document.createElement("section");
    app.className = "app" + (expanded[target] ? " open" : "");
    app.dataset.target = target;
    const head = document.createElement("button");
    head.type = "button";
    head.className = "app-head";
    head.setAttribute("aria-expanded", expanded[target] ? "true" : "false");
    head.appendChild(providerMarkNode(provider));
    const who = document.createElement("div");
    who.className = "who";
    const name = document.createElement("div");
    name.className = "name";
    name.textContent = provider.name || provider.id || target;
    const sub = document.createElement("div");
    sub.className = "sub";
    sub.textContent = "签到 · " + group.length + " 个账号";
    who.appendChild(name);
    who.appendChild(sub);
    head.appendChild(who);

    const summary = summaryFor(group);
    const state = document.createElement("div");
    state.className = "summary " + summary.cls;
    const title = document.createElement("b");
    title.textContent = summary.title;
    const detail = document.createElement("small");
    detail.textContent = summary.sub;
    state.appendChild(title);
    state.appendChild(detail);
    head.appendChild(state);

    const chevron = document.createElement("span");
    chevron.className = "chev";
    chevron.innerHTML = '<svg width="12" height="12" viewBox="0 0 16 16" fill="none">' +
      '<path d="m3 6 5 5 5-5" stroke="currentColor" stroke-width="1.5" ' +
      'stroke-linecap="round"/></svg>';
    head.appendChild(chevron);
    head.addEventListener("click", function () {
      expanded[target] = !expanded[target];
      app.classList.toggle("open", expanded[target]);
      head.setAttribute("aria-expanded", expanded[target] ? "true" : "false");
      syncPanelCollapseButton();
    });
    app.appendChild(head);

    const accountList = document.createElement("div");
    accountList.className = "accounts";
    group.forEach(function (account) {
      accountList.appendChild(accountNode(account));
    });
    app.appendChild(accountList);
    box.appendChild(app);
  });
  syncPanelCollapseButton();

  const alert = document.getElementById("alert");
  alert.hidden = attention === 0;
  if (attention) {
    document.getElementById("alert-title").textContent = attention + " 个账号需要处理";
    document.getElementById("alert-detail").textContent =
      "登录失效或签到失败，请打开主窗口查看详情。";
  }
  updateFooter();
}

function showMessage(text, success) {
  const node = document.getElementById("message");
  node.textContent = text;
  node.className = "message" + (success ? " success" : "");
  node.hidden = false;
}

async function loadProviderInfo() {
  Object.keys(providerInfo).forEach(function (key) {
    delete providerInfo[key];
  });
  try {
    const view = asJSON(await Bound.ProvidersList());
    (view.providers || []).forEach(function (provider) {
      providerInfo[provider.id] = provider;
    });
  } catch (_) {}
}

// 供应商运行时是随包 Bun 进程，首次读取要冷启动；首次加载时先把状态行写成
// “加载中”，并在失败时做有限次自动重试，避免面板一直停在默认的空状态。
let panelLoadedOnce = false;
let panelRetryCount = 0;

function refresh() {
  if (refreshInFlight) return refreshInFlight;
  const button = document.getElementById("refresh");
  button.disabled = true;
  if (!panelLoadedOnce) {
    document.getElementById("checkin-updated").textContent = "加载中…";
  }
  refreshInFlight = (async function () {
    try {
      const status = asJSON(await Bound.Status());
      await loadProviderInfo();
      render(status);
      panelLoadedOnce = true;
      panelRetryCount = 0;
    } catch (_) {
      fillEmpty(
        document.getElementById("targets"),
        "加载失败",
        "供应商运行时尚未就绪，正在自动重试；也可打开主窗口检查供应商状态。"
      );
      if (!panelLoadedOnce && panelRetryCount < 5) {
        panelRetryCount += 1;
        window.setTimeout(function () { refresh(); }, 2500);
      }
    } finally {
      button.disabled = false;
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

function renderSparkline() {
  const sparkline = document.getElementById("panel-speed-sparkline");
  sparkline.innerHTML = "";
  const max = Math.max.apply(null, speedHistory.concat([1]));
  speedHistory.forEach(function (value, index) {
    const bar = document.createElement("i");
    const ratio = Math.max(0.08, value / max);
    bar.style.height = Math.round(ratio * 100) + "%";
    bar.classList.toggle("hot", index === speedHistory.length - 1 && value > 0);
    sparkline.appendChild(bar);
  });
}

function seedSpeedHistory(output) {
  if (speedHistorySeeded || output <= 0) return;
  const shape = [
    0.14, 0.23, 0.19, 0.38, 0.31, 0.52, 0.42, 0.67,
    0.54, 0.76, 0.62, 0.84, 0.71, 0.93, 0.80, 0.88,
    0.73, 0.96, 0.82, 1, 0.91, 0.97, 0.89, 1,
  ];
  shape.forEach(function (ratio, index) {
    speedHistory[index] = output * ratio;
  });
  speedHistorySeeded = true;
}

function renderSpeedDirectory(sources, sessions, idleCount) {
  const list = document.getElementById("panel-sources");
  list.innerHTML = "";
  const sourceRows = Array.isArray(sources) ? sources : [];
  const sessionRows = Array.isArray(sessions) ? sessions : [];
  document.getElementById("panel-source-count").textContent =
    sourceRows.length + " 个 Agent";
  document.getElementById("panel-session-count").textContent =
    sessionRows.length ? "最近 10 分钟 · " + sessionRows.length + " 个会话" : "暂无近期会话";
  if (!sourceRows.length && !sessionRows.length) {
    const empty = document.createElement("div");
    empty.className = "agent-empty";
    empty.textContent = "未发现本机 Agent 数据源";
    list.appendChild(empty);
    return;
  }

  const sessionsBySource = new Map();
  sessionRows.forEach(function (session) {
    const key = String(session.source || "");
    if (!sessionsBySource.has(key)) sessionsBySource.set(key, []);
    sessionsBySource.get(key).push(session);
  });

  function renderAgentHead(options) {
    const hasSessions = options.related.length > 0;
    const stateKey = options.stateKey;
    const expanded = hasSessions
      ? (agentExpansion.has(stateKey)
        ? agentExpansion.get(stateKey)
        : options.generating || options.related.some(function (session) {
          return !!session.generating;
        }))
      : false;
    const head = document.createElement(hasSessions ? "button" : "div");
    head.className = "agent-head" + (hasSessions ? " agent-toggle" : "");
    if (hasSessions) {
      head.type = "button";
      head.setAttribute("aria-expanded", expanded ? "true" : "false");
      head.setAttribute("aria-controls", options.sessionsId);
      head.setAttribute(
        "aria-label",
        (expanded ? "收起 " : "展开 ") + options.label + " 的 " +
          options.related.length + " 个会话"
      );
    }
    const identity = document.createElement("div");
    identity.className = "agent-identity";
    identity.appendChild(sourceLogoNode(options.logoKey));
    const copy = document.createElement("div");
    copy.className = "agent-copy";
    const name = document.createElement("div");
    name.className = "agent-name";
    if (options.generating) {
      const dot = document.createElement("i");
      dot.className = "source-live-dot active";
      name.appendChild(dot);
    } else if (options.showIdleDot) {
      const dot = document.createElement("i");
      dot.className = "source-live-dot";
      name.appendChild(dot);
    }
    const label = document.createElement("span");
    label.textContent = options.label;
    name.appendChild(label);
    const sub = document.createElement("div");
    sub.className = "agent-sub";
    sub.textContent = options.subText;
    copy.appendChild(name);
    copy.appendChild(sub);
    identity.appendChild(copy);
    head.appendChild(identity);

    const side = document.createElement("div");
    side.className = "agent-head-side";
    const rate = document.createElement("div");
    rate.className = "agent-rate";
    const strong = document.createElement("b");
    strong.textContent = compactTokenRate(options.rate);
    rate.appendChild(strong);
    rate.appendChild(document.createTextNode(" tok/s"));
    if (options.rateSub) {
      const rateSub = document.createElement("small");
      rateSub.textContent = options.rateSub;
      rate.appendChild(rateSub);
    }
    side.appendChild(rate);
    if (hasSessions) {
      const chevron = document.createElement("span");
      chevron.className = "agent-chevron";
      chevron.setAttribute("aria-hidden", "true");
      chevron.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
        '<path d="M3.4 5.8 8 10.2l4.6-4.4" stroke="currentColor" ' +
        'stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>';
      side.appendChild(chevron);
    }
    head.appendChild(side);

    const body = appendSessionRows(options.related, options.sessionsId);
    if (!hasSessions) body.hidden = true;
    if (hasSessions && !expanded) body.hidden = true;
    const toggle = function () {
      const next = head.getAttribute("aria-expanded") !== "true";
      agentExpansion.set(stateKey, next);
      head.setAttribute("aria-expanded", next ? "true" : "false");
      head.setAttribute(
        "aria-label",
        (next ? "收起 " : "展开 ") + options.label + " 的 " +
          options.related.length + " 个会话"
      );
      body.hidden = !next;
      const card = body.closest(".agent-card");
      if (card) card.classList.toggle("is-collapsed", !next);
    };
    return {
      head: head,
      body: body,
      expanded: expanded,
      hasSessions: hasSessions,
      toggle: toggle,
    };
  }

  function appendSessionRows(related, sessionsId) {
    const nested = document.createElement("div");
    nested.className = "agent-sessions";
    nested.id = sessionsId;
    related
      .slice()
      .sort(function (a, b) {
        return Number(!!b.generating) - Number(!!a.generating);
      })
      .forEach(function (session) {
        const item = document.createElement("div");
        item.className = "agent-session";
        const main = document.createElement("div");
        main.className = "agent-session-main";
        const sessionDot = document.createElement("i");
        sessionDot.className = "source-live-dot" +
          (session.generating ? " active" : "");
        main.appendChild(sessionDot);
        const sessionCopy = document.createElement("div");
        sessionCopy.className = "agent-session-copy";
        const sessionName = document.createElement("span");
        sessionName.className = "agent-session-name";
        sessionName.textContent = session.label || "会话";
        sessionName.title = [
          session.label || "会话",
          session.model || "",
        ].filter(Boolean).join(" · ");
        const meta = document.createElement("span");
        meta.className = "agent-session-meta";
        meta.textContent = [
          session.model || "模型未知",
          session.generating ? "生成中" : "最近活跃",
        ].join(" · ");
        sessionCopy.appendChild(sessionName);
        sessionCopy.appendChild(meta);
        main.appendChild(sessionCopy);
        item.appendChild(main);
        const sessionRate = document.createElement("span");
        sessionRate.className = "agent-session-rate";
        sessionRate.textContent =
          compactTokenRate(session.output_tokens_per_second) + " tok/s";
        item.appendChild(sessionRate);
        nested.appendChild(item);
      });
    return nested;
  }

  // 整行空白（含卡片内边距）都可折叠；会话明细行本身不触发折叠。
  function bindAgentCardToggle(row, rendered) {
    if (!rendered.hasSessions) return;
    row.addEventListener("click", function (event) {
      const target = event.target;
      if (!target || !target.closest) return;
      if (target.closest(".agent-sessions")) return;
      rendered.toggle();
    });
  }

  sourceRows.forEach(function (source) {
    const sourceKey = String(source.source || "");
    const related = sessionsBySource.get(sourceKey) || [];
    sessionsBySource.delete(sourceKey);
    const row = document.createElement("article");
    row.className = "agent-card" +
      (Number(source.generating_agents) > 0 ? " generating" : "");
    const sessionsId = "agent-sessions-" + sourceKey.replace(/[^a-z0-9_-]/gi, "-");
    const rendered = renderAgentHead({
      stateKey: sourceKey,
      sessionsId: sessionsId,
      logoKey: source.source || source.label,
      label: source.label || source.source || "Agent",
      subText: "今日 " + compactTokenCount(Number(source.today_total_tokens) || 0) +
        (related.length ? " · " + related.length + " 个会话" : ""),
      rate: source.output_tokens_per_second,
      rateSub: Number(source.generating_agents) > 0 ? "正在生成" :
        (Number(source.today_total_tokens) > 0 ? "今日已用" : "待使用"),
      generating: Number(source.generating_agents) > 0,
      showIdleDot: true,
      related: related,
    });
    row.appendChild(rendered.head);
    row.appendChild(rendered.body);
    bindAgentCardToggle(row, rendered);
    if (!rendered.expanded && related.length) row.classList.add("is-collapsed");
    list.appendChild(row);
  });

  sessionsBySource.forEach(function (orphanSessions, sourceKey) {
    const row = document.createElement("article");
    row.className = "agent-card";
    const generating = orphanSessions.some(function (item) { return !!item.generating; });
    const sessionsId = "agent-sessions-orphan-" +
      sourceKey.replace(/[^a-z0-9_-]/gi, "-");
    const rendered = renderAgentHead({
      stateKey: sourceKey,
      sessionsId: sessionsId,
      logoKey: sourceKey,
      label: orphanSessions[0] && orphanSessions[0].source_label || "其他 Agent",
      subText: orphanSessions.length + " 个近期会话",
      rate: orphanSessions.reduce(function (total, item) {
      return total + (Number(item.output_tokens_per_second) || 0);
      }, 0),
      rateSub: generating ? "正在生成" : "近期活跃",
      generating: generating,
      showIdleDot: false,
      related: orphanSessions,
    });
    row.appendChild(rendered.head);
    row.appendChild(rendered.body);
    bindAgentCardToggle(row, rendered);
    if (!rendered.expanded) row.classList.add("is-collapsed");
    list.appendChild(row);
  });

  // 今天没用过的 Agent 不再各占一张空卡片，只在末尾用一行提示数量。
  if (!list.children.length) {
    const empty = document.createElement("p");
    empty.className = "panel-sources-empty";
    empty.textContent = "今天还没有 Agent 使用记录，开始对话后这里会出现";
    list.appendChild(empty);
  } else if (Number(idleCount) > 0) {
    const hint = document.createElement("p");
    hint.className = "panel-sources-empty";
    hint.textContent = "另有 " + Number(idleCount) + " 个已配置的 Agent 今天未使用";
    list.appendChild(hint);
  }
}

function formatUpdated(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function animateOutput(target) {
  if (animationFrame) cancelAnimationFrame(animationFrame);
  const reduced = window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!speedSeen || reduced) {
    animatedOutput = target;
    document.getElementById("panel-speed-output").textContent = compactTokenRate(target);
    speedSeen = true;
    return;
  }
  const startValue = animatedOutput;
  const started = performance.now();
  function step(now) {
    const progress = Math.min(1, (now - started) / 360);
    const eased = 1 - Math.pow(1 - progress, 3);
    animatedOutput = startValue + (target - startValue) * eased;
    document.getElementById("panel-speed-output").textContent =
      compactTokenRate(animatedOutput);
    if (progress < 1) animationFrame = requestAnimationFrame(step);
    else animationFrame = 0;
  }
  animationFrame = requestAnimationFrame(step);
}

function renderSpeed(raw) {
  const view = raw ? asJSON(raw) : null;
  const card = document.getElementById("panel-speed");
  const output = Number(view && view.output_tokens_per_second) || 0;
  const previousOutput = latestSpeedOutput;
  latestSpeedOutput = output;
  latestSpeedActive = !!(view && view.active);
  latestTodayTokens = Math.max(0, Number(view && view.today_total_tokens) || 0);
  card.classList.toggle("active", latestSpeedActive);

  if (!view) {
    document.getElementById("panel-speed-state").textContent = "速度读取失败";
    document.getElementById("panel-speed-title").textContent = "待机";
    document.getElementById("panel-speed-theme").textContent = "交通工具";
    document.getElementById("panel-speed-updated").textContent = "—";
    document.getElementById("panel-speed-agents").textContent = "0";
    document.getElementById("panel-speed-today").textContent = "0";
    document.getElementById("panel-source-count").textContent = "0 个 Agent";
    document.getElementById("panel-session-count").textContent = "暂无近期会话";
    renderPanelTitle(null);
    renderSpeedDirectory([], []);
    const badge = document.getElementById("speed-badge");
    badge.hidden = true;
    speedHistory.shift();
    speedHistory.push(0);
    renderSparkline();
    animateOutput(0);
    updatePetForDailyTokens(0);
    return;
  }

  const generating = Number(view.generating_agents) || 0;
  document.getElementById("panel-speed-state").textContent =
    generating > 0 ? "生成中 " + generating :
      (view.active ? "最近一次响应已完成解码" : "当前空闲");
  document.getElementById("panel-speed-updated").textContent =
    formatUpdated(view.updated_at);
  document.getElementById("panel-speed-title").textContent = view.title || "待机";
  document.getElementById("panel-speed-theme").textContent =
    view.title_theme || "交通工具";
  document.getElementById("panel-speed-agents").textContent = String(generating);
  document.getElementById("panel-speed-today").textContent =
    compactTokenCount(Number(view.today_total_tokens) || 0);
  renderPanelTitle(view);
  renderSpeedDirectory(view.sources || [], view.sessions || [],
    Number(view.idle_source_count) || 0);
  const badge = document.getElementById("speed-badge");
  badge.hidden = output <= 0 && generating === 0;
  badge.textContent = generating > 0 ? String(generating) : compactTokenRate(output);
  if (output > previousOutput + 0.5 && latestSpeedActive) {
    card.classList.remove("rate-bump");
    // 双 rAF 等下一帧再挂 class：不用 void offsetWidth 强制同步 reflow。
    requestAnimationFrame(function () {
      requestAnimationFrame(function () {
        if (latestSpeedActive) card.classList.add("rate-bump");
      });
    });
    // 监听只挂在真正触发动画的路径上：此前每次 renderSpeed 都注册一个
    // animationend（无动画时永不触发），空闲一整天会累积数万个闭包。
    card.addEventListener("animationend", function () {
      card.classList.remove("rate-bump");
    }, { once: true });
  }
  seedSpeedHistory(output);
  speedHistory.shift();
  speedHistory.push(output);
  renderSparkline();
  animateOutput(output);
  updatePetForDailyTokens(latestTodayTokens);
  updateFooter();
}

async function refreshSpeed() {
  try {
    renderSpeed(await Bound.TokenSpeedGet());
  } catch (_) {
    renderSpeed(null);
  }
}

function updateFooter() {
  const count = document.getElementById("count");
  const run = document.getElementById("run");
  // 签到页不再保留全局「巡检」按钮：自动执行每 2 小时跑一次，账号卡里也有
  // 立即签到/立即活跃，这个入口属于重复且容易误触。速度页同样不需要按钮；
  // 只有宠物页把同一个按钮复用为「放技能」。
  if (activeTab === "checkin") {
    count.innerHTML = "<b>" + currentAccounts.length + "</b> 个账号";
    run.hidden = true;
    return;
  }
  if (activeTab === "speed") {
    count.textContent = "自动刷新";
    run.hidden = true;
    return;
  }
  if (activeTab === "pet") {
    const tier = currentSkillTier();
    count.innerHTML = "<b>" + romanStage(tier) + "</b> / III 技能";
    run.hidden = false;
    run.textContent = "放技能";
    run.setAttribute("aria-label", "投出精灵球或放出伙伴");
    return;
  }
  run.hidden = true;
}

function currentPet() {
  return pokemonById(selectedPetId) || POKEMON_CATALOG[0];
}

let panelTrayPetId = "";
let panelTrayCaught = null;
function syncPanelTrayIcon() {
  if (panelTrayPetId === selectedPetId) return;
  panelTrayPetId = selectedPetId;
  try {
    Events.Emit("pet-selected", { pet: selectedPetId });
  } catch (_) {}
}

function syncPanelTrayCaught() {
  const caught = panelPetIsCaught(selectedPetId);
  if (panelTrayCaught === caught) return;
  panelTrayCaught = caught;
  try {
    Events.Emit("pet-caught", { caught: caught });
  } catch (_) {}
}

function currentSkillTier() {
  return evolutionStageForDailyTokens(latestTodayTokens);
}

function romanStage(stage) {
  return ["I", "II", "III"][Math.max(0, Math.min(2, stage - 1))];
}

function evolutionThreshold(stage) {
  return POKEMON_EVOLUTION_THRESHOLDS[Math.max(0, Math.min(2, stage - 1))];
}

function petEffectIntensity() {
  const tier = currentSkillTier();
  if (tier < 3) return tier;
  const strengthened = Math.max(
    0,
    Math.min(
      1,
      (latestTodayTokens - POKEMON_EVOLUTION_THRESHOLDS[2]) /
        Math.max(1, POKEMON_MAX_DAILY_TOKENS - POKEMON_EVOLUTION_THRESHOLDS[2])
    )
  );
  return 3 + Math.round(strengthened * 2);
}

function ensurePetViewer(pet) {
  const avatar = document.getElementById("pet-avatar");
  if (petViewer && petViewerPetId === pet.id) {
    petViewer.setEffectIntensity(petEffectIntensity());
    return;
  }

  if (petViewer) petViewer.dispose();
  petViewer = null;
  petViewerPetId = pet.id;
  avatar.innerHTML =
    '<canvas class="pokemon-3d-canvas" aria-hidden="true"></canvas>' + pet.art;
  avatar.className = "pet-avatar pet-" + pet.id + " is-loading";
  const canvas = avatar.querySelector(".pokemon-3d-canvas");
  petViewer = createPokemon3D(canvas, {
    models: pet.stages.map(function (entry) { return entry.model; }),
    boneAdjust: pet.stages[0].pose || null,
    eyeMaterial: pet.eyeMaterial || null,
    eyeMesh: pet.eyeMesh || null,
    modelRotations: pet.stages.map(function (entry) { return entry.rotation || null; }),
    stage: 1,
    enabled: activeTab === "pet",
    effectIntensity: petEffectIntensity(),
    onLoading: function (loading) {
      if (petViewerPetId === pet.id) {
        avatar.classList.toggle("is-loading", loading !== false);
      }
    },
    onReady: function () {
      if (petViewerPetId !== pet.id) return;
      avatar.classList.remove("is-loading", "model-fallback");
      avatar.classList.add("is-3d");
      if (latestPointer.x !== null && latestPointer.y !== null) {
        applyGazeAtClient(latestPointer.x, latestPointer.y);
      }
    },
    onUnavailable: function () {
      if (petViewerPetId !== pet.id) return;
      avatar.classList.remove("is-3d", "is-loading");
      avatar.classList.add("model-fallback");
    },
    onError: function () {
      if (petViewerPetId !== pet.id) return;
      avatar.classList.remove("is-3d", "is-loading");
      avatar.classList.add("model-fallback");
    },
  });
  if (petViewer && !petViewer.isCapturing()) {
    petViewer.setCaught(panelPetIsCaught(pet.id) || petViewer.isCaught(), false);
  }
}

function renderPetAvatar(pet) {
  const avatar = document.getElementById("pet-avatar");
  const stageNode = document.getElementById("pet-stage");
  ensurePetViewer(pet);
  if (stageNode) stageNode.classList.toggle("is-caught", panelPetIsCaught(pet.id));
  avatar.dataset.renderedPet = pet.id;
  avatar.dataset.evolution = "1";
  if (latestPointer.x !== null && latestPointer.y !== null) {
    requestAnimationFrame(function () {
      applyGazeAtClient(latestPointer.x, latestPointer.y);
    });
  }
}

function renderPet() {
  const pet = currentPet();
  const tier = currentSkillTier();
  const previousId = renderedPetId;
  const previousTier = renderedSkillTier;
  const changedTier = previousId === pet.id && previousTier > 0 &&
    tier !== previousTier;

  selectedPetId = pet.id;
  syncPanelTrayIcon();
  syncPanelTrayCaught();
  const stageNode = document.getElementById("pet-stage");
  stageNode.dataset.pet = pet.id;
  stageNode.dataset.evolution = "1";
  stageNode.style.setProperty("--pet-accent", pet.accent);
  renderPetAvatar(pet);
  renderedPetId = pet.id;
  renderedSkillTier = tier;

  document.getElementById("pet-name").textContent = pet.name;
  document.getElementById("pet-subtitle").textContent = pet.subtitle;
  document.getElementById("pet-current-speed").innerHTML =
    compactTokenRate(latestSpeedOutput) + " <small>tok/s</small>";
  document.getElementById("pet-today-tokens").textContent =
    compactTokenCount(latestTodayTokens);
  document.getElementById("pet-level").innerHTML =
    romanStage(tier) + "<small>技能等级</small>";
  document.getElementById("pet-stage-label").innerHTML =
    romanStage(tier) + " <small>/ III 技能</small>";

  const nextTier = tier < 3 ? tier + 1 : 0;
  if (nextTier) {
    const from = evolutionThreshold(tier);
    const to = evolutionThreshold(nextTier);
    const progress = Math.max(
      3,
      Math.min(100, (latestTodayTokens - from) / Math.max(1, to - from) * 100)
    );
    document.getElementById("pet-progress-title").textContent =
      "下一技能等级 · " + romanStage(nextTier);
    document.getElementById("pet-progress-copy").textContent =
      compactTokenCount(latestTodayTokens) + " / " + compactTokenCount(to) + " Token";
    document.getElementById("pet-progress").style.width = progress + "%";
  } else {
    const capped = latestTodayTokens >= POKEMON_MAX_DAILY_TOKENS;
    const progress = capped
      ? 100
      : Math.max(
        5,
        Math.min(
          100,
          (latestTodayTokens - POKEMON_EVOLUTION_THRESHOLDS[2]) /
            Math.max(1, POKEMON_MAX_DAILY_TOKENS - POKEMON_EVOLUTION_THRESHOLDS[2]) *
            100
        )
      );
    document.getElementById("pet-progress-title").textContent =
      capped ? "技能特效已达顶峰" : "技能特效强化中";
    document.getElementById("pet-progress-copy").textContent =
      compactTokenCount(latestTodayTokens) + " / " +
      compactTokenCount(POKEMON_MAX_DAILY_TOKENS) + " Token";
    document.getElementById("pet-progress").style.width = progress + "%";
  }

  document.querySelectorAll("button[data-pet-id]").forEach(function (button) {
    const candidate = petCatalog.find(function (entry) {
      return entry.id === button.dataset.petId;
    });
    const current = !!candidate && candidate.id === pet.id;
    button.disabled = false;
    button.classList.toggle("unlocked", !!candidate);
    button.classList.toggle("current", current);
    button.setAttribute("aria-pressed", current ? "true" : "false");
    const badge = document.createElement("span");
    badge.className = "pet-unlock-lock";
    badge.textContent = current ? "使用中" : "切换";
    button.replaceChildren(
      document.createTextNode((candidate && candidate.name) || "伙伴"),
      badge
    );
  });
  updateFooter();
  schedulePanelAutoSkill();
  return { pet, tier, previousTier, changedTier };
}

// applyGazeAtClient 用指针的屏幕坐标驱动眼神：指针在面板任意位置都有效，
// 舞台外会钳到最近边缘（朝那个方向看），不会因为指针离开舞台而突然回正。
function applyGazeAtClient(clientX, clientY) {
  const stage = document.getElementById("pet-stage");
  const avatar = document.getElementById("pet-avatar");
  if (!stage || !avatar) return;
  const stageRect = stage.getBoundingClientRect();
  const avatarRect = avatar.getBoundingClientRect();
  const aim = aimForClientPoint(clientX, clientY, stageRect, avatarRect);
  if (!aim) {
    resetPetGaze();
    return;
  }
  const nx = aim.x;
  const ny = aim.y;
  stage.style.setProperty("--pet-look-x", (nx * 6.4).toFixed(1) + "px");
  stage.style.setProperty("--pet-look-y", (ny * 4.2).toFixed(1) + "px");
  stage.style.setProperty("--pet-look-roll", (nx * 2.2).toFixed(1) + "deg");
  if (petViewer) petViewer.setPointer(nx, ny);
}

function resetPetGaze() {
  const stage = document.getElementById("pet-stage");
  if (!stage) return;
  stage.style.setProperty("--pet-look-x", "0px");
  stage.style.setProperty("--pet-look-y", "0px");
  stage.style.setProperty("--pet-look-roll", "0deg");
  if (petViewer) petViewer.resetPointer();
}

function updatePetForDailyTokens(total) {
  const previousTier = renderedSkillTier;
  latestTodayTokens = Math.max(0, Number(total) || 0);
  const rendered = renderPet();
  if (rendered.changedTier && previousTier > 0) {
    const pet = rendered.pet;
    speakPet(rendered.tier > previousTier
      ? "今日 Token 达到 " + compactTokenCount(evolutionThreshold(rendered.tier)) +
        "，「" + pet.name + "」技能提升到 " + romanStage(rendered.tier) + " 级。"
      : "今日 Token 重置，「" + pet.name + "」技能回到 " +
        romanStage(rendered.tier) + " 级。");
  }
  return rendered;
}

function selectPet(petId) {
  const pet = pokemonById(petId);
  if (!pet) return;
  selectedPetId = pet.id;
  syncPanelTrayIcon();
  syncPanelTrayCaught();
  writeStoredValue(petSelectedStorageKey, selectedPetId);
  latestPointer.x = null;
  latestPointer.y = null;
  resetPetGaze();
  clearPetSkill();
  renderPet();
  speakPet("轮到「" + pet.name + "」啦，技能等级 " + romanStage(currentSkillTier()));
}

document.querySelectorAll("button[data-pet-id]").forEach(function (button) {
  button.addEventListener("click", function () {
    selectPet(button.dataset.petId);
  });
});

function clampSkillPoint(value, max) {
  return Math.max(8, Math.min(max - 8, Number(value) || 0));
}

function skillZigzagPath(x1, y1, x2, y2, power) {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const length = Math.max(1, Math.hypot(dx, dy));
  const px = -dy / length;
  const py = dx / length;
  const segments = 7 + power;
  const points = [];
  for (let index = 0; index <= segments; index += 1) {
    const t = index / segments;
    const taper = Math.sin(Math.PI * t);
    const jitter = index === 0 || index === segments
      ? 0
      : (index % 2 ? 1 : -1) * (2.8 + power * .75) * taper;
    points.push([
      x1 + dx * t + px * jitter,
      y1 + dy * t + py * jitter,
    ]);
  }
  return "M" + points.map(function (point) {
    return point[0].toFixed(1) + " " + point[1].toFixed(1);
  }).join(" L");
}

// 实心技能形状：火焰不再是「粗描边直线」，而是带尖端与宽度的火舌轮廓；
// 水同理是锥形水柱。两者都从宠物身上指向落点，power 决定宽度/层数。
function skillFlameTonguePath(x1, y1, x2, y2, scale) {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const length = Math.max(1, Math.hypot(dx, dy));
  const nx = -dy / length;
  const ny = dx / length;
  const half = 12 * scale;
  const tipX = x2 + dx / length * 10;
  const tipY = y2 + dy / length * 10;
  const mx = x1 + dx * .55;
  const my = y1 + dy * .55;
  const f = function (n) { return n.toFixed(1); };
  return "M" + f(x1) + " " + f(y1) +
    " Q" + f(x1 + dx * .18 + nx * half) + " " + f(y1 + dy * .18 + ny * half) +
    " " + f(mx + nx * half * .8) + " " + f(my + ny * half * .8) +
    " Q" + f(tipX + nx * half * .3) + " " + f(tipY + ny * half * .3) +
    " " + f(tipX) + " " + f(tipY) +
    " Q" + f(tipX - nx * half * .3) + " " + f(tipY - ny * half * .3) +
    " " + f(mx - nx * half * .8) + " " + f(my - ny * half * .8) +
    " Q" + f(x1 + dx * .18 - nx * half) + " " + f(y1 + dy * .18 - ny * half) +
    " " + f(x1) + " " + f(y1) + " Z";
}

function skillJetConePath(x1, y1, x2, y2, scale) {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const length = Math.max(1, Math.hypot(dx, dy));
  const nx = -dy / length;
  const ny = dx / length;
  const w0 = 6 * scale;
  const w1 = 15 * scale;
  const tx = x2 + dx / length * 8;
  const ty = y2 + dy / length * 8;
  const f = function (n) { return n.toFixed(1); };
  return "M" + f(x1 + nx * w0) + " " + f(y1 + ny * w0) +
    " L" + f(tx + nx * w1) + " " + f(ty + ny * w1) +
    " L" + f(tx - nx * w1) + " " + f(ty - ny * w1) +
    " L" + f(x1 - nx * w0) + " " + f(y1 - ny * w0) + " Z";
}

function petSkillSvg(pet, targetX, targetY, rect, power) {
  const width = Math.max(1, rect.width);
  const height = Math.max(1, rect.height);
  const f = function (n) { return n.toFixed(1); };
  const cx = width * .5;
  const cy = height * .52;
  const effects = [];
  const count = pet.effect === "leaf" ? 12 + power : 9 + power;

  // 背景光爆：放射星芒 + 中心辉光，把宠物与粒子包成一个整体画面。
  const rays = [];
  for (let index = 0; index < 12; index += 1) {
    const angle = (index / 12) * Math.PI * 2;
    const angle2 = angle + Math.PI / 12 * .62;
    const R = Math.min(width, height) * .42 * (index % 2 ? .78 : 1.12);
    rays.push('M' + f(cx) + ' ' + f(cy) +
      ' L' + f(cx + Math.cos(angle - .06) * R) + ' ' + f(cy + Math.sin(angle - .06) * R) +
      ' Q' + f(cx + Math.cos(angle) * R * .72) + ' ' + f(cy + Math.sin(angle) * R * .72) +
      ' ' + f(cx + Math.cos(angle2) * R * .42) + ' ' + f(cy + Math.sin(angle2) * R * .42) + ' Z');
  }
  effects.push('<g class="skill-burst-rays">' + rays.map(function (d) {
    return '<path d="' + d + '"></path>';
  }).join('') + '</g>');
  effects.push('<ellipse class="skill-burst-halo" cx="' + f(cx) + '" cy="' + f(cy) +
    '" rx="' + f(Math.min(width, height) * .38) + '" ry="' + f(Math.min(width, height) * .32) + '"></ellipse>');

  // 统一的周身分布：四系共用同一个「半透明、环绕、向上飘」的构图，
  // 内容各是各的（火苗 / 电光 / 水泡 / 树叶），不遮挡立绘本体。
  for (let index = 0; index < count; index += 1) {
    const angle = index * 2.399 + .4;
    const radius = Math.min(width, height) * (.2 + (index % 4) * .065);
    const x = cx + Math.cos(angle) * radius * 1.15;
    const y = cy + Math.sin(angle) * radius * .82;
    const delay = index * 52;
    const front = index % 3 === 0;
    const style = '--dx:' + (((index * 13) % 18) - 9) + 'px;' +
      '--dy:' + (-20 - (index % 3) * 8) + 'px;animation-delay:' + delay + 'ms';
    let shape = '';

    if (pet.effect === "ember") {
      // 小火龙：一簇小簇火苗（半透明，不糊原图）
      const s = 7 + (index % 3) * 2.2;
      const outer = 'M' + f(x - s * .5) + ' ' + f(y) +
        ' Q' + f(x - s * .55) + ' ' + f(y - s * .75) + ' ' + f(x - s * .1) + ' ' + f(y - s * 1.25) +
        ' Q' + f(x + s * .05) + ' ' + f(y - s * 1.75) + ' ' + f(x + s * .16) + ' ' + f(y - s * 2.1) +
        ' Q' + f(x + s * .3) + ' ' + f(y - s * 1.2) + ' ' + f(x + s * .42) + ' ' + f(y - s * .72) +
        ' Q' + f(x + s * .55) + ' ' + f(y - s * .25) + ' ' + f(x + s * .5) + ' ' + f(y) + ' Z';
      const inner = 'M' + f(x - s * .24) + ' ' + f(y) +
        ' Q' + f(x - s * .28) + ' ' + f(y - s * .6) + ' ' + f(x + s * .05) + ' ' + f(y - s * 1.15) +
        ' Q' + f(x + s * .3) + ' ' + f(y - s * .55) + ' ' + f(x + s * .24) + ' ' + f(y) + ' Z';
      shape = '<g class="skill-float skill-flame-stream" style="' + style + '" data-depth="' + (front ? "front" : "back") + '">' +
        '<path class="outer" d="' + outer + '"></path><path class="inner" d="' + inner + '"></path></g>';
    } else if (pet.effect === "leaf") {
      // 妙蛙种子：半透明小叶片。外层 <g> 只做定位，内层 .skill-float 才做 CSS 动画：
      // 否则 animation 的 transform 会覆盖 translate，叶片全堆在原点看不见。
      const s = 13 + (index % 3) * 3;
      const d = 'M0 0 Q' + f(s * .6) + ' ' + f(-s * .85) + ' ' + f(s * 1.35) + ' ' + f(-s * .15) +
        ' Q' + f(s * .6) + ' ' + f(s * .7) + ' 0 0 Z';
      shape = '<g transform="translate(' + f(x) + ' ' + f(y) + ')">' +
        '<g class="skill-float" style="' + style + '" data-depth="' + (front ? "front" : "back") + '">' +
        '<g transform="rotate(' + (index * 37 - 20) + ')">' +
        '<path class="skill-vine" d="' + d + '"></path>' +
        '<path class="vein" d="M0 0 L' + f(s * 1.1) + ' ' + f(-s * .2) + '"></path>' +
        '</g></g></g>';
    } else if (pet.effect === "water") {
      // 杰尼龟：半透明水泡（基准样式）
      const r = 6 + (index % 4) * 3.4;
      shape = '<g class="skill-float skill-water-stream" style="' + style + '" data-depth="' + (front ? "front" : "back") + '">' +
        '<circle cx="' + f(x) + '" cy="' + f(y) + '" r="' + f(r) + '"></circle>' +
        '<circle class="glint" cx="' + f(x - r * .35) + '" cy="' + f(y - r * .38) + '" r="' + f(r * .22) + '"></circle></g>';
    } else {
      // 皮卡丘：周身短电弧
      const ex = x + Math.cos(angle) * 18;
      const ey = y + Math.sin(angle) * 18;
      const mx = x + Math.cos(angle + .5) * 9;
      const my = y + Math.sin(angle + .5) * 9;
      shape = '<path class="skill-line skill-bolt" style="animation-delay:' + delay + 'ms" ' +
        'd="M' + f(x) + ' ' + f(y) + ' L' + f(mx) + ' ' + f(my) + ' L' + f(ex) + ' ' + f(ey) + '"></path>';
    }
    effects.push(shape);
  }
  // 皮卡丘再加一圈落点电光球，把周身电弧收成一个整体。
  if (pet.effect === "electric") {
    effects.push('<circle class="skill-arc-ball" cx="' + f(cx) + '" cy="' + f(cy) +
      '" r="' + f(Math.min(width, height) * .24) + '"></circle>');
  }

  const defs = '<defs>' +
    '<radialGradient id="skill-grad-halo"><stop offset="0" stop-color="rgba(255,255,255,.9)"/>' +
      '<stop offset=".45" stop-color="rgba(255,190,80,.5)"/><stop offset="1" stop-color="rgba(255,255,255,0)"/></radialGradient>' +
    '<linearGradient id="skill-grad-rays" x1="0" y1="0" x2="1" y2="1">' +
      '<stop offset="0" stop-color="rgba(255,255,255,.85)"/><stop offset="1" stop-color="rgba(255,255,255,0)"/></linearGradient>' +
    '<linearGradient id="skill-grad-fire" x1="0" y1="1" x2="0" y2="0">' +
      '<stop offset="0" stop-color="#ff6a18"/><stop offset=".5" stop-color="#ffab35"/><stop offset="1" stop-color="#ffe799"/></linearGradient>' +
    '<linearGradient id="skill-grad-fire-inner" x1="0" y1="1" x2="0" y2="0">' +
      '<stop offset="0" stop-color="#ffe07a"/><stop offset="1" stop-color="#fff8d8"/></linearGradient>' +
    '<linearGradient id="skill-grad-leaf" x1="0" y1="1" x2="1" y2="0">' +
      '<stop offset="0" stop-color="#4ea56a"/><stop offset="1" stop-color="#c8f0a4"/></linearGradient>' +
    '<radialGradient id="skill-grad-bubble"><stop offset="0" stop-color="rgba(255,255,255,.5)"/>' +
      '<stop offset=".65" stop-color="rgba(140,220,250,.22)"/><stop offset="1" stop-color="rgba(210,245,255,.65)"/></radialGradient>' +
    '<radialGradient id="skill-grad-bolt"><stop offset="0" stop-color="#fffde7"/>' +
      '<stop offset=".55" stop-color="#ffe45f"/><stop offset="1" stop-color="rgba(255,210,50,0)"/></radialGradient>' +
    '</defs>';
  return '<svg class="pet-skill-vector is-' + pet.effect + '" viewBox="0 0 ' +
    width.toFixed(1) + " " + height.toFixed(1) +
    '" preserveAspectRatio="none" aria-hidden="true">' + defs + effects.join("") + "</svg>";
}

function clearPetSkill() {
  const stage = document.getElementById("pet-stage");
  const layer = document.getElementById("pet-skill-layer");
  const particles = document.getElementById("pet-particles");
  if (layer) layer.replaceChildren();
  if (particles) particles.replaceChildren();
  if (!stage) return;
  stage.classList.remove(
    "skill-leaf", "skill-ember", "skill-water", "skill-electric", "playing"
  );
}

function spawnPetEffect(pet, x, y, rect, options) {
  const ultimate = !!(options && options.ultimate);
  const stage = document.getElementById("pet-stage");
  const layer = document.getElementById("pet-skill-layer");
  const particles = document.getElementById("pet-particles");
  particles.replaceChildren();
  layer.innerHTML = "";
  stage.classList.remove("skill-leaf", "skill-ember", "skill-water", "skill-electric");
  void stage.offsetWidth;
  stage.classList.add("skill-" + pet.effect);

  const counts = { leaf: 12, ember: 16, water: 14, electric: 12 };
  const power = ultimate ? 5 : petEffectIntensity();
  stage.style.setProperty("--fx-power", String(power));
  const targetX = clampSkillPoint(x, rect.width);
  const targetY = clampSkillPoint(y, rect.height);
  layer.innerHTML = petSkillSvg(pet, targetX, targetY, rect, power);

  const origins = {
    leaf: [rect.width * .5, rect.height * .72],
    ember: [rect.width * .5, rect.height * .47],
    water: [rect.width * .5, rect.height * .61],
    electric: [rect.width * .5, rect.height * .48],
  };
  const origin = origins[pet.effect] || [rect.width * .5, rect.height * .55];
  const baseCount = (counts[pet.effect] || 12) * (0.72 + power * 0.28);
  const count = Math.round(baseCount * (ultimate ? 1.9 : 1));
  for (let index = 0; index < count; index++) {
    const particle = document.createElement("i");
    particle.className = "pet-particle fx-" + pet.effect;
    const travel = .48 + (index % 5) * .07;
    particle.style.left =
      (origin[0] + (targetX - origin[0]) * travel).toFixed(1) + "px";
    particle.style.top =
      (origin[1] + (targetY - origin[1]) * travel).toFixed(1) + "px";
    particle.style.setProperty("--particle-accent", pet.accent);
    const angle = index / count * Math.PI * 2 + (index % 3) * 0.16;
    const distance = (18 + power * 4 + (index % 5) * 8) * (ultimate ? 1.45 : 1);
    particle.style.setProperty("--dx", Math.cos(angle) * distance + "px");
    particle.style.setProperty("--dy", Math.sin(angle) * distance + "px");
    particle.style.setProperty("--spin", (index % 2 ? -1 : 1) * (45 + index * 9) + "deg");
    particle.addEventListener("animationend", function () {
      particle.remove();
    }, { once: true });
    particles.appendChild(particle);
  }
  window.setTimeout(function () {
    stage.classList.remove("skill-leaf", "skill-ember", "skill-water", "skill-electric");
    layer.replaceChildren();
  }, 760 + power * 110);
}

// 捕获交互（与 Windows 迷你伙伴同一套手势）：轻点放技能、按住 ≥620ms 蓄力
// 松手投球；已捕获时点一下放出伙伴。结果用美漫风横幅给。
// 已捕获状态按宠物 id 存在 localStorage，和主窗口共用同一份记录。
const PET_CAUGHT_STORAGE_KEY = "qka.pet.caught.v1";
let petPressStart = 0;
let petChargeFrame = 0;
let petCharging = false;
let petPopupTimer = 0;

function showPetPopup(text, kind) {
  const popup = document.getElementById("pet-popup");
  const label = document.getElementById("pet-popup-text");
  if (!popup || !label) return;
  label.textContent = text;
  popup.className = "pet-popup" + (kind === "miss" ? " miss" : "");
  popup.hidden = false;
  // 重播入场动画：先清掉再强制回流一次。
  popup.style.animation = "none";
  void popup.offsetWidth;
  popup.style.animation = "";
  if (petPopupTimer) window.clearTimeout(petPopupTimer);
  petPopupTimer = window.setTimeout(function () {
    popup.hidden = true;
    petPopupTimer = 0;
  }, 2200);
}

function readPetCaughtMap() {
  try {
    const raw = localStorage.getItem(PET_CAUGHT_STORAGE_KEY);
    const parsed = raw ? JSON.parse(raw) : null;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch (_) {
    return {};
  }
}

function panelPetIsCaught(petId) {
  return !!readPetCaughtMap()[petId || selectedPetId];
}

function writePanelPetCaught(petId, caught) {
  const map = readPetCaughtMap();
  if (caught) map[petId] = true;
  else delete map[petId];
  try {
    localStorage.setItem(PET_CAUGHT_STORAGE_KEY, JSON.stringify(map));
  } catch (_) {}
}

function spawnPanelShockwave(count) {
  const stage = document.getElementById("pet-stage");
  if (!stage) return;
  const total = Math.max(1, Math.min(3, Number(count) || 1));
  for (let index = 0; index < total; index += 1) {
    const ring = document.createElement("span");
    ring.className = "pet-shockwave" + (index > 0 ? " second" : "");
    stage.appendChild(ring);
    ring.addEventListener("animationend", function () { ring.remove(); });
  }
}

// 技能演出：轻点宠物与自动释放共用同一套（随机目标点 + 2D 技能层 + 3D 特效）。
function playPanelSkill() {
  const pet = currentPet();
  const stage = document.getElementById("pet-stage");
  if (!stage) return;
  const rect = stage.getBoundingClientRect();
  const targetX = rect.width * (0.5 + (Math.random() - 0.5) * 0.46);
  const targetY = rect.height * (0.3 + Math.random() * 0.24);
  const avatar = document.getElementById("pet-avatar");
  if (avatar) {
    avatar.classList.remove("pounce", "ultimate");
    void avatar.offsetWidth;
    avatar.classList.add("pounce");
  }
  const power = petEffectIntensity();
  if (petViewer) petViewer.playSkill(pet.effect, power);
  // 档位越高圈越多：与 3D 粒子一起把「这次更强」讲清楚。
  spawnPanelShockwave(power >= 4 ? 3 : power >= 2 ? 2 : 1);
  const bubble = document.getElementById("pet-bubble");
  if (bubble) bubble.classList.remove("is-ultimate");
  speakPet(pet.messages[petMessageIndex++ % pet.messages.length]);
  spawnPetEffect(pet, targetX, targetY, rect);
  stage.classList.remove("playing");
  void stage.offsetWidth;
  stage.classList.add("playing");
}

// 自动释放（按 Token 档位定时）保留原入口名：轻点与自动走同一套演出。
function spawnPanelAutoSkill() {
  playPanelSkill();
}

function panelChargeUi(sweep) {
  const stage = document.getElementById("pet-stage");
  const gauge = document.getElementById("pet-gauge");
  const fill = document.getElementById("pet-charge-fill");
  const needle = document.getElementById("pet-charge-needle");
  const bubble = document.getElementById("pet-bubble");
  const position = Math.max(0, Math.min(1, Number(sweep.position) || 0));
  const perfect = Math.abs(position - CAPTURE_SWEEP.perfectCenter) <=
    CAPTURE_SWEEP.perfectHalfWidth;
  const percent = position * 100;
  if (stage) {
    stage.style.setProperty("--pet-charge", String(position));
    stage.style.setProperty("--pet-charge-pct", percent.toFixed(1) + "%");
  }
  if (fill) fill.style.width = percent.toFixed(1) + "%";
  if (needle) needle.style.left = percent.toFixed(1) + "%";
  if (gauge) gauge.classList.toggle("is-perfect", perfect);
  if (!bubble) return;
  if (perfect) {
    bubble.textContent = "PERFECT！松手投球";
  } else {
    const speed = Number(sweep.speed) || CAPTURE_SWEEP.baseSpeed;
    bubble.textContent = "指针进 PERFECT 再松手（×" +
      (speed / CAPTURE_SWEEP.baseSpeed).toFixed(1) + "）";
  }
}

function stopPanelChargeLoop() {
  if (!petChargeFrame) return;
  cancelAnimationFrame(petChargeFrame);
  petChargeFrame = 0;
}

function panelChargeTick() {
  petChargeFrame = 0;
  if (!petCharging) return;
  panelChargeUi(sweepProgress(performance.now() - petPressStart));
  petChargeFrame = requestAnimationFrame(panelChargeTick);
}

let petPressActive = false;

function beginPanelPetPress() {
  if (petViewer && (petViewer.isCapturing() || petViewer.isCharging())) return;
  petPressActive = true;
  petPressStart = performance.now();
  // 已捕获：按下不蓄力；松手时手势判为「放出」，不会误投第二球。
  if (panelPetIsCaught()) return;
  stopPanelChargeLoop();
  petCharging = true;
  const stage = document.getElementById("pet-stage");
  if (stage) stage.classList.add("is-charging");
  const avatar = document.getElementById("pet-avatar");
  if (avatar) avatar.classList.add("charge");
  if (petViewer) petViewer.setCharging(true);
  panelChargeUi({ position: 0, speed: CAPTURE_SWEEP.baseSpeed });
  petChargeFrame = requestAnimationFrame(panelChargeTick);
}

function endPanelPetPress(cancelled) {
  if (!petPressActive) return;
  petPressActive = false;
  const held = petPressStart ? performance.now() - petPressStart : 0;
  const sweep = sweepProgress(held);
  stopPanelChargeLoop();
  petCharging = false;
  petPressStart = 0;
  const stage = document.getElementById("pet-stage");
  if (stage) stage.classList.remove("is-charging");
  const avatar = document.getElementById("pet-avatar");
  if (avatar) avatar.classList.remove("charge");
  if (petViewer) petViewer.setCharging(false);
  panelChargeUi({ position: 0, speed: CAPTURE_SWEEP.baseSpeed });
  if (cancelled || !petViewer) return;
  // 手势与 Windows 迷你伙伴共用同一套纯函数：短按放技能，按住 ≥620ms 才投球，
  // 已捕获一律按「放出」处理，抓住演出中则忽略。
  const gesture = resolvePetGesture({
    heldMs: held,
    caught: panelPetIsCaught(),
    capturing: petViewer.isCapturing(),
  });
  if (gesture.action === "release") {
    releasePanelPetBall();
    return;
  }
  if (gesture.action === "skill") {
    playPanelSkill();
    schedulePanelAutoSkill();
    return;
  }
  if (gesture.action === "throw") {
    throwPanelBall(timingAccuracy(sweep.position));
  }
}

function throwPanelBall(accuracy) {
  if (!petViewer || petViewer.isCapturing() || panelPetIsCaught()) return;
  const pet = currentPet();
  const outcome = captureOutcome({ accuracy: accuracy, tier: currentSkillTier() });
  const bubble = document.getElementById("pet-bubble");
  if (bubble) {
    bubble.textContent = outcome.rating === "perfect"
      ? "完美投球！"
      : outcome.rating === "good" ? "投得不错…" : "这一球有点飘。";
  }
  if (outcome.rating === "perfect") spawnPanelShockwave(1);
  petViewer.throwBall({
    willDodge: outcome.dodged,
    willCatch: outcome.caught,
    onResolved: function (result) {
      const stageNode = document.getElementById("pet-stage");
      if (result.caught) {
        writePanelPetCaught(pet.id, true);
        syncPanelTrayCaught();
        if (stageNode) stageNode.classList.add("is-caught");
        showPetPopup("捕获成功！", "ok");
        if (bubble) bubble.textContent = "点精灵球放出伙伴";
        stopPetTalk();
        spawnPanelShockwave(2);
      } else {
        showPetPopup("捕获失败", "miss");
        if (bubble) {
          bubble.textContent = result.dodged
            ? "它躲开了！再来一球"
            : "球晃了两下弹开了…卡准 PERFECT 更稳";
        }
        schedulePetTalk();
      }
      schedulePanelAutoSkill();
    },
  });
}

// panelPetAction 供面板顶部的按钮使用：已捕获就放出，否则按完美判定投一球。
function panelPetAction() {
  if (panelPetIsCaught()) {
    releasePanelPetBall();
    return;
  }
  throwPanelBall(1);
}

function releasePanelPetBall() {
  if (!petViewer || !petViewer.isCaught()) return;
  const pet = currentPet();
  petViewer.releaseBall();
  writePanelPetCaught(pet.id, false);
  syncPanelTrayCaught();
  const stage = document.getElementById("pet-stage");
  if (stage) stage.classList.remove("is-caught");
  const bubble = document.getElementById("pet-bubble");
  if (bubble) bubble.textContent = pet.name + "回来了！";
  showPetPopup("回来了！", "ok");
  spawnPanelShockwave(1);
  schedulePanelAutoSkill();
  speakPet(pet.name + "回来啦！");
}

// ---------- 宠物自动说话（美漫气泡） ----------
// 每 9-15 秒随机说一句；技能演出也会占用同一条气泡。说话/技能互不打断
// 正在进行的手势（蓄力/捕获演出期间不自动说话）。
let petTalkTimer = 0;
let petTalkIndex = 0;

function speakPet(text) {
  const bubble = document.getElementById("pet-bubble");
  if (!bubble || !text) return;
  bubble.textContent = text;
  bubble.classList.remove("pop");
  void bubble.offsetWidth;
  bubble.classList.add("pop");
  schedulePetTalk();
}

function stopPetTalk() {
  window.clearTimeout(petTalkTimer);
  petTalkTimer = 0;
}

function schedulePetTalk() {
  stopPetTalk();
  if (activeTab !== "pet" || document.visibilityState === "hidden" || panelPetIsCaught()) {
    return;
  }
  petTalkTimer = window.setTimeout(function () {
    petTalkTimer = 0;
    if (activeTab !== "pet" || document.visibilityState === "hidden" || panelPetIsCaught()) {
      return;
    }
    if (petViewer && (petViewer.isCapturing() || petViewer.isCharging())) {
      schedulePetTalk();
      return;
    }
    const pet = currentPet();
    const messages = pet.messages && pet.messages.length
      ? pet.messages
      : [pet.name + "在陪你写代码。"];
    speakPet(messages[petTalkIndex++ % messages.length]);
  }, 9000 + Math.random() * 6000);
}

let panelAutoSkillTimer = 0;
function schedulePanelAutoSkill() {
  if (activeTab !== "pet" || panelPetIsCaught() || document.visibilityState === "hidden") {
    window.clearTimeout(panelAutoSkillTimer);
    panelAutoSkillTimer = 0;
    return;
  }
  if (panelAutoSkillTimer) return;
  panelAutoSkillTimer = window.setTimeout(function () {
    panelAutoSkillTimer = 0;
    if (activeTab !== "pet" || panelPetIsCaught() || document.visibilityState === "hidden") {
      schedulePanelAutoSkill();
      return;
    }
    if (!petViewer || petViewer.isCapturing() || petViewer.isCharging()) {
      schedulePanelAutoSkill();
      return;
    }
    spawnPanelAutoSkill();
    schedulePanelAutoSkill();
  }, autoSkillDelay(currentSkillTier()));
}

const petStage = document.getElementById("pet-stage");
// 眼神跟随监听整个文档：指针在面板任意位置移动都生效（舞台外自动钳到边缘）。
// 只挂 petStage 时，指针移到标题栏/统计区就完全没有事件，头会停在上一帧。
document.addEventListener("pointermove", function (event) {
  if (activeTab !== "pet") return;
  latestPointer.x = event.clientX;
  latestPointer.y = event.clientY;
  applyGazeAtClient(event.clientX, event.clientY);
}, { passive: true });
document.documentElement.addEventListener("pointerleave", function () {
  latestPointer.x = null;
  latestPointer.y = null;
  resetPetGaze();
});
document.addEventListener("visibilitychange", function () {
  if (petViewer) {
    petViewer.setEnabled(document.visibilityState !== "hidden" && activeTab === "pet");
  }
  schedulePanelAutoSkill();
  if (document.visibilityState === "hidden") stopPetTalk();
  else schedulePetTalk();
});
window.addEventListener("blur", resetPetGaze);

petStage.addEventListener("pointerdown", function (event) {
  if (event.button !== 0) return;
  beginPanelPetPress();
});
// 松手/取消挂在 window 上：长按蓄力后即使指针滑出舞台再松手，也能正常结算，
// 不会出现「投出去了但没有任何结果提示」。
window.addEventListener("pointerup", function (event) {
  if (event.button !== undefined && event.button !== 0) return;
  endPanelPetPress(false);
});
window.addEventListener("pointercancel", function () { endPanelPetPress(true); });
petStage.addEventListener("keydown", function (event) {
  if (event.key !== "Enter" && event.key !== " ") return;
  event.preventDefault();
  if (event.repeat) return;
  beginPanelPetPress();
});
petStage.addEventListener("keyup", function (event) {
  if (event.key !== "Enter" && event.key !== " ") return;
  event.preventDefault();
  endPanelPetPress(false);
});
document.getElementById("pet-avatar").addEventListener("animationend", function () {
  this.classList.remove("pounce");
});
document.getElementById("pet-stage").addEventListener("animationend", function () {
  this.classList.remove("playing");
});

function applyTheme(theme) {
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  } else {
    delete document.documentElement.dataset.theme;
  }
}

async function syncTheme() {
  try {
    const values = asJSON(await Bound.SettingsGet());
    applyTheme(values.theme || "system");
    // 面板与主窗口共用同一份性能模式设置；软件渲染时面板的玻璃也会降级。
    initPerformanceMode({ setting: values[PERFORMANCE_MODE_KEY] });
  } catch (_) {
    initPerformanceMode({ setting: "auto" });
  }
}

function hidePanel() {
  Events.Emit("collapse-panel");
  Window.Hide().catch(function () {});
}

function openMain() {
  Events.Emit("open-main");
  hidePanel();
}

document.getElementById("refresh").addEventListener("click", function () {
  if (activeTab === "speed") {
    refreshSpeed();
    return;
  }
  if (activeTab === "pet") {
    panelPetAction();
    return;
  }
  refresh();
});
document.getElementById("open-main-top").addEventListener("click", openMain);
document.getElementById("account-privacy").addEventListener("click", function () {
  toggleAccountPrivacy();
});
document.getElementById("checkin-collapse-all").addEventListener("click", function () {
  if (!currentTargets.length) return;
  const next = nextGroupExpansion(currentTargets, expanded);
  currentTargets.forEach(function (target) {
    expanded[target] = next[target];
  });
  document.querySelectorAll("#targets .app").forEach(function (app) {
    const open = expanded[app.dataset.target] !== false;
    app.classList.toggle("open", open);
    const head = app.querySelector(".app-head");
    if (head) head.setAttribute("aria-expanded", open ? "true" : "false");
  });
  syncPanelCollapseButton();
});
document.getElementById("settings").addEventListener("click", function () {
  Events.Emit("open-settings");
  hidePanel();
});
document.getElementById("collapse").addEventListener("click", hidePanel);
// 只有宠物页用它放技能；签到/速度页的按钮是隐藏的，不再承载「巡检」。
document.getElementById("run").addEventListener("click", function () {
  if (activeTab === "pet") panelPetAction();
});
document.getElementById("open-main").addEventListener("click", openMain);
document.getElementById("quit").addEventListener("click", function () {
  Events.Emit("quit");
});

Events.On("accounts-updated", function () {
  accountQuotaCache.clear();
  if (activeTab === "checkin") refresh();
});
Events.On("accounts-changed", function () {
  accountQuotaCache.clear();
  if (activeTab === "checkin") refresh();
});

// 面板窗口是隐藏创建的，收起时仍然常驻内存。速度页每 750ms 会收到一次
// tokenspeed-changed，如果照着渲染就会在用户看不见的时候不停重建 DOM
// （Windows 虚拟机里能明显感到卡）。因此只在窗口可见时渲染，隐藏期间
// 只记住最新快照，下次显示时补一次。
let panelVisible = false;

Events.On("common:WindowShow", function (event) {
  panelVisible = true;
  if (event.sender !== "panel") return;
  if (activeTab === "checkin") refresh();
  if (activeTab === "speed") refreshSpeed();
  if (activeTab === "pet") {
    renderPet();
    schedulePetTalk();
  }
  if (pendingSpeedSnapshot) {
    renderSpeed(pendingSpeedSnapshot);
    pendingSpeedSnapshot = null;
  }
});
Events.On("common:WindowHide", function (event) {
  if (event && event.sender && event.sender !== "panel") return;
  panelVisible = false;
});
Events.On("tokenspeed-changed", function (event) {
  if (!event || !event.data) return;
  // 面板窗口隐藏常驻时 document.visibilityState 不可靠（隐藏窗口可能仍报
  // visible），用 WindowShow/WindowHide 维护的 panelVisible 判断更准；非速度页
  // 收到快照也只暂存，切回速度页时由 activateTab 补渲染。
  if (panelVisible && activeTab === "speed") {
    pendingSpeedSnapshot = null;
    renderSpeed(event.data);
    return;
  }
  pendingSpeedSnapshot = event.data;
});
Events.On("theme-changed", function (event) {
  applyTheme(event && event.data);
});
Events.On("performance-changed", function (event) {
  initPerformanceMode({ setting: event && event.data });
});
renderSparkline();
renderPet();
activateTab(readStoredString(panelTabStorageKey, "checkin"), { persist: false });
refreshSpeed();
refresh();
syncTheme();
