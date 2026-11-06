import assert from "node:assert/strict";
import test from "node:test";

import {
  DOCK_CONTENT_SHIFT,
  dockContentShift,
  dockSideForWindow,
} from "../frontend/assets/ball-dock.js";

// 贴边态：Go 把 44px 窗口有一半放到屏幕外；页面只能靠窗口矩形与工作区
// 推断贴边侧，再把 18px 主体往屏幕内移半宽。这里用真实坐标跑表。

const PRIMARY = {
  availLeft: 0,
  availTop: 0,
  availWidth: 1920,
  availHeight: 1040,
};

function at(screenX, screenY, width = 44, height = 44, area = PRIMARY) {
  return dockSideForWindow({
    screenX: screenX, screenY: screenY,
    width: width, height: height,
    availLeft: area.availLeft, availTop: area.availTop,
    availWidth: area.availWidth, availHeight: area.availHeight,
  });
}

test("dockSideForWindow: 四条边都能识别", function () {
  assert.equal(at(-22, 300), "left");
  assert.equal(at(PRIMARY.availWidth - 22, 300), "right");
  assert.equal(at(500, -22), "top");
  assert.equal(at(500, PRIMARY.availHeight - 22), "bottom");
});

test("dockSideForWindow: 浮动态（完全在工作区内）返回空串", function () {
  assert.equal(at(100, 100), "");
  // 正好贴边但没越界：Go 的钳制会把它留在工作区内，不算贴边态。
  assert.equal(at(PRIMARY.availWidth - 44, 300), "");
  assert.equal(at(500, PRIMARY.availHeight - 44), "");
  assert.equal(at(0, 300), "");
});

test("dockSideForWindow: 角落优先判定水平边", function () {
  // 左上角同时越界：Go 的最近边判定会选左右，页面推断也应一致。
  assert.equal(at(-22, -22), "left");
  assert.equal(at(PRIMARY.availWidth - 22, -22), "right");
});

test("dockSideForWindow: 左侧副屏（负坐标）同样能识别", function () {
  const leftScreen = {
    availLeft: -1920, availTop: 0, availWidth: 1920, availHeight: 1040,
  };
  assert.equal(at(-1942, 300, 44, 44, leftScreen), "left");
  assert.equal(at(-22, 300, 44, 44, leftScreen), "right");
});

test("dockSideForWindow: 非法/缺失尺寸回退为不贴边", function () {
  for (const input of [undefined, null, {}, { screenX: 0 }]) {
    assert.equal(dockSideForWindow(input), "");
  }
  assert.equal(at(0, 0, 0, 44), "");
  assert.equal(at(0, 0, 44, 0), "");
  const zeroArea = { availLeft: 0, availTop: 0, availWidth: 0, availHeight: 0 };
  assert.equal(at(-22, 300, 44, 44, zeroArea), "");
  // 字符串数字按 Number 归一。
  assert.equal(dockSideForWindow({
    screenX: "-22", screenY: "300", width: "44", height: "44",
    availLeft: "0", availTop: "0", availWidth: "1920", availHeight: "1040",
  }), "left");
});

test("dockContentShift: 往屏幕内移半宽，浮动态不偏移", function () {
  assert.equal(DOCK_CONTENT_SHIFT, 9);
  assert.deepEqual(dockContentShift("left"), { x: 9, y: 0 });
  assert.deepEqual(dockContentShift("right"), { x: -9, y: 0 });
  assert.deepEqual(dockContentShift("top"), { x: 0, y: 9 });
  assert.deepEqual(dockContentShift("bottom"), { x: 0, y: -9 });
  assert.deepEqual(dockContentShift(""), { x: 0, y: 0 });
  assert.deepEqual(dockContentShift("nonsense"), { x: 0, y: 0 });
});