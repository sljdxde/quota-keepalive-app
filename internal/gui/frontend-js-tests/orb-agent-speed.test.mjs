import assert from "node:assert/strict";
import test from "node:test";

import { createAgentSpeedOrb, orbSettled } from "../frontend/assets/orb-agent-speed.js";

// baseState 构造「完全静止」的渲染核状态：速度、目标、显示值都归零，
// 没有任何待播特效。各用例只覆盖需要变化的那一项。
function baseState(overrides) {
  return Object.assign({
    v: 0,
    target: 0,
    shown: 0,
    peakAge: 99,
    waves: [],
    parts: [],
    bolts: [],
    zaps: [],
    arcs: [],
  }, overrides || {});
}

test("静止状态：速度、目标、显示值与特效全部归零时判定为可停帧", function () {
  assert.equal(orbSettled(baseState()), true);
  // 缺少 peakAge 视为没有破纪录特效，不阻止自停。
  assert.equal(orbSettled({ v: 0, target: 0 }), true);
});

test("有粒子时不停帧", function () {
  assert.equal(orbSettled(baseState({ parts: [{ age: 0.1, life: 1 }] })), false);
  // 鼠标/冲击波等其他特效队列同样算「未静止」。
  assert.equal(orbSettled(baseState({ waves: [{ age: 0.2 }] })), false);
  assert.equal(orbSettled(baseState({ bolts: [{ age: 0.01 }] })), false);
  assert.equal(orbSettled(baseState({ zaps: [{ age: 0.01 }] })), false);
  assert.equal(orbSettled(baseState({ arcs: [{ age: 0.01 }] })), false);
});

test("v 尚未衰减到 0 时不停帧", function () {
  assert.equal(orbSettled(baseState({ v: 0.2 })), false);
  assert.equal(orbSettled(baseState({ v: 0.04 })), true);
  // 目标值还没归零时也不能停，否则会在收敛前冻结。
  assert.equal(orbSettled(baseState({ target: 0.2 })), false);
});

test("显示值尚未收敛到 0 时不停帧", function () {
  assert.equal(orbSettled(baseState({ shown: 0.6 })), false);
});

test("破纪录特效播放期间不停帧", function () {
  assert.equal(orbSettled(baseState({ peakAge: 0.5 })), false);
  assert.equal(orbSettled(baseState({ peakAge: 1.3 })), false);
  assert.equal(orbSettled(baseState({ peakAge: 1.31 })), true);
});

test("空值/非法输入保守地视为未静止", function () {
  assert.equal(orbSettled(null), false);
  assert.equal(orbSettled(undefined), false);
});

test("模块导出工厂函数，且 node 下可安全 import", function () {
  assert.equal(typeof createAgentSpeedOrb, "function");
  assert.equal(typeof orbSettled, "function");
});
