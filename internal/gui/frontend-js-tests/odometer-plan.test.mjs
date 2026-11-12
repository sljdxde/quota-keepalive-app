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

test("extra loops always one full lap, for every duration", function () {
  // 恒走一整圈：0.5 圈会让每列偏移 5（见下一条回归），0 圈会让快档退化成
  // 跳变、丢掉连滚观感。任何采样档位都绕一圈：快档 110ms 走 10 位依然利落。
  assert.equal(extraLoopsFor(110), 1);
  assert.equal(extraLoopsFor(260), 1);
  assert.equal(extraLoopsFor(420), 1);
  assert.equal(extraLoopsFor(520), 1);
  assert.equal(extraLoopsFor(undefined), 1);
  assert.equal(extraLoopsFor(0), 1);
});

// 回归：额外圈数必须是整数。reel 是 0-9 循环，走半圈会让每列偏移 5 个数字，
// 实际显示值变成 (目标+5)%10 —— 209 会显示成 754，而菜单栏与 tab 徽标走纯文本
// 不受影响，于是表现为「面板大数字和其他位置不一致」。
test("extra loops are whole turns so displayed digits stay correct", function () {
  const DIGITS = "0123456789";
  // 与 rollColumn 相同的位移推导：从归一化起点 0 出发，前进 forward 步再加整圈。
  function shownDigit(target, extra) {
    const forward = ((target - 0) % 10 + 10) % 10;
    const to = forward + extra * 10;
    return DIGITS[((to % 10) + 10) % 10];
  }
  for (const extra of [extraLoopsFor(110), extraLoopsFor(260), extraLoopsFor(520)]) {
    assert.equal(Number.isInteger(extra), true, `额外圈数必须是整数，实际 ${extra}`);
    for (let target = 0; target <= 9; target++) {
      assert.equal(
        shownDigit(target, extra),
        String(target),
        `extra=${extra} 时目标 ${target} 显示成了 ${shownDigit(target, extra)}`
      );
    }
  }
});
