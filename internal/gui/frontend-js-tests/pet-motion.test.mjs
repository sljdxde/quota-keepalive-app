import assert from "node:assert/strict";
import test from "node:test";

import {
  CAPTURE_SWEEP,
  GAZE_LIMITS,
  autoSkillDelay,
  captureOutcome,
  gazePose,
  skillProfile,
  skillTimeline,
  sweepProgress,
  timingAccuracy,
} from "../frontend/assets/pet-motion.js";

test("gazePose amplifies small pointer moves and clamps at the edges", function () {
  const center = gazePose(0, 0);
  assert.equal(center.headYaw, 0);
  assert.equal(center.headPitch, 0);

  // 小位移（0.2）也要有明显偏转：旧的实现约 0.12 弧度，肉眼几乎看不出。
  const small = gazePose(0.2, 0);
  assert.ok(Math.abs(small.headYaw) > 0.14);
  assert.ok(Math.abs(small.headYaw) < GAZE_LIMITS.headYaw);

  const edge = gazePose(1, 1);
  assert.ok(Math.abs(edge.headYaw - GAZE_LIMITS.headYaw) < 1e-9);
  assert.ok(Math.abs(edge.headPitch - GAZE_LIMITS.headPitch) < 1e-9);
  // 越界输入按边界裁剪，不能把脖子拧过头。
  const over = gazePose(4, -4);
  assert.equal(over.headYaw, edge.headYaw);
  assert.equal(over.headPitch, -edge.headPitch);
});

test("gazePose is symmetric and shares work between neck, head and body", function () {
  const left = gazePose(-0.6, -0.3);
  const right = gazePose(0.6, 0.3);
  assert.ok(Math.abs(left.headYaw + right.headYaw) < 1e-9);
  assert.ok(left.headRoll > 0 && right.headRoll < 0);
  // 头拿最大一份，脖子次之，身体只负责重心。
  assert.ok(Math.abs(right.headYaw) > Math.abs(right.neckYaw));
  assert.ok(Math.abs(right.neckYaw) > Math.abs(right.bodyYaw));
});

test("captureOutcome turns accuracy into dodge / catch results", function () {
  const perfect = captureOutcome({
    accuracy: 1, tier: 1, dodgeRandom: 0.99, catchRandom: 0.01,
  });
  assert.equal(perfect.dodged, false);
  assert.equal(perfect.caught, true);
  assert.equal(perfect.rating, "perfect");

  const dodged = captureOutcome({
    accuracy: 1, tier: 1, dodgeRandom: 0, catchRandom: 0,
  });
  assert.equal(dodged.dodged, true);
  assert.equal(dodged.caught, false);

  const missed = captureOutcome({
    accuracy: 0.2, tier: 1, dodgeRandom: 0.99, catchRandom: 0.99,
  });
  assert.equal(missed.caught, false);
  assert.equal(missed.rating, "wild");

  // 高等级宠物更警觉：同样蓄力下躲避概率不比低等级低。
  const tier1 = captureOutcome({ accuracy: 0.4, tier: 1, dodgeRandom: 1, catchRandom: 1 });
  const tier5 = captureOutcome({ accuracy: 0.4, tier: 5, dodgeRandom: 1, catchRandom: 1 });
  assert.ok(tier5.dodgeChance >= tier1.dodgeChance);
});

test("skillProfile scales every tier monotonically and ultimate tops it", function () {
  const tiers = [1, 2, 3, 4, 5].map(function (tier) {
    return skillProfile("electric", tier, false);
  });
  for (let index = 1; index < tiers.length; index += 1) {
    assert.ok(tiers[index].particles > tiers[index - 1].particles);
    assert.ok(tiers[index].flash > tiers[index - 1].flash);
    assert.ok(tiers[index].duration > tiers[index - 1].duration);
  }
  assert.equal(tiers[0].punch, 0);
  assert.ok(tiers[4].punch > 0);

  const ultimate = skillProfile("electric", 5, true);
  assert.ok(ultimate.particles > tiers[4].particles);
  assert.ok(ultimate.flash > tiers[4].flash);
  assert.ok(ultimate.rings >= tiers[4].rings);
  assert.equal(ultimate.secondary, true);

  // 类型各自有颜色与物理性格，不是同一套参数换名字。
  const types = ["electric", "ember", "water", "leaf"].map(function (type) {
    return skillProfile(type, 3, false);
  });
  const colors = new Set(types.map(function (profile) { return profile.colors[0]; }));
  assert.equal(colors.size, 4);
  assert.notEqual(types[0].yAccel, types[1].yAccel);
});

test("skillProfile honors low-motion and density for weak machines", function () {
  const normal = skillProfile("water", 5, false);
  const low = skillProfile("water", 5, false, { density: 0.55 });
  const reduced = skillProfile("water", 5, false, { lowMotion: true });
  assert.ok(low.particles < normal.particles);
  assert.equal(reduced.particles, 0);
  assert.ok(reduced.rings >= 1);
  // 未知技能类型退回默认类型，不能产生 undefined 特效。
  assert.equal(skillProfile("unknown", 2, false).type, "electric");
});

test("skillTimeline gives windup, a release spike and a settle tail", function () {
  assert.equal(skillTimeline(0).release, 0);
  const peak = skillTimeline(0.18);
  assert.ok(peak.release > 0.9);
  assert.ok(skillTimeline(0.6).release < 0.1);
  assert.ok(skillTimeline(0.6).settle > 0);
  assert.ok(skillTimeline(1).settle < 0.01);
});

test("autoSkillDelay shortens with tier and keeps a random jitter", function () {
  const slow = autoSkillDelay(1, 0);
  const fast = autoSkillDelay(5, 0);
  assert.ok(fast < slow);
  assert.ok(autoSkillDelay(3, 1) > autoSkillDelay(3, 0));
});
test("sweepProgress starts slow, accelerates, and ping-pongs inside 0..1", function () {
  const start = sweepProgress(0);
  assert.equal(start.position, 0);
  assert.ok(Math.abs(start.speed - CAPTURE_SWEEP.baseSpeed) < 1e-9);

  const slow = sweepProgress(400);
  const fast = sweepProgress(2200);
  assert.ok(fast.speed > slow.speed);
  assert.ok(fast.position >= 0 && fast.position <= 1);
  // 位置来回摆动：连续采样不会越界。
  for (let ms = 0; ms <= 4000; ms += 90) {
    const sample = sweepProgress(ms);
    assert.ok(sample.position >= 0 && sample.position <= 1);
  }
  // 速度存在封顶，否则按住越久会变成闪屏。
  assert.ok(sweepProgress(100000).speed <= CAPTURE_SWEEP.maxSpeed + 1e-9);
});

test("timingAccuracy is a narrow rhythm window around PERFECT", function () {
  const center = CAPTURE_SWEEP.perfectCenter;
  const half = CAPTURE_SWEEP.perfectHalfWidth;
  assert.equal(timingAccuracy(center), 1);
  assert.ok(timingAccuracy(center + half * 0.9) > 0.9);
  assert.ok(timingAccuracy(center + half * 3) < 0.55);
  assert.ok(timingAccuracy(0) < 0.3);
  // 窄带只占刻度条的 9%，不能做成「随便按都完美」。
  assert.ok(half * 2 < 0.12);
  // 越靠近中心，判定单调变好。
  const near = timingAccuracy(center + half * 0.2);
  const far = timingAccuracy(center + half * 1.5);
  assert.ok(near > far);
});

test("captureOutcome accepts a rhythm accuracy directly", function () {
  const perfect = captureOutcome({
    accuracy: 1, tier: 1, dodgeRandom: 0.99, catchRandom: 0,
  });
  assert.equal(perfect.caught, true);
  assert.equal(perfect.rating, "perfect");
  const wild = captureOutcome({
    accuracy: 0.2, tier: 3, dodgeRandom: 0, catchRandom: 0,
  });
  assert.equal(wild.dodged, true);
  assert.equal(wild.rating, "wild");
  assert.equal(wild.caught, false);
});
