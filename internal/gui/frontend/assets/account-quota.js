// 账号「额度行」的纯判定：把一次额度观测收敛成一套口径。
// 与主窗口一致：amount 是已用量，剩余 = limit - amount；只有余额或供应商原文
// 时按原义显示，不硬凑总量。纯函数，node 单测直接覆盖，不在 DOM 里算。
//
// 说明：面板历史上只读 op.balance（一个「额度」数字），口径与主窗口的
// 「剩余 / 总量」不一致；这里统一由同一个函数产出文案，主窗口与面板共用。

// formatQuotaNumber 与主窗口 formatQuota 保持一致：千分位、最多两位小数。
export function formatQuotaNumber(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return "—";
  return n.toLocaleString("zh-CN", { maximumFractionDigits: 2 });
}

// firstQuotaWindow 取第一个非 aside 的额度窗口（aside 窗口不进摘要）。
export function firstQuotaWindow(reading) {
  if (!reading) return null;
  const windows = (reading.windows || []).filter(function (window) { return !window.aside; });
  return windows.length ? windows[0] : null;
}

// quotaViewForWindow 把单个额度窗口转成 {text, pct}；没有可用读数返回 null。
// pct 是「已用比例」（0-100），用于细条填充；窗口只给余额时 pct 为 null。
export function quotaViewForWindow(window, reading) {
  if (window) {
    const prefix = window.name ? window.name + " · " : "";
    const limit = Number(window.limit);
    const amount = Number(window.amount);
    if (limit > 0 && Number.isFinite(amount) && amount >= 0) {
      const remaining = Math.max(0, limit - amount);
      return {
        text: prefix + "剩余 " + formatQuotaNumber(remaining) + " / 总量 " +
          formatQuotaNumber(limit) + (window.unit ? " " + window.unit : ""),
        pct: Math.max(0, Math.min(100, (amount / limit) * 100)),
      };
    }
    if (window.balance != null) {
      return { text: prefix + "剩余 " + formatQuotaNumber(Number(window.balance)), pct: null };
    }
    if (window.display) {
      return { text: prefix + String(window.display), pct: null };
    }
  }
  if (reading && reading.balance != null && reading.balance !== "") {
    return { text: "剩余 " + reading.balance, pct: null };
  }
  return null;
}

// quotaViewForReading 从一次额度观测里取第一个窗口并给出视图。
export function quotaViewForReading(reading) {
  if (!reading) return null;
  return quotaViewForWindow(firstQuotaWindow(reading), reading);
}

// quotaViewForBalance 是签到返回余额的兜底视图（异步读数到达前先显示）。
export function quotaViewForBalance(balance) {
  if (balance == null) return null;
  const n = Number(balance);
  if (!Number.isFinite(n)) return null;
  return { text: "剩余 " + formatQuotaNumber(n), pct: null };
}

// 把一次观测时刻格式化成「本地」HH:mm。后端 observed_at 是 UTC 的 ISO 串，
// 直接 slice(11,16) 会显示 UTC 时间（东八区差 8 小时），刷新后看着像没变。
// 解析不了就退回原样（兼容早期只存 HH:mm 的库）。
export function quotaClockText(value) {
  if (value == null || value === "") return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    const text = String(value);
    return text.length >= 16 ? text.slice(11, 16) : text;
  }
  return String(date.getHours()).padStart(2, "0") + ":" +
    String(date.getMinutes()).padStart(2, "0");
}

// lastRefreshClockText 取后端观测时刻与「本次会话里手动刷新的本地时刻」中较晚
// 的那个，按本地时区输出 HH:mm。手动刷新会记一个本地时间戳：即使后端 15s 去重
// 没写新观测（observed_at 还是旧的），用户刚点过刷新也应看到时间前进。
export function lastRefreshClockText(observedAt, manualRefreshedAt) {
  let chosen = observedAt || "";
  if (manualRefreshedAt != null) {
    const observedDate = observedAt ? new Date(observedAt) : null;
    const manualDate = new Date(manualRefreshedAt);
    if (!Number.isNaN(manualDate.getTime()) &&
        (!observedDate || Number.isNaN(observedDate.getTime()) ||
          manualDate.getTime() > observedDate.getTime())) {
      chosen = manualDate.getTime();
    }
  }
  return quotaClockText(chosen);
}
