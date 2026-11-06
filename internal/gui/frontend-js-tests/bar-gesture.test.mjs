import assert from "node:assert/strict";
import test from "node:test";

import {
  CHARGE_THROW_MS,
  DRAG_START_PX,
  chargeProgress,
  dragTargetOrigin,
  resolveGesture,
  shouldStartDrag,
} from "../frontend/assets/bar-gesture.js";

// resolveGesture：悬浮球「按下→弹起」的全部分支。表驱动、真实输入，
// 不 mock 任何东西——它就是页面 finishPress 调用的同一个纯函数。
test("resolveGesture: floating short tap plays skill", function () {
  const result = resolveGesture({
    heldMs: 80, docked: false, caught: false, capturing: false,
    moved: false, hasPet: true,
  });
  assert.equal(result.action, "skill");
  assert.equal(result.longPress, false);
});

test("resolveGesture: long press throws the ball", function () {
  const atThreshold = resolveGesture({
    heldMs: CHARGE_THROW_MS, docked: false, caught: false, capturing: false,
    moved: false, hasPet: true,
  });
  assert.equal(atThreshold.action, "throw");
  assert.equal(atThreshold.longPress, true);

  // 刚好差 1ms 仍是短按。
  const below = resolveGesture({
    heldMs: CHARGE_THROW_MS - 1, docked: false, caught: false, capturing: false,
    moved: false, hasPet: true,
  });
  assert.equal(below.action, "skill");
  assert.equal(below.longPress, false);
});

test("resolveGesture: caught pet short tap releases", function () {
  const result = resolveGesture({
    heldMs: 60, docked: false, caught: true, capturing: false,
    moved: false, hasPet: true,
  });
  assert.equal(result.action, "release");
});

test("resolveGesture: caught pet long press still throws at the ball", function () {
  // 已捕获时长按语义仍是 throw：releaseBall 之后想再抓要等下次放下。
  // 当前产品定义：长按优先于 release。
  const result = resolveGesture({
    heldMs: CHARGE_THROW_MS + 100, docked: false, caught: true, capturing: false,
    moved: false, hasPet: true,
  });
  assert.equal(result.action, "throw");
});

test("resolveGesture: capturing state blocks skill on short tap", function () {
  const result = resolveGesture({
    heldMs: 80, docked: false, caught: false, capturing: true,
    moved: false, hasPet: true,
  });
  assert.equal(result.action, "none");
});

test("resolveGesture: docked short tap toggles dock regardless of pet state", function () {
  for (const caught of [false, true]) {
    const result = resolveGesture({
      heldMs: 80, docked: true, caught, capturing: false,
      moved: false, hasPet: true,
    });
    assert.equal(result.action, "dock-toggle");
  }
  // 长按在贴边态同样弹出（窗口太小没有第二种手势）。
  const long = resolveGesture({
    heldMs: CHARGE_THROW_MS + 50, docked: true, caught: false, capturing: false,
    moved: false, hasPet: true,
  });
  assert.equal(long.action, "dock-toggle");
  assert.equal(long.longPress, true);
});

test("resolveGesture: moved (native drag started) swallows the release", function () {
  const result = resolveGesture({
    heldMs: 200, docked: false, caught: false, capturing: false,
    moved: true, hasPet: true,
  });
  assert.equal(result.action, "none");
});

test("resolveGesture: no pet short tap is a no-op", function () {
  const result = resolveGesture({
    heldMs: 80, docked: false, caught: false, capturing: false,
    moved: false, hasPet: false,
  });
  assert.equal(result.action, "none");
});

test("resolveGesture: invalid/negative heldMs is treated as short tap", function () {
  for (const heldMs of [NaN, undefined, -50]) {
    const result = resolveGesture({
      heldMs, docked: false, caught: false, capturing: false,
      moved: false, hasPet: true,
    });
    assert.equal(result.action, "skill");
    assert.equal(result.longPress, false);
  }
});

// shouldStartDrag：拖动升级的边界——阈值、按键位、按住状态。
test("shouldStartDrag: arms only after the 6px threshold with left button held", function () {
  const pressed = { pressed: true, buttons: 1 };
  assert.equal(shouldStartDrag({ ...pressed, dx: DRAG_START_PX, dy: 0 }), true);
  assert.equal(shouldStartDrag({ ...pressed, dx: 0, dy: -DRAG_START_PX }), true);
  assert.equal(shouldStartDrag({ ...pressed, dx: DRAG_START_PX - 1, dy: 0 }), false);
  assert.equal(shouldStartDrag({ ...pressed, dx: 0, dy: DRAG_START_PX - 1 }), false);
  // 斜向分量合成越阈：各 4px 不到阈值也不拖。
  assert.equal(shouldStartDrag({ ...pressed, dx: 4, dy: 4 }), false);
});

test("shouldStartDrag: ignores non-left buttons and released state", function () {
  assert.equal(shouldStartDrag({ pressed: true, buttons: 2, dx: 100, dy: 100 }), false);
  assert.equal(shouldStartDrag({ pressed: true, buttons: 0, dx: 100, dy: 100 }), false);
  assert.equal(shouldStartDrag({ pressed: false, buttons: 1, dx: 100, dy: 100 }), false);
});

test("shouldStartDrag: malformed numbers never arm dragging", function () {
  assert.equal(shouldStartDrag({ pressed: true, buttons: 1, dx: NaN, dy: NaN }), false);
  // 字符串 "1" 经 Number() 归一后就是左键：位移超阈仍要拖动（真实事件里
  // buttons 一定是 number，但这里锁定归一行为不变）。
  assert.equal(shouldStartDrag({ pressed: true, buttons: "1", dx: 10, dy: 0 }), true);
});

// dragTargetOrigin：把球心对中到指针下。
test("dragTargetOrigin centers the ball under the pointer", function () {
  // 指针在窗口中心：目标位置不变。
  const center = dragTargetOrigin({
    clientX: 32, clientY: 32, windowW: 64, windowH: 64, screenX: 100, screenY: 200,
  });
  assert.deepEqual(center, { x: 100, y: 200 });
  // 指针在右上角：窗口往右下补差。
  const corner = dragTargetOrigin({
    clientX: 0, clientY: 0, windowW: 64, windowH: 64, screenX: 100, screenY: 200,
  });
  assert.deepEqual(corner, { x: 132, y: 232 });
  // 贴边 44px 半圆同样对中（窗口半出屏时目标坐标可以为负）。
  const docked = dragTargetOrigin({
    clientX: 10, clientY: 30, windowW: 44, windowH: 44, screenX: -22, screenY: 300,
  });
  assert.deepEqual(docked, { x: -10, y: 292 });
});

test("dragTargetOrigin: malformed input falls back to safe values", function () {
  const result = dragTargetOrigin({});
  // 无输入按 1×1 窗口、指针在原点处理：dx=dy=0.5 → Math.round 得 1。
  assert.deepEqual(result, { x: 1, y: 1 });
  const tiny = dragTargetOrigin({
    clientX: 5, clientY: 5, windowW: 0, windowH: -3, screenX: 10, screenY: 10,
  });
  // 窗口尺寸非法时按 1×1 处理：dx = 0.5 - 5 = -4.5 → Math.round(-4.5) = -4。
  assert.deepEqual(tiny, { x: 6, y: 6 });
});

// chargeProgress：蓄力弧进度。
test("chargeProgress: clamps between 0 and 1 and tracks time linearly", function () {
  assert.equal(chargeProgress(0), 0);
  assert.equal(chargeProgress(CHARGE_THROW_MS / 2), 0.5);
  assert.equal(chargeProgress(CHARGE_THROW_MS), 1);
  // 超时封顶，不会倒卷。
  assert.equal(chargeProgress(CHARGE_THROW_MS * 10), 1);
  // 非法输入按 0 处理。
  assert.equal(chargeProgress(NaN), 0);
  assert.equal(chargeProgress(-100), 0);
});

test("gesture constants stay in sync with product decisions", function () {
  // 长按阈值：要明显高于普通点击（>300ms），又不能让长按等待太难受（<1s）。
  assert.ok(CHARGE_THROW_MS > 300 && CHARGE_THROW_MS < 1000);
  // 拖动阈值：大于手抖幅度，小于明显拖动意图。
  assert.ok(DRAG_START_PX >= 4 && DRAG_START_PX <= 12);
});
