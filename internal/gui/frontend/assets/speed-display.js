// 速度数字的显示方式：把「设置项字符串 → 渲染行为」这一段纯逻辑抽出来，
// 主窗口与菜单栏面板共用，避免两边各写一份 switch 后慢慢走样。
//
// 三种方式：
//   - roll   逐位滚轮（odometer），数字变化时每一位纵向滚到目标值
//   - fade   平滑缓动，数值在 240ms 内插值到目标值
//   - direct 直接替换，不做任何过渡
export const SPEED_DISPLAY_MODES = ["roll", "fade", "direct"];

// 未知值回退到逐位滚轮：它是默认值，也是「有速度感」最明显的一种。
export const SPEED_DISPLAY_DEFAULT = "roll";

// FADE_DURATION_MS 是平滑缓动的时长。太长会明显滞后于真实值，
// 太短又看不出过渡；240ms 与主窗口其他过渡（200-300ms）保持一致。
export const FADE_DURATION_MS = 240;

/**
 * 把设置值归一化成三种已知模式之一。
 * @param {unknown} value
 * @returns {"roll"|"fade"|"direct"}
 */
export function normalizeSpeedDisplay(value) {
  const text = String(value == null ? "" : value).trim();
  return SPEED_DISPLAY_MODES.indexOf(text) >= 0 ? text : SPEED_DISPLAY_DEFAULT;
}

/**
 * 三次缓出曲线：起步快、收尾慢，读起来像「数值稳住了」。
 * @param {number} progress 0-1
 * @returns {number}
 */
export function easeOutCubic(progress) {
  const clamped = Math.max(0, Math.min(1, Number(progress) || 0));
  return 1 - Math.pow(1 - clamped, 3);
}

/**
 * 计算缓动的中间值。
 * @param {number} from 起始值
 * @param {number} to 目标值
 * @param {number} progress 0-1
 * @returns {number}
 */
export function fadeValue(from, to, progress) {
  const start = Number(from) || 0;
  const end = Number(to) || 0;
  return start + (end - start) * easeOutCubic(progress);
}

/**
 * 判断这次是否需要播放动画。以下情况直接落位，不播动画：
 * - 模式是 direct
 * - 首次渲染（避免从 0 滚/缓动上来）
 * - 系统开了「减少动态效果」
 * @param {string} mode
 * @param {{ first: boolean, reducedMotion: boolean }} state
 * @returns {boolean}
 */
export function shouldAnimateSpeed(mode, state) {
  const current = state || {};
  if (normalizeSpeedDisplay(mode) === "direct") return false;
  if (current.first) return false;
  return !current.reducedMotion;
}

/**
 * 滚轮每一位的过渡时长：采样越快，动画要越短，否则永远追不上新值。
 * @param {number} sampleMs 采样间隔（毫秒）
 * @returns {number} 毫秒，限定在 90-260 之间
 */
export function rollDurationFor(sampleMs) {
  const sample = Number(sampleMs);
  if (!Number.isFinite(sample) || sample <= 0) return 110;
  return Math.round(Math.max(90, Math.min(260, sample * 0.55)));
}
