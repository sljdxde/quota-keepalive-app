// 悬浮球贴边态的「贴在哪条边」与内容偏移判定。
// Go 侧把 44px 窗口有一半放到屏幕外，只保留朝向屏幕内的半圆；页面拿不到
// Go 的贴边侧，只能根据窗口矩形与当前屏幕工作区推断。纯函数，便于单测。

// 贴边态内容（宠物/精灵球 18px）向屏幕内偏移半宽，避免正好被窗口边界切一半。
export const DOCK_CONTENT_SHIFT = 9;

/**
 * 根据窗口矩形与屏幕工作区判断贴边侧。
 * @param {object} input
 * @param {number} input.screenX 窗口左上角 X（CSS px）
 * @param {number} input.screenY 窗口左上角 Y
 * @param {number} input.width 窗口宽
 * @param {number} input.height 窗口高
 * @param {number} input.availLeft 工作区左边界
 * @param {number} input.availTop 工作区上边界
 * @param {number} input.availWidth 工作区宽
 * @param {number} input.availHeight 工作区高
 * @returns {""|"left"|"right"|"top"|"bottom"} 贴边侧；浮动态返回空串
 */
export function dockSideForWindow(input) {
  const source = input || {};
  const x = Number(source.screenX);
  const y = Number(source.screenY);
  const width = Number(source.width);
  const height = Number(source.height);
  const left = Number(source.availLeft);
  const top = Number(source.availTop);
  const availWidth = Number(source.availWidth);
  const availHeight = Number(source.availHeight);
  if (![x, y, width, height, left, top, availWidth, availHeight].every(Number.isFinite)) {
    return "";
  }
  if (width <= 0 || height <= 0 || availWidth <= 0 || availHeight <= 0) return "";
  const right = left + availWidth;
  const bottom = top + availHeight;
  // 贴边窗口有一半越出工作区；浮动态被 Go 钳制在工作区内，不会越界。
  if (x < left) return "left";
  if (x + width > right) return "right";
  if (y < top) return "top";
  if (y + height > bottom) return "bottom";
  return "";
}

/**
 * 贴边内容沿屏幕内侧方向偏移多少像素。
 * @param {string} side dockSideForWindow 的结果
 * @returns {{x: number, y: number}}
 */
export function dockContentShift(side) {
  switch (side) {
    case "left": return { x: DOCK_CONTENT_SHIFT, y: 0 };
    case "right": return { x: -DOCK_CONTENT_SHIFT, y: 0 };
    case "top": return { x: 0, y: DOCK_CONTENT_SHIFT };
    case "bottom": return { x: 0, y: -DOCK_CONTENT_SHIFT };
    default: return { x: 0, y: 0 };
  }
}