// AIHOT 免费额度（Token 羊毛）页的纯展示逻辑：分档、kind、金额、时间与
// 筛选。只在 node 单测里覆盖，页面（main.html）只负责 DOM。

export const TIERS = [
  { id: "ready", label: "已配置可用", hint: "QKA 已内置插件且已登录" },
  { id: "setup", label: "有插件待配置", hint: "插件已内置，登录/启用后即可用" },
  { id: "none", label: "暂无插件", hint: "QKA 还没有对应插件，可去 AIHOT 看原文" },
];

const TIER_LABELS = Object.fromEntries(TIERS.map((tier) => [tier.id, tier.label]));

const KIND_LABELS = {
  daily: "每日",
  monthly: "每月",
  once: "一次性",
  limited: "限时",
  free: "长期免费",
};

const ACTION_LABELS = {
  checkin: "去签到",
  login: "去登录",
  "open-apps": "去应用管理",
};

export function tierLabel(tier) {
  return TIER_LABELS[tier] || "";
}

export function kindLabel(kind) {
  return KIND_LABELS[String(kind || "").toLowerCase()] || String(kind || "");
}

export function actionLabel(action) {
  return ACTION_LABELS[action] || "";
}

// 折算金额：AIHOT 的 yuan 为 null/0 时不给数字（避免把"不折钱"写成 0 元）。
export function formatYuan(value) {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return "—";
  const rounded = Math.round(n * 10) / 10;
  return "¥" + (Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1));
}

export function summaryText(summary) {
  const source = summary || {};
  const ready = Number(source.ready) || 0;
  const setup = Number(source.setup) || 0;
  const none = Number(source.none) || 0;
  return "已配置可用 " + ready + " · 有插件待配置 " + setup + " · 暂无插件 " + none;
}

// endsAt 是 UTC（T16:00:00Z = 北京时间次日 00:00），统一按 Asia/Shanghai 渲染。
export function formatEndsAt(iso) {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  try {
    return new Intl.DateTimeFormat("zh-CN", {
      timeZone: "Asia/Shanghai",
      month: "numeric",
      day: "numeric",
    }).format(date);
  } catch (_) {
    return "";
  }
}

// 有效口径：有截止时间说"截止 M/D"，否则回落到 validity 原文。
export function validityText(deal) {
  const source = deal || {};
  const ends = formatEndsAt(source.endsAt);
  if (ends) return "截止 " + ends;
  return source.validity || "";
}

export function dealMeta(deal) {
  const parts = [];
  const validity = validityText(deal);
  if (validity) parts.push(validity);
  if (deal && Array.isArray(deal.tags)) {
    for (const tag of deal.tags) {
      if (tag) parts.push(tag);
    }
  }
  return parts;
}

// 按档位与关键词筛选；query 匹配厂商/标题/标签。
export function filterDeals(deals, options) {
  const source = Array.isArray(deals) ? deals : [];
  const opts = options || {};
  const tier = opts.tier || "";
  const query = String(opts.query || "").trim().toLowerCase();
  return source.filter(function (deal) {
    if (tier && deal.tier !== tier) return false;
    if (!query) return true;
    const haystack = [
      deal.provider,
      deal.title,
      deal.qka_provider,
      ...(Array.isArray(deal.tags) ? deal.tags : []),
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return haystack.includes(query);
  });
}

// 页面顶部摘要数字：今日折算 + 计数。
export function heroText(summary) {
  const source = summary || {};
  return formatYuan(source.today_yuan) + " 今日可得 · " + summaryText(source);
}
