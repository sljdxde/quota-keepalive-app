import assert from "node:assert/strict";
import test from "node:test";

import {
  AUTO_RELEASE_SPEED,
  CHARGE_THROW_MS,
  DRAG_START_PX,
  chargeProgress,
  dragTargetOrigin,
  resolvePetGesture,
  runAmplitude,
  runDuration,
  runTier,
  shakeDuration,
  shouldAutoRelease,
  shouldStartDrag,
} from "../frontend/assets/bar-gesture.js";

// resolvePetGesture：宠物窗口里「按下→弹起」的全部分支。表驱动、真实输入，
// 不 mock 任何东西——它就是页面 finishPetPress 调用的同一个纯函数。
test("resolvePetGesture: short tap plays a skill", function () {
  const result = resolvePetGesture({ heldMs: 80, caught: false, capturing: false });
  assert.equal(result.action, "skill");
  assert.equal(result.longPress, false);
});

test("resolvePetGesture: long press throws the ball", function () {
  const atThreshold = resolvePetGesture({
    heldMs: CHARGE_THROW_MS, caught: false, capturing: false,
  });
  assert.equal(atThreshold.action, "throw");
  assert.equal(atThreshold.longPress, true);

  const below = resolvePetGesture({
    heldMs: CHARGE_THROW_MS - 1, caught: false, capturing: false,
  });
  assert.equal(below.action, "skill");
  assert.equal(below.longPress, false);
});

test("resolvePetGesture: caught pet is released on any tap", function () {
  for (const heldMs of [80, CHARGE_THROW_MS, CHARGE_THROW_MS + 200]) {
    const result = resolvePetGesture({ heldMs, caught: true, capturing: false });
    assert.equal(result.action, "release");
  }
});

test("resolvePetGesture: capturing animation blocks new gestures", function () {
  const result = resolvePetGesture({ heldMs: 80, caught: false, capturing: true });
  assert.equal(result.action, "none");
  const long = resolvePetGesture({
    heldMs: CHARGE_THROW_MS + 50, caught: false, capturing: true,
  });
  assert.equal(long.action, "none");
});

test("resolvePetGesture: invalid/negative heldMs is a short tap", function () {
  for (const heldMs of [NaN, undefined, -50]) {
    const result = resolvePetGesture({ heldMs, caught: false, capturing: false });
    assert.equal(result.action, "skill");
    assert.equal(result.longPress, false);
  }
});

// shouldStartDrag：拖动升级的边界。
test("shouldStartDrag: arms only after the 6px threshold with left button held", function () {
  const pressed = { pressed: true, buttons: 1 };
  assert.equal(shouldStartDrag({ ...pressed, dx: DRAG_START_PX, dy: 0 }), true);
  assert.equal(shouldStartDrag({ ...pressed, dx: 0, dy: -DRAG_START_PX }), true);
  assert.equal(shouldStartDrag({ ...pressed, dx: DRAG_START_PX - 1, dy: 0 }), false);
  assert.equal(shouldStartDrag({ ...pressed, dx: 0, dy: DRAG_START_PX - 1 }), false);
  assert.equal(shouldStartDrag({ ...pressed, dx: 4, dy: 4 }), false);
});

test("shouldStartDrag: ignores non-left buttons and released state", function () {
  assert.equal(shouldStartDrag({ pressed: true, buttons: 2, dx: 100, dy: 100 }), false);
  assert.equal(shouldStartDrag({ pressed: true, buttons: 0, dx: 100, dy: 100 }), false);
  assert.equal(shouldStartDrag({ pressed: false, buttons: 1, dx: 100, dy: 100 }), false);
});

test("shouldStartDrag: malformed numbers never arm dragging", function () {
  assert.equal(shouldStartDrag({ pressed: true, buttons: 1, dx: NaN, dy: NaN }), false);
  // 字符串 "1" 经 Number() 归一后就是左键：位移超阈仍要拖动。
  assert.equal(shouldStartDrag({ pressed: true, buttons: "1", dx: 10, dy: 0 }), true);
});

// dragTargetOrigin：把球心对中到指针下。
test("dragTargetOrigin centers the ball under the pointer", function () {
  const center = dragTargetOrigin({
    clientX: 32, clientY: 32, windowW: 64, windowH: 64, screenX: 100, screenY: 200,
  });
  assert.deepEqual(center, { x: 100, y: 200 });
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
  assert.deepEqual(result, { x: 1, y: 1 });
  const tiny = dragTargetOrigin({
    clientX: 5, clientY: 5, windowW: 0, windowH: -3, screenX: 10, screenY: 10,
  });
  assert.deepEqual(tiny, { x: 6, y: 6 });
});

// chargeProgress：蓄力条进度。
test("chargeProgress: clamps between 0 and 1 and tracks time linearly", function () {
  assert.equal(chargeProgress(0), 0);
  assert.equal(chargeProgress(CHARGE_THROW_MS / 2), 0.5);
  assert.equal(chargeProgress(CHARGE_THROW_MS), 1);
  assert.equal(chargeProgress(CHARGE_THROW_MS * 10), 1);
  assert.equal(chargeProgress(NaN), 0);
  assert.equal(chargeProgress(-100), 0);
});

// runTier / runDuration / runAmplitude：分档驱动不同跑姿，档间跳变明显。
// 连续插值在 26px 宠物上肉眼分辨不出，分档后每档周期差约 2 倍。
test("runTier: 静止 / 踱步 / 小跑 / 快跑 / 冲刺 五档边界", function () {
  const cases = [
    [0, 0], [-5, 0], [NaN, 0],
    [1, 1], [7, 1],
    [8, 2], [18, 2], [31, 2],
    [32, 3], [120, 3], [135, 3],
    [136, 4], [399, 4], [400, 4], [5000, 4],
  ];
  for (const [speed, want] of cases) {
    assert.equal(runTier(speed), want, `speed=${speed}`);
  }
});

test("runDuration: 每档一步时长，静止→冲刺差近 10 倍", function () {
  assert.equal(runDuration(0), 0);
  assert.equal(runDuration(-5), 0);
  assert.equal(runDuration(NaN), 0);
  const tiers = [runDuration(1), runDuration(8), runDuration(32), runDuration(136)];
  assert.deepEqual(tiers, [1.15, 0.6, 0.3, 0.14]);
  for (let i = 1; i < tiers.length; i++) {
    assert.ok(tiers[i - 1] > tiers[i] * 1.7, `相邻档差应明显: ${tiers}`);
  }
  // 同一档内周期不变；封顶后也不再变快。
  assert.equal(runDuration(18), runDuration(8));
  assert.equal(runDuration(5000), runDuration(136));
});

test("runAmplitude: 各档幅度跳变，冲刺档远大于踱步档", function () {
  assert.deepEqual(runAmplitude(0), { bob: 0, tilt: 0, stride: 0 });
  assert.deepEqual(runAmplitude(-5), { bob: 0, tilt: 0, stride: 0 });
  assert.deepEqual(runAmplitude(NaN), { bob: 0, tilt: 0, stride: 0 });
  const stroll = runAmplitude(1);
  const jog = runAmplitude(18);
  const fast = runAmplitude(120);
  const sprint = runAmplitude(400);
  assert.deepEqual(stroll, { bob: 0.6, tilt: 1.5, stride: 0.2 });
  assert.deepEqual(jog, { bob: 2.2, tilt: 6, stride: 1.2 });
  assert.deepEqual(fast, { bob: 4.2, tilt: 13, stride: 2.6 });
  assert.deepEqual(sprint, { bob: 7, tilt: 20, stride: 4.2 });
  const ordered = [stroll, jog, fast, sprint];
  for (const key of ["bob", "tilt", "stride"]) {
    for (let i = 1; i < ordered.length; i++) {
      assert.ok(ordered[i][key] > ordered[i - 1][key] * 1.5,
        `${key} 档间应有明显跳变: ${JSON.stringify(ordered)}`);
    }
  }
  // 满环速度封顶后幅度不再变大；同档内幅度相同。
  assert.deepEqual(runAmplitude(5000), sprint);
  assert.deepEqual(runAmplitude(31), runAmplitude(18));
});

test("shakeDuration: idle means still, faster speed shakes faster and is capped", function () {
  assert.equal(shakeDuration(0), 0);
  const slow = shakeDuration(50);
  const fast = shakeDuration(400);
  assert.equal(slow, 0.595);
  assert.equal(fast, 0.22);
  assert.ok(slow > fast);
  assert.equal(shakeDuration(AUTO_RELEASE_SPEED * 3), fast);
});

test("shouldAutoRelease: 达到 400 tok/s（称号环满环）就放出", function () {
  assert.equal(AUTO_RELEASE_SPEED, 400);
  assert.equal(shouldAutoRelease(0), false);
  assert.equal(shouldAutoRelease(399.9), false);
  assert.equal(shouldAutoRelease(400), true);
  assert.equal(shouldAutoRelease(400.1), true);
  assert.equal(shouldAutoRelease(2000), true);
  assert.equal(shouldAutoRelease(NaN), false);
});