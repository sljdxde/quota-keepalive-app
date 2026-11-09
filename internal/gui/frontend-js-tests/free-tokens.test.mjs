import assert from "node:assert/strict";
import test from "node:test";

import {
  actionLabel,
  dealMeta,
  filterDeals,
  formatEndsAt,
  formatYuan,
  heroText,
  kindLabel,
  summaryText,
  tierLabel,
  validityText,
} from "../frontend/assets/free-tokens.js";

const deals = [
  {
    id: "minimax",
    provider: "MiniMax",
    qka_provider: "MiniMax Code",
    title: "MiniMax 每日领 400",
    tier: "ready",
    tags: ["每日"],
    endsAt: "2026-11-09T16:00:00.000Z",
  },
  {
    id: "qoder",
    provider: "阿里云 Qoder",
    qka_provider: "Qoder",
    title: "Qoder 每日 100",
    tier: "setup",
    tags: [],
    validity: "领取后 7 天",
  },
  {
    id: "amd",
    provider: "AMD Radeon Cloud",
    title: "AMD 体验额度",
    tier: "none",
    tags: ["限时"],
  },
];

test("tier/kind/action 文案", function () {
  assert.equal(tierLabel("ready"), "已配置可用");
  assert.equal(tierLabel("setup"), "有插件待配置");
  assert.equal(tierLabel("none"), "暂无插件");
  assert.equal(tierLabel(""), "");
  assert.equal(kindLabel("daily"), "每日");
  assert.equal(kindLabel("monthly"), "每月");
  assert.equal(kindLabel("once"), "一次性");
  assert.equal(kindLabel("weekend"), "weekend");
  assert.equal(actionLabel("checkin"), "去签到");
  assert.equal(actionLabel("login"), "去登录");
  assert.equal(actionLabel("open-apps"), "去应用管理");
  assert.equal(actionLabel(""), "");
});

test("金额：null/0 显示破折号，其余保留一位小数", function () {
  assert.equal(formatYuan(null), "—");
  assert.equal(formatYuan(0), "—");
  assert.equal(formatYuan(201), "¥201");
  assert.equal(formatYuan(6.54), "¥6.5");
  assert.equal(formatYuan("6.55"), "¥6.6");
});

test("endsAt 按 Asia/Shanghai 渲染，无效值回落 validity", function () {
  // 2026-11-09T16:00:00Z = 北京时间 11 月 10 日 00:00
  assert.equal(formatEndsAt("2026-11-09T16:00:00.000Z"), "11/10");
  assert.equal(formatEndsAt(""), "");
  assert.equal(formatEndsAt("not-a-date"), "");
  assert.equal(validityText({ endsAt: "2026-11-09T16:00:00.000Z" }), "截止 11/10");
  assert.equal(validityText({ validity: "领取后 7 天" }), "领取后 7 天");
});

test("dealMeta 合并有效期与标签", function () {
  assert.deepEqual(dealMeta(deals[0]), ["截止 11/10", "每日"]);
  assert.deepEqual(dealMeta(deals[1]), ["领取后 7 天"]);
  assert.deepEqual(dealMeta(null), []);
});

test("filterDeals 按档位与关键词过滤", function () {
  assert.equal(filterDeals(deals, { tier: "ready" }).length, 1);
  assert.equal(filterDeals(deals, { tier: "setup" })[0].id, "qoder");
  assert.equal(filterDeals(deals, {}).length, 3);
  assert.equal(filterDeals(deals, { query: "qoder" }).length, 1);
  assert.equal(filterDeals(deals, { query: "mini" }).length, 1);
  assert.equal(filterDeals(deals, { query: "限时" }).length, 1);
  assert.equal(filterDeals(deals, { query: "不存在" }).length, 0);
  assert.equal(filterDeals(null, {}).length, 0);
});

test("summary/hero 文案", function () {
  const summary = { ready: 2, setup: 3, none: 26, today_yuan: 201 };
  assert.equal(summaryText(summary), "已配置可用 2 · 有插件待配置 3 · 暂无插件 26");
  assert.equal(summaryText(null), "已配置可用 0 · 有插件待配置 0 · 暂无插件 0");
  assert.equal(heroText(summary), "¥201 今日可得 · 已配置可用 2 · 有插件待配置 3 · 暂无插件 26");
});
