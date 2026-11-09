// 悬浮球「蛙跳 + 速度特效」的纯判定：页面只把这里的结果落到 DOM。
// 抽出来是为了能用真实输入做表驱动单测（周期、档位、伙伴特效），
// 不依赖 DOM / Wails runtime。

// 特效档位阈值：100 tok/s 起「跑起来」出轻特效，300 起越跑越急出重特效。
// 400 是称号环满环 / 精灵球自动放出的既有阈值，保持不变，重特效延续到其上。
export const EFFECT_TIER_STARTS = [0, 100, 300];

// 宠物主题特效：与 pokemon-art.js 的 effect 字段对应。
// tier 1 = 轻（脚下/身后零星一团），tier 2 = 重（长拖尾）。
export const PET_EFFECTS = {
  ember: { tier1: "smoke", tier2: "flame" },
  electric: { tier1: "spark", tier2: "arc" },
  leaf: { tier1: "seed", tier2: "vine" },
  water: { tier1: "drop", tier2: "jet" },
};

/**
 * 速度 → 特效档位。0 静止无特效 / 1 轻 / 2 重。
 * @param {number} speed tok/s
 * @returns {number} 0..2
 */
export function effectTierForSpeed(speed) {
  const value = Math.max(0, Number(speed) || 0);
  if (value < EFFECT_TIER_STARTS[1]) return 0;
  if (value < EFFECT_TIER_STARTS[2]) return 1;
  return 2;
}

/**
 * 速度 → 一次蛙跳的周期（秒）。快慢只由频率表现，幅度固定。
 * sqrt 曲线把常见速度（10~120）的节奏差拉开：
 * 0.84s/跳（约 71 跳/分，慢）→ 0.20s/跳（约 300 跳/分，快），约 4.2 倍。
 * 0 速度返回 0（不跳）。
 * @param {number} speed tok/s
 * @returns {number} 秒；0 表示不播放
 */
export function hopDuration(speed) {
  const value = Math.max(0, Number(speed) || 0);
  if (value <= 0) return 0;
  const t = Math.min(1, Math.sqrt(value / 400));
  return Math.round((0.84 - 0.64 * t) * 100) / 100;
}

/**
 * 球面特效状态。收进精灵球 / 减少动效 / 静止时都不出特效。
 * @param {object} input
 * @param {number} input.speed tok/s
 * @param {boolean} input.caught 伙伴是否在精灵球里
 * @param {boolean} input.reducedMotion 系统是否要求减少动效
 * @param {string} input.effect pokemon-art.js 的 effect（ember/electric/leaf/water）
 * @returns {{
 *   hopping: boolean, hopSeconds: number,
 *   tier: number, effect: string,
 *   active: boolean
 * }}
 */
export function ballEffectsState(input) {
  const source = input || {};
  const speed = Math.max(0, Number(source.speed) || 0);
  const caught = !!source.caught;
  const reducedMotion = !!source.reducedMotion;
  const seconds = hopDuration(speed);
  const petEffect = PET_EFFECTS[source.effect] || PET_EFFECTS.ember;
  const tier = effectTierForSpeed(speed);
  // 静止 / 精灵球模式 / 减少动效：三种情况都不播放连续动画
  // （置顶小窗 60fps 重绘曾带着整屏闪，静止时零动画是既有约束）。
  const active = !caught && !reducedMotion && seconds > 0;
  return {
    hopping: active,
    hopSeconds: active ? seconds : 0,
    tier: active ? tier : 0,
    effect: active ? petEffect[tier === 2 ? "tier2" : "tier1"] : "",
    active: active,
  };
}
