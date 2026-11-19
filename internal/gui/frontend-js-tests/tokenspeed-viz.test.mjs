import assert from "node:assert/strict";
import test from "node:test";

import { clampPeakFlagPercent } from "../frontend/assets/tokenspeed-viz.js";

test("peak flag at 100% stays inside the track", function () {
  // 919px 窗口下轨道约 406.7px、标签约 51px；旧实现 100% 时标签右缘
  // 会伸进右侧等级卡约 11.5px。
  const left = clampPeakFlagPercent(100, 51, 406.7);
  assert.ok(left < 100, "标签中心必须左移");
  const half = (51 / 2 / 406.7) * 100;
  assert.ok(Math.abs(left - (100 - half)) < 1e-9, "右缘应刚好贴住轨道右端");
});

test("peak flag keeps its position when there is room", function () {
  assert.equal(clampPeakFlagPercent(42.5, 51, 800), 42.5);
  assert.equal(clampPeakFlagPercent(50, 51, 406.7), 50);
});

test("peak flag at 0% is pushed in by half its width", function () {
  const half = (51 / 2 / 406.7) * 100;
  assert.ok(Math.abs(clampPeakFlagPercent(0, 51, 406.7) - half) < 1e-9);
});

test("missing or invalid layout falls back to plain 0-100 clamp", function () {
  assert.equal(clampPeakFlagPercent(120, 51, 0), 100);
  assert.equal(clampPeakFlagPercent(-5, 51, 0), 0);
  assert.equal(clampPeakFlagPercent(60, 0, 406.7), 60);
  assert.equal(clampPeakFlagPercent(NaN, 51, 406.7), 0);
});

test("flag wider than the track centers at 50%", function () {
  assert.equal(clampPeakFlagPercent(100, 600, 406.7), 50);
  assert.equal(clampPeakFlagPercent(0, 600, 406.7), 50);
});
