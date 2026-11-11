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
