import assert from "node:assert/strict";
import test from "node:test";

import {
  activityAccountsFor,
  activityChipText,
  activityCostText,
  activityDetailText,
  activityFailureReason,
  activityStateKind,
  activityStateText,
  activitySummaryText,
} from "../frontend/assets/activity.js";

test("state text covers success, idle and every failure reason", function () {
  assert.equal(activityStateText("completed"), "今日已活跃");
  assert.equal(activityStateText("none"), "今日未活跃");
  assert.equal(activityStateText(""), "今日未活跃");
  for (const state of [
    "failed_retryable", "failed_final", "unauthorized", "rate_limited",
    "no_free_model", "needs_stream", "no_reply",
  ]) {
    assert.notEqual(activityStateText(state), state);
    assert.notEqual(activityFailureReason(state), "");
  }
  assert.equal(activityStateKind("completed"), "good");
  assert.equal(activityStateKind("rate_limited"), "bad");
  assert.equal(activityStateKind("none"), "");
});

test("chip text follows sent_today and completed state", function () {
  assert.equal(activityChipText({ sent_today: true }), "今日活跃");
  assert.equal(activityChipText({ today_state: "completed" }), "今日活跃");
  assert.equal(activityChipText({ today_state: "failed_retryable" }), "今日未活跃");
  assert.equal(activityChipText(null), "今日未活跃");
});

test("summary text uses done/total and max streak", function () {
  assert.equal(
    activitySummaryText({ total: 7, done: 5, failed: 1, streak_max: 9 }),
    "今日活跃 5/7 · 最长连续 9 天",
  );
  assert.equal(activitySummaryText(null), "今日活跃 0/0 · 最长连续 0 天");
});

test("detail text exposes failure reason, free cost and streak", function () {
  const detail = activityDetailText({
    today_state: "no_free_model",
    today_message: "没有可用的免费模型，已跳过每日活跃",
    streak: 3,
    last_at: "2026-11-09T10:00:00Z",
  });
  assert.match(detail, /没有免费模型/);
  assert.match(detail, /按量计费/);
  assert.match(detail, /连续 3 天/);
  assert.doesNotMatch(detail, /成本：/);
  const success = activityDetailText({ today_state: "completed", sent_today: true });
  assert.match(success, /成本：免费模型/);
  assert.equal(
    activityCostText({ sent_today: true, today_message: "每日活跃完成（订阅额度）" }),
    "成本：订阅额度",
  );
  // WorkBuddy 走连登上报，不是模型调用。
  assert.equal(
    activityCostText({ sent_today: true, today_message: "每日活跃已上报（WorkBuddy 连登已点亮）" }),
    "成本：不消耗模型额度",
  );
  assert.equal(activityCostText({ sent_today: false }), "");
});

test("accounts are filtered by provider", function () {
  const view = {
    accounts: [
      { provider: "workbuddy", account: "a" },
      { provider: "qoder", account: "b" },
    ],
  };
  assert.deepEqual(activityAccountsFor(view, "workbuddy").map((a) => a.account), ["a"]);
  assert.deepEqual(activityAccountsFor(null, "workbuddy"), []);
});
