// 滑动胶囊的 zoom 换算测试。
//
// 背景（实测）：150% 缩放的笔记本上界面缩放 0.85 时，白色滑动胶囊只有 30px
// 高，而按钮是 35.7px——胶囊比文字小、整体上移。原因是 liquid-ui.js 用
// getBoundingClientRect() 取几何（返回值已含 zoom），又写回被 zoom 的上下文，
// 等于缩放两次（0.85² ≈ 0.72）。这里验证换算函数把几何还原回元素自己的 CSS px。

import test from "node:test";
import assert from "node:assert/strict";

// 与 liquid-ui.js 中的实现保持一致的纯函数版本（该文件是浏览器脚本，未导出内部
// 函数；这里复制逻辑做回归保护，尺寸/行为改动需同步）。
function splitGeometry(buttonRect, rootRect, zoom) {
  const scale = zoom > 0 ? zoom : 1;
  return {
    x: (buttonRect.left - rootRect.left) / scale,
    y: (buttonRect.top - rootRect.top) / scale,
    width: buttonRect.width / scale,
    height: buttonRect.height / scale,
  };
}

const rect = (left, top, width, height) => ({ left, top, width, height });

test("未缩放时时几何原样透传", () => {
  const g = splitGeometry(rect(103, 77, 100, 28), rect(100, 74, 400, 34), 1);
  assert.deepEqual(g, { x: 3, y: 3, width: 100, height: 28 });
});

test("zoom 0.85 时还原成元素 CSS px（胶囊与按钮等高）", () => {
  // rect 已被 zoom 缩放：28 * 0.85 = 23.8
  const g = splitGeometry(rect(103 * 0.85, 77 * 0.85, 100 * 0.85, 28 * 0.85),
    rect(100 * 0.85, 74 * 0.85, 400 * 0.85, 34 * 0.85), 0.85);
  assert.equal(Math.round(g.width), 100);
  assert.equal(Math.round(g.height), 28);
  assert.equal(Math.round(g.x), 3);
  assert.equal(Math.round(g.y), 3);
});

test("缩放两次的错误不会再出现（0.85² 会缩成 72%）", () => {
  const g = splitGeometry(rect(0, 0, 28 * 0.85, 28 * 0.85), rect(0, 0, 1, 1), 0.85);
  assert.equal(Math.round(g.height), 28);
  // 反例：若不换算，写回后渲染高度只有 28 * 0.85 * 0.85 ≈ 20.2
  assert.notEqual(Math.round(g.height * 0.85 * 0.85), 28);
});

test("非法 zoom 视为 1，不产生 NaN/Infinity", () => {
  for (const bad of [0, -1, NaN, undefined]) {
    const g = splitGeometry(rect(1, 2, 3, 4), rect(0, 0, 0, 0), bad);
    assert.deepEqual(g, { x: 1, y: 2, width: 3, height: 4 });
  }
});
