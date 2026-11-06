// 悬浮球面两种模式的纯判定：宠物模式（未捕获）/ 精灵球模式（已捕获）。
// 页面（bar.html）只把这里的结果落到 DOM，单测用真实输入跑同一套表：
// 互斥显隐、跑动/晃动节奏、超速自动放出、速度字号。
import {
  runDuration,
  shakeDuration,
  shouldAutoRelease,
} from "./bar-gesture.js";

// 与 ball.css 对齐：球面宠物/精灵球 24px，速度基准字号 16px。
export const BALL_PET_SIZE = 24;
export const BALL_SPEED_FONT_PX = 16;

/**
 * 速度数字的字号：3 位以内 16px，4 位 14px，5 位以上 12px。
 * @param {string|number} text 已格式化的速度文本（如 "18" / "1.2k" / "18.3k"）
 * @returns {number} 像素字号
 */
export function ballSpeedFontPx(text) {
  const length = String(text == null ? "" : text).length;
  if (length >= 5) return 12;
  if (length === 4) return 14;
  return BALL_SPEED_FONT_PX;
}

/**
 * 球面状态：宠物与精灵球互斥；动画节奏只跟速度（以及是否捕获）走。
 * @param {object} input
 * @param {number} input.speed tok/s
 * @param {string} input.speedText 已格式化的速度文本
 * @param {boolean} input.caught 伙伴是否在精灵球里
 * @param {boolean} input.reducedMotion 系统是否要求减少动效
 * @returns {{
 *   petVisible: boolean, ballVisible: boolean,
 *   running: boolean, shaking: boolean,
 *   runSeconds: number, shakeSeconds: number,
 *   autoRelease: boolean, speedFontPx: number,
 * }}
 */
export function ballFaceState(input) {
  const source = input || {};
  const speed = Math.max(0, Number(source.speed) || 0);
  const caught = !!source.caught;
  const reducedMotion = !!source.reducedMotion;
  const runSeconds = caught ? 0 : runDuration(speed);
  const shakeSeconds = caught ? shakeDuration(speed) : 0;
  return {
    petVisible: !caught,
    ballVisible: caught,
    running: !reducedMotion && runSeconds > 0,
    shaking: !reducedMotion && shakeSeconds > 0,
    runSeconds: runSeconds,
    shakeSeconds: shakeSeconds,
    // 只有精灵球模式才有「被挣开」：普通宠物模式不写回状态、不广播事件。
    autoRelease: caught && shouldAutoRelease(speed),
    speedFontPx: ballSpeedFontPx(source.speedText),
  };
}