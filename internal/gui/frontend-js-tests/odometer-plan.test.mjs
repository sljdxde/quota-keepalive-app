import assert from "node:assert/strict";
import test from "node:test";

import { extraLoopsFor, planColumns } from "../frontend/assets/odometer.js";

test("same-length digits keep every column", function () {
  const steps = planColumns(
    [..."235"],
    [..."947"],
  );
  assert.deepEqual(
    steps.map(function (s) { return s.type; }),
    ["keep", "keep", "keep"],
  );
  assert.deepEqual(
    steps.map(function (s) { return s.ch; }),
    ["9", "4", "7"],
  );
  // old 下标指向旧序列从左数的槽位，供引擎按位取列。
  assert.deepEqual(
    steps.map(function (s) { return s.old; }),
    [0, 1, 2],
  );
});

test("growing digits enter on the left and keep the tail", function () {
  // 85 → 128：右对齐后个位/十位保留，新增的百位入场。
  const steps = planColumns([..."85"], [..."128"]);
  assert.deepEqual(steps, [
    { type: "enter", ch: "1" },
    { type: "keep", ch: "2", old: 0 },
    { type: "keep", ch: "8", old: 1 },
  ]);
});

test("shrinking digits exit on the left", function () {
  const steps = planColumns([..."128"], [..."85"]);
  assert.deepEqual(steps, [
    { type: "exit", ch: "", old: 0 },
    { type: "keep", ch: "8", old: 1 },
    { type: "keep", ch: "5", old: 2 },
  ]);
});

test("crossing 1k replaces matched slots and enters the leading digit", function () {
  // 999 → 1.0k：右对齐配对——旧个位 '9'→'k' 替换、旧十位→'0' 保留、
  // 旧百位→'.' 替换；'1' 从右数超出旧长度（3 位），入场。
  const steps = planColumns([..."999"], [..."1.0k"]);
  assert.deepEqual(steps, [
    { type: "enter", ch: "1" },
    { type: "replace", ch: ".", old: 0 },
    { type: "keep", ch: "0", old: 1 },
    { type: "replace", ch: "k", old: 2 },
  ]);
});

test("losing the decimal point exits the tail slot", function () {
  // 9.9 → 10：右对齐配对——旧个位 '9'→'0' 保留、旧 '.'→'1' 替换、
  // 旧最左 '9' 超出新长度，退场。
  const steps = planColumns([..."9.9"], [..."10"]);
  assert.deepEqual(steps, [
    { type: "exit", ch: "", old: 0 },
    { type: "replace", ch: "1", old: 1 },
    { type: "keep", ch: "0", old: 2 },
  ]);
});

test("equal length but different shape replaces in place", function () {
  // 235 → 9.5：长度相同但数字/固定符位置不同，中间槽位替换。
  const steps = planColumns([..."235"], [..."9.5"]);
  assert.deepEqual(steps, [
    { type: "keep", ch: "9", old: 0 },
    { type: "replace", ch: ".", old: 1 },
    { type: "keep", ch: "5", old: 2 },
  ]);
});

test("identical text yields all-keep steps in aligned coordinates", function () {
  // 引擎在 set() 里对相同文本提前返回，planColumns 不会收到相同序列；
  // 即便收到，语义也是全 keep、old 逐位对齐，不产生入场/退场。
  assert.deepEqual(planColumns([..."1.2k"], [..."1.2k"]), [
    { type: "keep", ch: "1", old: 0 },
    { type: "keep", ch: ".", old: 1 },
    { type: "keep", ch: "2", old: 2 },
    { type: "keep", ch: "k", old: 3 },
  ]);
});

test("extra loops scale with duration so fast sampling stays nimble", function () {
  // 采样密（时长短）只走半圈，时长充裕才走整圈。
  assert.equal(extraLoopsFor(110), 0.5);
  assert.equal(extraLoopsFor(260), 0.5);
  assert.equal(extraLoopsFor(420), 1);
  assert.equal(extraLoopsFor(520), 1);
  assert.equal(extraLoopsFor(undefined), 0.5);
  assert.equal(extraLoopsFor(0), 0.5);
});
