import assert from "node:assert/strict";
import test from "node:test";

import {
  CAPTURE_SWEET_SPOT,
  GAZE_LIMITS,
  autoSkillDelay,
  captureAccuracy,
  captureCharge,
  captureOutcome,
  gazePose,
  skillProfile,
  skillTimeline,
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

test("captureCharge grows with hold time and stops at full", function () {
  assert.equal(captureCharge(0, 1.6), 0);
  assert.equal(captureCharge(800, 1.6), 0.5);
  assert.equal(captureCharge(1600, 1.6), 1);
  assert.equal(captureCharge(9000, 1.6), 1);
});

test("captureAccuracy peaks at the sweet spot and drops off both sides", function () {
  const perfect = captureAccuracy(CAPTURE_SWEET_SPOT);
  const inner = captureAccuracy(CAPTURE_SWEET_SPOT - 0.05);
  const weak = captureAccuracy(0.05);
  const overcharged = captureAccuracy(1);
  assert.ok(perfect >= 0.99);
  assert.ok(inner < perfect);
  assert.ok(weak < inner);
  assert.ok(overcharged < inner);
  assert.ok(captureAccuracy(-3) === captureAccuracy(0));
});

test("captureOutcome turns accuracy into dodge / catch results", function () {
  const perfect = captureOutcome({
    charge: CAPTURE_SWEET_SPOT, tier: 1, dodgeRandom: 0.99, catchRandom: 0.01,
  });
  assert.equal(perfect.dodged, false);
  assert.equal(perfect.caught, true);
  assert.equal(perfect.rating, "perfect");

  const dodged = captureOutcome({
    charge: CAPTURE_SWEET_SPOT, tier: 1, dodgeRandom: 0, catchRandom: 0,
  });
  assert.equal(dodged.dodged, true);
  assert.equal(dodged.caught, false);

  const missed = captureOutcome({
    charge: 0.02, tier: 1, dodgeRandom: 0.99, catchRandom: 0.99,
  });
  assert.equal(missed.caught, false);
  assert.equal(missed.rating, "wild");

  // 高等级宠物更警觉：同样蓄力下躲避概率不比低等级低。
  const tier1 = captureOutcome({ charge: 0.3, tier: 1, dodgeRandom: 1, catchRandom: 1 });
  const tier5 = captureOutcome({ charge: 0.3, tier: 5, dodgeRandom: 1, catchRandom: 1 });
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