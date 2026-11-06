// 悬浮球手势判定（纯函数，bar.html 使用；DOM 与计时器都留在页面里）。
// 抽出来是为了能用真实输入做表驱动单测：短按/长按/贴边态/拖动升级的
// 全部分支都在 bar-gesture.test.mjs 里覆盖。

// CHARGE_THROW_MS 与 bar.html 保持一致：按住达到此时长即判定为长按（抛球）。
export const CHARGE_THROW_MS = 620;
// DRAG_START_PX 与 bar.html 保持一致：按住移动超过该距离升级为窗口拖动。
export const DRAG_START_PX = 6;

/**
 * 判定一次「按下→弹起」的结果。
 * @param {object} input
 * @param {number} input.heldMs 按住时长（毫秒）。
 * @param {boolean} input.docked 球是否处于贴边态（44px 半圆）。
 * @param {boolean} input.caught 伙伴是否已收进精灵球。
 * @param {boolean} input.capturing 3D 演出是否正在进行（投球/捕获动画）。
 * @param {boolean} input.moved 是否已升级为窗口拖动（拖动中弹起不算手势）。
 * @param {boolean} [input.hasPet] 是否有可用宠物（无宠物时短按无动作）。
 * @returns {{action: "idle"|"dock-toggle"|"throw"|"release"|"skill"|"none",
 *            longPress: boolean}} action 为页面应执行的动作。
 */
export function resolveGesture(input) {
  const heldMs = Math.max(0, Number(input.heldMs) || 0);
  const longPress = heldMs >= CHARGE_THROW_MS;

  // 拖动升级后弹起：手势已在 startNativeDrag 中消费，什么都不做。
  if (input.moved) return { action: "none", longPress };

  // 贴边态窗口只有半颗球，容不下宠物交互：短按 = 弹回 + 开迷你宠物。
  if (input.docked) {
    return { action: input.moved ? "none" : "dock-toggle", longPress };
  }

  if (longPress) return { action: "throw", longPress };
  if (input.caught) return { action: "release", longPress };
  if (input.hasPet && !input.capturing) return { action: "skill", longPress };
  return { action: "none", longPress };
}

/**
 * 判定 pointermove 是否应升级为窗口拖动。
 * @param {object} input
 * @param {number} input.pressed 按钮当前是否按住。
 * @param {number} input.buttons 事件里的 buttons 位掩码（1=左键）。
 * @param {number} input.dx 距按下点的 X 偏移。
 * @param {number} input.dy 距按下点的 Y 偏移。
 * @returns {boolean}
 */
export function shouldStartDrag(input) {
  if (!input.pressed) return false;
  if ((Number(input.buttons) || 0) !== 1) return false;
  return Math.abs(Number(input.dx) || 0) >= DRAG_START_PX ||
    Math.abs(Number(input.dy) || 0) >= DRAG_START_PX;
}

/**
 * 把鼠标事件坐标换算成「球心对中到指针下」的目标窗口位置。
 * 球窗口很小（64/44px），把窗口中心挪到指针处，视觉上就是「指针抓住球」。
 * @param {object} input
 * @param {number} input.clientX 指针相对窗口的 X。
 * @param {number} input.clientY 指针相对窗口的 Y。
 * @param {number} input.windowW 窗口宽。
 * @param {number} input.windowH 窗口高。
 * @param {number} input.screenX 窗口当前屏幕 X。
 * @param {number} input.screenY 窗口当前屏幕 Y。
 * @returns {{x: number, y: number}} 目标窗口屏幕坐标。
 */
export function dragTargetOrigin(input) {
  const w = Math.max(1, Number(input.windowW) || 1);
  const h = Math.max(1, Number(input.windowH) || 1);
  const dx = w / 2 - (Number(input.clientX) || 0);
  const dy = h / 2 - (Number(input.clientY) || 0);
  return {
    x: Math.round((Number(input.screenX) || 0) + dx),
    y: Math.round((Number(input.screenY) || 0) + dy),
  };
}

/**
 * 计算长按蓄力弧的进度（0-1）：驱动环内细弧的 stroke-dashoffset。
 * 线性增长、封顶 1；无动画，只随时间变化。
 * @param {number} heldMs
 * @returns {number}
 */
export function chargeProgress(heldMs) {
  return Math.min(1, Math.max(0, (Number(heldMs) || 0) / CHARGE_THROW_MS));
}
