// 悬浮球 / 迷你宠物的手势与动画判定（纯函数，页面只负责 DOM 与计时器）。
// 抽出来是为了能用真实输入做表驱动单测：短按/长按/捕获/拖动升级、以及
// 「速度越快跑得越快 / 精灵球晃得越快 / 超速自动放出」都在这里定。

// CHARGE_THROW_MS 与页面保持一致：按住达到此时长即判定为长按（抛球）。
export const CHARGE_THROW_MS = 620;
// DRAG_START_PX 与页面保持一致：按住移动超过该距离升级为窗口拖动。
export const DRAG_START_PX = 6;
// AUTO_RELEASE_SPEED 是「精灵球被挣扎开」的阈值：超过它自动放出伙伴。
export const AUTO_RELEASE_SPEED = 1000;

/**
 * 判定一次「按下→弹起」的结果（用于宠物窗口）。
 * @param {object} input
 * @param {number} input.heldMs 按住时长（毫秒）。
 * @param {boolean} input.caught 伙伴是否已收进精灵球。
 * @param {boolean} input.capturing 3D 演出是否正在进行（投球/捕获动画）。
 * @returns {{action: "throw"|"release"|"skill"|"none", longPress: boolean}}
 */
export function resolvePetGesture(input) {
  const heldMs = Math.max(0, Number(input.heldMs) || 0);
  const longPress = heldMs >= CHARGE_THROW_MS;
  if (input.capturing) return { action: "none", longPress };
  // 已捕获：点一下就把伙伴放出来（长按也按放出处理，不重复投球）。
  if (input.caught) return { action: "release", longPress };
  if (longPress) return { action: "throw", longPress };
  return { action: "skill", longPress };
}

/**
 * 判定 pointermove 是否应升级为窗口拖动。
 * @param {object} input
 * @param {boolean} input.pressed 按钮当前是否按住。
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
 * 计算长按蓄力弧的进度（0-1）。
 * @param {number} heldMs
 * @returns {number}
 */
export function chargeProgress(heldMs) {
  return Math.min(1, Math.max(0, (Number(heldMs) || 0) / CHARGE_THROW_MS));
}

/**
 * 宠物奔跑动画的一步时长（秒）：速度越快越短（跑得越快）。
 * 0 速度返回 0（不跑）；1..400 tok/s 从 0.90s 收敛到 0.22s。
 * @param {number} speed
 * @returns {number} 秒；0 表示不播放
 */
export function runDuration(speed) {
  const value = Math.max(0, Number(speed) || 0);
  if (value <= 0) return 0;
  const t = Math.min(1, value / 400);
  return Math.round((0.9 - 0.68 * t) * 1000) / 1000;
}

/**
 * 精灵球晃动动画的一步时长（秒）：速度越快晃得越快。
 * 0 速度返回 0（静止）；1..1000 tok/s 从 0.80s 收敛到 0.25s。
 * @param {number} speed
 * @returns {number} 秒；0 表示不播放
 */
export function shakeDuration(speed) {
  const value = Math.max(0, Number(speed) || 0);
  if (value <= 0) return 0;
  const t = Math.min(1, value / AUTO_RELEASE_SPEED);
  return Math.round((0.8 - 0.55 * t) * 1000) / 1000;
}

/**
 * 速度超过阈值时，精灵球里的伙伴会自己挣出来。
 * @param {number} speed tok/s
 * @returns {boolean}
 */
export function shouldAutoRelease(speed) {
  return (Number(speed) || 0) > AUTO_RELEASE_SPEED;
}