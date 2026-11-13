import assert from "node:assert/strict";
import test from "node:test";

import {
  FADE_DURATION_MS,
  SPEED_DISPLAY_DEFAULT,
  easeOutCubic,
  fadeValue,
  normalizeSpeedDisplay,
  rollDurationFor,
  shouldAnimateSpeed,
} from "../frontend/assets/speed-display.js";

test("known display modes pass through unchanged", function () {
  assert.equal(normalizeSpeedDisplay("roll"), "roll");
  assert.equal(normalizeSpeedDisplay("fade"), "fade");
  assert.equal(normalizeSpeedDisplay("direct"), "direct");
});

test("unknown or empty display mode falls back to roll", function () {
  assert.equal(normalizeSpeedDisplay("future"), SPEED_DISPLAY_DEFAULT);
  assert.equal(normalizeSpeedDisplay(""), SPEED_DISPLAY_DEFAULT);
  assert.equal(normalizeSpeedDisplay(null), SPEED_DISPLAY_DEFAULT);
  assert.equal(normalizeSpeedDisplay(undefined), SPEED_DISPLAY_DEFAULT);
  assert.equal(normalizeSpeedDisplay(42), SPEED_DISPLAY_DEFAULT);
  assert.equal(normalizeSpeedDisplay("  fade  "), "fade");
});

test("easeOutCubic is monotonic and clamps at both ends", function () {
  assert.equal(easeOutCubic(0), 0);
  assert.equal(easeOutCubic(1), 1);
  assert.equal(easeOutCubic(-1), 0);
  assert.equal(easeOutCubic(2), 1);
  let previous = -1;
  for (let step = 0; step <= 10; step++) {
    const value = easeOutCubic(step / 10);
    assert.ok(value >= previous, "缓出曲线必须单调不减");
    previous = value;
  }
  // 起步比线性快：三次缓出在前 20% 就走过约一半。
  assert.ok(easeOutCubic(0.2) > 0.2);
});

test("fadeValue interpolates between the two endpoints", function () {
  assert.equal(fadeValue(0, 100, 0), 0);
  assert.equal(fadeValue(0, 100, 1), 100);
  assert.equal(fadeValue(50, 50, 0.5), 50);
  // 非数字输入按 0 处理，避免把 NaN 写进界面。
  assert.equal(fadeValue(undefined, 100, 0), 0);
  assert.equal(fadeValue(0, undefined, 1), 0);
});

test("direct mode and the first render never animate", function () {
  assert.equal(shouldAnimateSpeed("direct", { first: false }), false);
  assert.equal(shouldAnimateSpeed("roll", { first: true }), false);
  assert.equal(shouldAnimateSpeed("roll", { first: false }), true);
  assert.equal(shouldAnimateSpeed("fade", { first: false }), true);
});

test("reduced motion disables animation for every mode", function () {
  assert.equal(shouldAnimateSpeed("roll", { first: false, reducedMotion: true }), false);
  assert.equal(shouldAnimateSpeed("fade", { first: false, reducedMotion: true }), false);
  assert.equal(shouldAnimateSpeed("direct", { first: false, reducedMotion: true }), false);
});

test("roll duration shrinks with the sample interval but stays readable", function () {
  assert.equal(rollDurationFor(200), 110);
  assert.equal(rollDurationFor(100), 90);
  assert.equal(rollDurationFor(1000), 260);
  assert.equal(rollDurationFor(0), 110);
  assert.equal(rollDurationFor(undefined), 110);
  // 采样越密，动画时长必须越短，否则追不上新值。
  assert.ok(rollDurationFor(200) < rollDurationFor(750));
});

test("fade duration is a sane fixed value", function () {
  assert.ok(FADE_DURATION_MS >= 150 && FADE_DURATION_MS <= 400);
});

// 速度数字最多每 500ms 变一次：采样 200ms 时若不节流，数字每 200ms 抖一下，
// 人眼读不到具体值。
test("speed throttle caps commits to one per interval", async function () {
  const { createSpeedThrottle } = await import("../frontend/assets/tokenspeed-viz.js");
  const committed = [];
  const throttle = createSpeedThrottle(500, function (text) { committed.push(text); });

  throttle.push("10");
  assert.deepEqual(committed, ["10"], "首帧应直接落位");

  // 间隔内连续推三次：只有最后一次挂起，未到点不提交。
  throttle.push("20");
  throttle.push("30");
  throttle.push("40");
  assert.deepEqual(committed, ["10"], "间隔内不应重复提交");

  await new Promise(function (resolve) { setTimeout(resolve, 560); });
  assert.deepEqual(committed, ["10", "40"], "到点只提交最新值");
  throttle.reset();
});

// 悬浮球读数只排数值 + tok/s 两行；数值字号必须随文本长度收缩，否则
// "1.23k" 这类 5 字符会顶出 36 段 LED 环（真机反馈）。
test("ball readout font shrinks to fit inside the LED ring", async function () {
  const { ballReadoutFontSize, ballReadoutUnitSize } =
    await import("../frontend/assets/tokenspeed-viz.js");

  // 64px 球、3 位以内用基准 0.25S = 16px。
  assert.equal(ballReadoutFontSize(64, "0"), 16);
  assert.equal(ballReadoutFontSize(64, "219"), 16);

  const five = ballReadoutFontSize(64, "1.23k");
  assert.ok(five < 16, "5 字符必须收字号");
  const seven = ballReadoutFontSize(64, "100.00k");
  assert.ok(seven <= five, "字符越多字号越小");
  assert.ok(seven >= 9, "字号有 9px 可读下限");

  // 估算宽度必须收在环内可用宽度（0.60×球径）里。
  for (const text of ["219", "1.23k", "100.00k"]) {
    const size = ballReadoutFontSize(64, text);
    assert.ok(text.length * 0.58 * size <= 0.60 * 64 + 0.5,
      text + " 的估算宽度不应超出球内可用宽度");
  }

  // 贴边 40px 球同样有下限；空文本按 1 字符处理。
  assert.ok(ballReadoutFontSize(40, "999") >= 9);
  assert.equal(ballReadoutFontSize(64, ""), 16);

  // 单位行 64px 球落在 9px（0.086×64=5.5 太小），大球按比例放大。
  assert.equal(ballReadoutUnitSize(64), 9);
  assert.equal(ballReadoutUnitSize(148), 12.7);
});
