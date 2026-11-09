import assert from "node:assert/strict";
import test from "node:test";

import {
  BALL_PET_SIZE,
  BALL_SPEED_FONT_PX,
  ballFaceState,
  ballSpeedFontPx,
} from "../frontend/assets/ball-face.js";

// ballFaceState 是 bar.html 球面唯一的状态判定：宠物模式 / 精灵球模式
// 直接决定显隐与动画。这里用真实输入跑表，不 mock DOM。

test("ballFaceState: 宠物模式只显示宠物，任何速度都不会显示精灵球", function () {
  for (const speed of [0, 18, 1200]) {
    const face = ballFaceState({ speed: speed, speedText: "18", caught: false });
    assert.equal(face.petVisible, true);
    assert.equal(face.ballVisible, false);
  }
});

test("ballFaceState: 精灵球模式只显示精灵球，任何速度都不会显示宠物", function () {
  for (const speed of [0, 18, 1200]) {
    const face = ballFaceState({ speed: speed, speedText: "18", caught: true });
    assert.equal(face.petVisible, false);
    assert.equal(face.ballVisible, true);
  }
});

test("ballFaceState: 宠物与精灵球任何时刻都互斥", function () {
  for (const caught of [false, true]) {
    for (const speed of [0, 1, 18, 999.9, 1000, 1000.1, 5000]) {
      const face = ballFaceState({
        speed: speed, speedText: "18", caught: caught,
      });
      assert.notEqual(
        face.petVisible,
        face.ballVisible,
        `caught=${caught} speed=${speed} 时两个主体不得同时显示`
      );
    }
  }
});

test("ballFaceState: 宠物模式有速度才跑，速度越快周期越短", function () {
  const idle = ballFaceState({ speed: 0, speedText: "0", caught: false });
  assert.equal(idle.running, false);
  assert.equal(idle.runSeconds, 0);

  const slow = ballFaceState({ speed: 10, speedText: "10", caught: false });
  const mid = ballFaceState({ speed: 120, speedText: "120", caught: false });
  const fast = ballFaceState({ speed: 400, speedText: "400", caught: false });
  assert.equal(slow.running, true);
  assert.equal(mid.running, true);
  assert.equal(fast.running, true);
  assert.ok(slow.runSeconds > mid.runSeconds);
  assert.ok(mid.runSeconds > fast.runSeconds);
  assert.equal(slow.runTier, 2);
  assert.equal(mid.runTier, 3);
  assert.equal(fast.runTier, 4);
  assert.deepEqual(slow.amplitude, { bob: 2.2, tilt: 6, stride: 1.2 });
  assert.deepEqual(fast.amplitude, { bob: 7, tilt: 20, stride: 4.2 });
  assert.equal(fast.runSeconds, 0.14);
  // 封顶后不再变快。
  const capped = ballFaceState({ speed: 5000, speedText: "5.0k", caught: false });
  assert.equal(capped.runSeconds, fast.runSeconds);
});

test("ballFaceState: 精灵球模式有速度才晃，速度越快周期越短", function () {
  const idle = ballFaceState({ speed: 0, speedText: "0", caught: true });
  assert.equal(idle.shaking, false);
  assert.equal(idle.shakeSeconds, 0);

  const slow = ballFaceState({ speed: 50, speedText: "50", caught: true });
  const fast = ballFaceState({ speed: 900, speedText: "900", caught: true });
  assert.equal(slow.shaking, true);
  assert.equal(fast.shaking, true);
  assert.ok(slow.shakeSeconds > fast.shakeSeconds);
  assert.ok(fast.shakeSeconds >= 0.22);
  assert.equal(ballFaceState({ speed: 400, speedText: "400", caught: true }).shakeSeconds, 0.22);
});

test("ballFaceState: 捕获态与运动互不串台", function () {
  // 宠物模式：即使速度很快也不会有晃动周期。
  const petFast = ballFaceState({ speed: 900, speedText: "900", caught: false });
  assert.equal(petFast.shaking, false);
  assert.equal(petFast.shakeSeconds, 0);

  // 精灵球模式：即使速度很快也不会有跑动周期。
  const ballFast = ballFaceState({ speed: 400, speedText: "400", caught: true });
  assert.equal(ballFast.running, false);
  assert.equal(ballFast.runSeconds, 0);
});

test("ballFaceState: reduced-motion 只停动画，不隐藏主体、不改速度", function () {
  const pet = ballFaceState({
    speed: 120, speedText: "120", caught: false, reducedMotion: true,
  });
  assert.equal(pet.petVisible, true);
  assert.equal(pet.running, false);
  assert.equal(pet.runSeconds, 0.3);
  assert.equal(pet.runTier, 3);
  assert.equal(pet.speedFontPx, 11);

  const ball = ballFaceState({
    speed: 800, speedText: "800", caught: true, reducedMotion: true,
  });
  assert.equal(ball.ballVisible, true);
  assert.equal(ball.shaking, false);
  assert.ok(ball.shakeSeconds > 0);
});

test("ballFaceState: 只有精灵球模式达到 400（满环）才自动放出", function () {
  assert.equal(
    ballFaceState({ speed: 399.9, speedText: "400", caught: true }).autoRelease,
    false
  );
  assert.equal(
    ballFaceState({ speed: 400, speedText: "400", caught: true }).autoRelease,
    true
  );
  // 宠物没被捕获：没有「放出」这回事，页面不应写状态或广播事件。
  assert.equal(
    ballFaceState({ speed: 5000, speedText: "5.0k", caught: false }).autoRelease,
    false
  );
});

test("ballFaceState: 非法输入退回安全默认值", function () {
  for (const input of [undefined, null, {}]) {
    const face = ballFaceState(input);
    assert.deepEqual(face.amplitude, { bob: 0, tilt: 0, stride: 0 });
    assert.equal(face.runTier, 0);
    assert.equal(face.petVisible, true);
    assert.equal(face.ballVisible, false);
    assert.equal(face.running, false);
    assert.equal(face.shaking, false);
    assert.equal(face.autoRelease, false);
    assert.equal(face.speedFontPx, BALL_SPEED_FONT_PX);
  }
  const negative = ballFaceState({ speed: -50, speedText: "18", caught: false });
  assert.equal(negative.running, false);
  assert.equal(negative.runSeconds, 0);
  const textual = ballFaceState({ speed: "120", speedText: "120", caught: false });
  assert.equal(textual.running, true);
});

test("ballSpeedFontPx: 3 位以内 11、4 位 10、5 位以上 9", function () {
  assert.equal(ballSpeedFontPx("0"), 11);
  assert.equal(ballSpeedFontPx("18"), 11);
  assert.equal(ballSpeedFontPx("999"), 11);
  // 1000+ 实际是 "1.2k"/"9.9k" 这类带小数点与 k 的 4 字符形态，
  // 按字符数（不是纯数字位数）分档，等宽字体下每字符 1ch 仍放得下。
  assert.equal(ballSpeedFontPx("1.2k"), 10);
  assert.equal(ballSpeedFontPx("9.9k"), 10);
  assert.equal(ballSpeedFontPx("123M"), 10);
  assert.equal(ballSpeedFontPx("18.3k"), 9);
  assert.equal(ballSpeedFontPx(undefined), 11);
  assert.equal(BALL_SPEED_FONT_PX, 11);
  assert.equal(BALL_PET_SIZE, 26);
});