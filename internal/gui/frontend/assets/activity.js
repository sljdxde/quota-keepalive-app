// 「每日活跃」的纯展示逻辑：状态文案、汇总文案、失败原因与账号筛选。
// 主窗口只负责 DOM；这里保持无副作用，便于 node --test 直接覆盖。
//
// 口径：
// - 免费模型优先；订阅制账号没有免费模型时用订阅额度（成本记「订阅额度」），
//   按量计费账号不会自动发送；不虚构金额。
// - 连续天数允许今天还没成功：从昨天往前数，断一天即止。
// - 失败态区分：无免费模型 / 未登录 / 限流 / 鉴权 / 需要流式 / 无回复。

const STATE_TEXT = {
  completed: "今日已活跃",
  none: "今日未活跃",
  pending: "等待执行",
  running: "执行中",
  failed_retryable: "稍后自动重试",
  failed_final: "活跃失败",
  unauthorized: "需要重新登录",
  rate_limited: "被限流，稍后重试",
  no_free_model: "没有免费模型",
  needs_stream: "供应商要求流式",
  no_reply: "供应商没有回复",
};

const FAILURE_HINTS = {
  failed_retryable: "网络或上游暂时失败；10 分钟后自动重试，不消耗当天次数。",
  failed_final: "本次执行失败；明天会自动再试。",
  unauthorized: "账号登录已失效，请重新登录后再试。",
  rate_limited: "上游限流 429；稍后自动重试，不消耗当天次数。",
  no_free_model: "该账号没有可用模型，已跳过（按量计费账号不会自动发送）。",
  needs_stream: "供应商要求流式请求，自动降级重试仍失败。",
  no_reply: "供应商返回 2xx，但没有模型回复；按失败处理并稍后重试。",
};

const FAILED_STATES = [
  "failed_retryable", "failed_final", "unauthorized", "rate_limited",
  "no_free_model", "needs_stream", "no_reply",
];

export function activityStateText(state) {
  const key = String(state || "none");
  return STATE_TEXT[key] || key;
}

export function activityStateKind(state) {
  if (state === "completed") return "good";
  if (FAILED_STATES.indexOf(state) !== -1) return "bad";
  if (state === "running" || state === "pending") return "warn";
  return "";
}

export function activityFailureReason(state) {
  return FAILURE_HINTS[String(state || "")] || "";
}

export function activityChipText(account) {
  if (!account) return "今日未活跃";
  return account.sent_today || account.today_state === "completed"
    ? "今日活跃"
    : "今日未活跃";
}

export function activitySummaryText(summary) {
  const item = summary || {};
  const total = Number(item.total) || 0;
  const done = Number(item.done) || 0;
  const streak = Number(item.streak_max) || 0;
  return "今日活跃 " + done + "/" + total + " · 最长连续 " + streak + " 天";
}

export function activityCostText(account) {
  if (!account || !account.sent_today) return "";
  const message = String(account.today_message || "");
  // WorkBuddy 的每日活跃是计费域 chat 事件（点亮连登），不经过模型，别写成
  // 「免费模型」，否则用户会以为它真的消费了一次模型调用。
  if (message.indexOf("连登") >= 0) return "成本：不消耗模型额度";
  return message.indexOf("订阅") >= 0 ? "成本：订阅额度" : "成本：免费模型";
}

export function activityDetailText(account) {
  if (!account) return "";
  const parts = [];
  const state = account.today_state || "none";
  parts.push(activityStateText(state));
  const reason = activityFailureReason(state);
  if (reason) parts.push(reason);
  if (account.today_message) parts.push(account.today_message);
  const cost = activityCostText(account);
  if (cost) parts.push(cost);
  if (Number(account.streak) > 0) parts.push("连续 " + account.streak + " 天");
  if (account.last_at) parts.push("最近 " + account.last_at);
  return parts.join(" · ");
}

export function activityAccountsFor(view, provider) {
  const items = (view && view.accounts) || [];
  return items.filter(function (item) {
    return item && item.provider === provider;
  });
}
