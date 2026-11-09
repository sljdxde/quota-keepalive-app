import assert from "node:assert/strict";
import test from "node:test";

import { petAimPoint } from "../frontend/assets/pet-gaze.js";

const stage = { left: 0, top: 0, right: 300, bottom: 220, width: 300, height: 220 };
const avatar = { left: 90, top: 50, right: 210, bottom: 170, width: 120, height: 120 };

test("pet gaze is centered when the pointer is at the avatar center", function () {
  const aim = petAimPoint(150, 100.4, stage, avatar);
  assert.ok(aim);
  assert.ok(Math.abs(aim.x) < 0.0001);
  assert.ok(Math.abs(aim.y) < 0.0001);
});

test("pet gaze turns both left and right symmetrically", function () {
  const left = petAimPoint(10, 100, stage, avatar);
  const right = petAimPoint(290, 100, stage, avatar);
  assert.ok(left && right);
  assert.ok(left.x < 0);
  assert.ok(right.x > 0);
  assert.ok(Math.abs(left.x + right.x) < 0.0001);
});

test("pet gaze looks up and down and resets outside the stage", function () {
  const up = petAimPoint(150, 10, stage, avatar);
  const down = petAimPoint(150, 210, stage, avatar);
  assert.ok(up && down);
  assert.ok(up.y < 0);
  assert.ok(down.y > 0);
  assert.equal(petAimPoint(-1, 20, stage, avatar), null);
});

// 防回归：竖直移动必须是「干净的俯仰」，不能漏进水平分量——否则头会停在
// 默认侧脸上，就是用户反馈的「鼠标挪到最下方、头还看着偏右」。
test("pet gaze straight down is pure pitch with no horizontal bleed", function () {
  const down = petAimPoint(150, 220, stage, avatar);
  assert.ok(down);
  assert.ok(Math.abs(down.x) < 1e-9, "竖直移动不应产生水平偏航");
  assert.ok(down.y > 0.9, "移到舞台底边俯仰应接近满量程");
});

// 防回归：指针移到边缘要能到满量程 ±1（旧分母固定，走满也到不了极值，追踪发虚）。
test("pet gaze reaches full range at the stage edges", function () {
  const right = petAimPoint(299, 100, stage, avatar);
  const left = petAimPoint(1, 100, stage, avatar);
  const bottom = petAimPoint(150, 219, stage, avatar);
  const top = petAimPoint(150, 1, stage, avatar);
  assert.ok(right.x > 0.95 && left.x < -0.95);
  assert.ok(bottom.y > 0.95 && top.y < -0.95);
});
