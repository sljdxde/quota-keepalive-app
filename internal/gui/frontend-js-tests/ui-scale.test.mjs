// 界面缩放的纯逻辑测试：node --test internal/gui/frontend-js-tests/ui-scale.test.mjs
//
// 背景：默认主窗口是 920×640 DIP（见 window_size.go），内容区约 906×603 CSS px，
// 而设计基准是 900×600——所以默认窗口下缩放应为 1（原生字号），只有窗口被拖小
// 或屏幕更矮时才收缩。

import test from "node:test";
import assert from "node:assert/strict";

import {
  autoScale,
  resolveScale,
  readViewport,
  MIN_AUTO_SCALE,
  DESIGN_WIDTH,
  DESIGN_HEIGHT,
} from "../frontend/assets/ui-scale.js";

test("默认窗口的内容区不缩放（906x603 ≥ 900x600）", () => {
  assert.equal(autoScale(906, 603), 1);
});

test("大窗口不缩放", () => {
  assert.equal(autoScale(DESIGN_WIDTH, DESIGN_HEIGHT), 1);
  assert.equal(autoScale(1600, 900), 1);
  assert.equal(autoScale(2560, 1400), 1);
});

test("窗口被拖小时按高度收缩", () => {
  // 520/600 ≈ 0.867 → 0.85（下限）
  assert.equal(autoScale(906, 520), MIN_AUTO_SCALE);
  // 收缩后等效高度要够用：520/0.85 ≈ 612，覆盖设计高度。
  assert.ok(520 / autoScale(906, 520) >= DESIGN_HEIGHT * 0.95);
});

test("高度略不足时取 0.05 步长", () => {
  // 570/600 = 0.95
  assert.equal(autoScale(900, 570), 0.95);
  // 530/600 ≈ 0.883 → 0.85
  assert.equal(autoScale(900, 530), 0.85);
});

test("宽度不足也会触发收缩", () => {
  // 810/900 = 0.9
  assert.equal(autoScale(810, 900), 0.9);
  const scale = autoScale(700, 900);
  assert.ok(scale < 1);
  assert.equal(scale, MIN_AUTO_SCALE);
});

test("不低于自动缩放下限，宁可让用户滚动", () => {
  assert.equal(autoScale(906, 300), MIN_AUTO_SCALE);
  assert.equal(autoScale(400, 300), MIN_AUTO_SCALE);
});

test("视口未知时返回 1", () => {
  assert.equal(autoScale(0, 0), 1);
  assert.equal(autoScale(undefined, 595), 1);
  assert.equal(autoScale(906, -1), 1);
});

test("设置值解析：auto/空走自动，数字走固定", () => {
  assert.equal(resolveScale("auto", 906, 400), MIN_AUTO_SCALE);
  assert.equal(resolveScale("", 906, 400), MIN_AUTO_SCALE);
  assert.equal(resolveScale(undefined, 906, 400), MIN_AUTO_SCALE);
  assert.equal(resolveScale("1", 906, 400), 1);
  assert.equal(resolveScale("0.9", 906, 400), 0.9);
  assert.equal(resolveScale(0.8, 906, 400), 0.8);
});

test("非法固定值回退到自动", () => {
  assert.equal(resolveScale("abc", 906, 400), MIN_AUTO_SCALE);
  assert.equal(resolveScale("0.2", 906, 400), MIN_AUTO_SCALE);
  assert.equal(resolveScale("9", 906, 400), MIN_AUTO_SCALE);
});

test("视口尺寸原样返回（Chromium zoom 不改变 innerWidth）", () => {
  assert.deepEqual(readViewport(906, 603), { width: 906, height: 603 });
  // 非法值折算为 0，交给 autoScale 返回 1。
  assert.deepEqual(readViewport(undefined, -1), { width: 0, height: 0 });
  assert.equal(autoScale(readViewport(undefined, -1).width, readViewport(undefined, -1).height), 1);
});
