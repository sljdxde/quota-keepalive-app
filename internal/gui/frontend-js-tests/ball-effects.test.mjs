import assert from "node:assert/strict";
import test from "node:test";

import {
  EFFECT_TIER_STARTS,
  PET_EFFECTS,
  ballEffectsState,
  effectTierForSpeed,
  hopDuration,
} from "../frontend/assets/ball-effects.js";

// ballEffectsState 是球面蛙跳/特效的唯一判定：与 ball-face.js 一样抽成
// 纯函数，用真实输入跑表，不 mock DOM。

test("hopDuration: 0 速度不跳，其余按 sqrt 曲线单调变快", function () {
  assert.equal(hopDuration(0), 0);
  assert.equal(hopDuration(-5), 0);
  const speeds = [1, 10, 60, 120, 200, 320, 400, 500];
  const values = speeds.map(hopDuration);
  for (let i = 1; i < values.length; i++) {
    assert.ok(values[i] <= values[i - 1], `${speeds[i]} 的周期不应比低速长`);
  }
  // 400 及以上收敛到最快周期（0.20s），不再无限变快。
  assert.equal(hopDuration(400), 0.2);
  assert.equal(hopDuration(5000), 0.2);
  // 低速慢跳、高速快跳，约 4 倍差距，肉眼可分。
  assert.ok(values[0] / values[5] > 2.5);
});

test("effectTierForSpeed: 100 / 300 两个边界", function () {
  assert.equal(effectTierForSpeed(0), 0);
  assert.equal(effectTierForSpeed(99.9), 0);
  assert.equal(effectTierForSpeed(100), 1);
  assert.equal(effectTierForSpeed(299.9), 1);
  assert.equal(effectTierForSpeed(300), 2);
  assert.equal(effectTierForSpeed(400), 2);
  assert.equal(effectTierForSpeed(1000), 2);
  assert.deepEqual(EFFECT_TIER_STARTS, [0, 100, 300]);
});

test("PET_EFFECTS: 四宠各自两档特效齐全", function () {
  for (const key of ["ember", "electric", "leaf", "water"]) {
    const pair = PET_EFFECTS[key];
    assert.ok(pair, `缺少 ${key}`);
    assert.ok(pair.tier1 && pair.tier2, `${key} 两档都要有`);
    assert.notEqual(pair.tier1, pair.tier2, `${key} 两档不能同名`);
  }
  assert.equal(PET_EFFECTS.ember.tier1, "smoke");
  assert.equal(PET_EFFECTS.ember.tier2, "flame");
  assert.equal(PET_EFFECTS.electric.tier2, "arc");
  assert.equal(PET_EFFECTS.leaf.tier2, "vine");
  assert.equal(PET_EFFECTS.water.tier2, "jet");
});

test("ballEffectsState: 四宠在 100 / 300 两档拿到各自特效", function () {
  const cases = [
    { effect: "ember", v: 120, want: "smoke" },
    { effect: "ember", v: 320, want: "flame" },
    { effect: "electric", v: 120, want: "spark" },
    { effect: "electric", v: 320, want: "arc" },
    { effect: "leaf", v: 120, want: "seed" },
    { effect: "leaf", v: 320, want: "vine" },
    { effect: "water", v: 120, want: "drop" },
    { effect: "water", v: 320, want: "jet" },
  ];
  for (const c of cases) {
    const state = ballEffectsState({ speed: c.v, effect: c.effect });
    assert.equal(state.tier, c.v >= 300 ? 2 : 1);
    assert.equal(state.effect, c.want);
    assert.equal(state.active, true);
    assert.equal(state.hopping, true);
    assert.ok(state.hopSeconds > 0);
  }
});

test("ballEffectsState: 静止 / 收进球 / 减少动效一律不播放", function () {
  const inputs = [
    { speed: 0, effect: "ember" },
    { speed: 320, effect: "ember", caught: true },
    { speed: 320, effect: "ember", reducedMotion: true },
  ];
  for (const input of inputs) {
    const state = ballEffectsState(input);
    assert.equal(state.hopping, false);
    assert.equal(state.hopSeconds, 0);
    assert.equal(state.tier, 0);
    assert.equal(state.effect, "");
    assert.equal(state.active, false);
  }
});

test("ballEffectsState: 未识别的 effect 回退到小火龙系，不抛错", function () {
  const state = ballEffectsState({ speed: 320, effect: "unknown" });
  assert.equal(state.active, true);
  assert.equal(state.effect, "flame");
});

test("ballEffectsState: 蛙跳周期随速度变快，但特效档位只按 100/300 跳变", function () {
  const slow = ballEffectsState({ speed: 120, effect: "ember" });
  const mid = ballEffectsState({ speed: 200, effect: "ember" });
  const fast = ballEffectsState({ speed: 320, effect: "ember" });
  assert.ok(slow.hopSeconds > mid.hopSeconds);
  assert.ok(mid.hopSeconds > fast.hopSeconds);
  // 120 与 200 同属轻档，只有频率不同。
  assert.equal(slow.tier, mid.tier);
  assert.equal(slow.effect, mid.effect);
  assert.notEqual(mid.tier, fast.tier);
});
