import assert from "node:assert/strict";
import test from "node:test";

import {
  firstQuotaWindow,
  formatQuotaNumber,
  quotaViewForBalance,
  quotaViewForReading,
  quotaViewForWindow,
} from "../frontend/assets/account-quota.js";

// 额度口径与主窗口一致：只说「剩余 / 总量」，amount 是已用量。
test("窗口额度：剩余 = limit - amount，进度条按已用比例", function () {
  const view = quotaViewForReading({
    windows: [
      { name: "5h", limit: 1000, amount: 250, unit: "credits" },
      { name: "week", limit: 5000, amount: 100 },
    ],
  });
  assert.deepEqual(view, {
    text: "5h · 剩余 750 / 总量 1,000 credits",
    pct: 25,
  });
});

test("窗口额度：amount 超过 limit 时剩余钳到 0、pct 封顶 100", function () {
  const view = quotaViewForWindow({ limit: 100, amount: 140 });
  assert.equal(view.text, "剩余 0 / 总量 100");
  assert.equal(view.pct, 100);
});

test("aside 窗口不参与摘要，取第一个可见窗口", function () {
  const reading = {
    windows: [
      { name: "试用", amount: 10, aside: true },
      { name: "正式", limit: 200, amount: 50 },
    ],
  };
  assert.equal(firstQuotaWindow(reading).name, "正式");
  assert.equal(quotaViewForReading(reading).text, "正式 · 剩余 150 / 总量 200");
});

test("只有 balance 的窗口按原义显示剩余，不给伪总量", function () {
  const view = quotaViewForReading({ windows: [{ balance: 900 }] });
  assert.deepEqual(view, { text: "剩余 900", pct: null });
});

test("只有 display 原文时原样展示", function () {
  const view = quotaViewForReading({ windows: [{ display: "900 / 900 credits" }] });
  assert.deepEqual(view, { text: "900 / 900 credits", pct: null });
});

test("没有窗口时退回观测级 balance", function () {
  const view = quotaViewForReading({ windows: [], balance: "1,883.02" });
  assert.deepEqual(view, { text: "剩余 1,883.02", pct: null });
});

test("没有可用读数返回 null（面板显示「暂无额度读数」）", function () {
  assert.equal(quotaViewForReading(null), null);
  assert.equal(quotaViewForReading({}), null);
  assert.equal(quotaViewForReading({ windows: [{ aside: true, amount: 1 }] }), null);
});

test("签到余额兜底视图与缓存兜底", function () {
  assert.deepEqual(quotaViewForBalance(1883), { text: "剩余 1,883", pct: null });
  assert.equal(quotaViewForBalance(null), null);
  assert.equal(quotaViewForBalance("not-a-number"), null);
});

test("数字格式：千分位、最多两位小数、非法值给破折号", function () {
  assert.equal(formatQuotaNumber(1016.98), "1,016.98");
  assert.equal(formatQuotaNumber(1016.9800001), "1,016.98");
  assert.equal(formatQuotaNumber("abc"), "—");
});

import { quotaClockText, lastRefreshClockText } from "../frontend/assets/account-quota.js";

test("观测时刻按本地时区显示 HH:mm（后端存 UTC）", function () {
  const utc = "2026-11-18T07:23:00Z";
  const expected = String(new Date(utc).getHours()).padStart(2, "0") + ":" +
    String(new Date(utc).getMinutes()).padStart(2, "0");
  assert.equal(quotaClockText(utc), expected);
});

test("观测时刻解析不了就退回原文切片", function () {
  assert.equal(quotaClockText("07:23"), "07:23");
  assert.equal(quotaClockText(""), "");
  assert.equal(quotaClockText(null), "");
});

test("手动刷新时刻比观测更新时，取手动时刻（后端 15s 去重未写新观测）", function () {
  const observed = "2026-11-18T01:00:00Z";
  const manual = new Date("2026-11-18T09:00:00Z").getTime();
  const expected = String(new Date(manual).getHours()).padStart(2, "0") + ":" +
    String(new Date(manual).getMinutes()).padStart(2, "0");
  assert.equal(lastRefreshClockText(observed, manual), expected);
});

test("观测比手动刷新更新时，取观测时刻（自动刷新后）", function () {
  const observed = "2026-11-18T10:00:00Z";
  const manual = new Date("2026-11-18T09:00:00Z").getTime();
  assert.equal(lastRefreshClockText(observed, manual),
    quotaClockText(observed));
});
