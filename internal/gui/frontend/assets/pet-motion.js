// 宠物动作与技能参数的纯数学层：不依赖 three / DOM，node 单测可以直接覆盖。
//
// 这里集中两类参数：
// 1. gazePose：鼠标位置 → 头/颈/身体的转角。之前的问题是把「幅度」写死在
//    调用点（0.62 / 0.3 再乘 0.14 的权重），指针横穿舞台也只转到 10 度左右，
//    看起来像没反应。这里用一张显式的角度表 + 非线性曲线，保证小位移也有
//    可读的偏转、边缘不越界。
// 2. skillProfile：技能类型 + 强度档位 → 粒子数、环数、闪光、镜头冲击、
//    持续时间、颜色。档位 1-5 与「大招」在同一张表上单调递增，视觉差异由
//    参数保证，而不是靠调几个系数碰运气。
export const GAZE_LIMITS = {
  headYaw: 0.62,
  headPitch: 0.34,
  headRoll: 0.1,
  neckYaw: 0.3,
  neckPitch: 0.18,
  bodyYaw: 0.16,
  bodyPitch: 0.08,
  bodyRoll: 0.04,
  bodyShift: 0.06,
};

function clamp(value, min, max) {
  const number = Number(value);
  if (!Number.isFinite(number)) return 0;
  return Math.max(min, Math.min(max, number));
}

// signedCurve 保留符号地做幂次压缩：|x|<1 时被放大，边缘继续压向 ±1。
// 指数 < 1 意味着小位移更「敏感」——这正是「动的幅度太小」的解。
function signedCurve(value, exponent) {
  const x = clamp(value, -1, 1);
  return Math.sign(x) * Math.pow(Math.abs(x), exponent);
}

// gazePose 把归一化指针位置（-1..1）转成各关节的欧拉角（弧度）。
export function gazePose(aimX, aimY) {
  const x = signedCurve(aimX, 0.62);
  const y = signedCurve(aimY, 0.7);
  return {
    headYaw: x * GAZE_LIMITS.headYaw,
    headPitch: y * GAZE_LIMITS.headPitch,
    headRoll: -x * GAZE_LIMITS.headRoll,
    neckYaw: x * GAZE_LIMITS.neckYaw,
    neckPitch: y * GAZE_LIMITS.neckPitch,
    bodyYaw: x * GAZE_LIMITS.bodyYaw,
    bodyPitch: -y * GAZE_LIMITS.bodyPitch,
    bodyRoll: -x * GAZE_LIMITS.bodyRoll,
    bodyShift: x * GAZE_LIMITS.bodyShift,
  };
}

// 每种技能的「物理性格」：颜色、粒子初速、纵向加速度、尺寸/寿命区间。
// 颜色用十六进制数字，交给 three.Color 直接使用；纯测试也方便比较。
export const SKILL_THEMES = {
  electric: {
    colors: [0xfff3a0, 0xffd83d, 0x9fe8ff],
    ringColor: 0xffe259,
    flashColor: 0xdff6ff,
    lightColor: 0xffe25a,
    speed: 4.2,
    spread: 1.0,
    forward: 1.1,
    yAccel: 0.2,
    size: [0.08, 0.2],
    life: [0.32, 0.62],
    ringScale: 1.05,
  },
  ember: {
    colors: [0xffd166, 0xff8a3d, 0xd93b1e],
    ringColor: 0xff9a4d,
    flashColor: 0xffb066,
    lightColor: 0xff7a33,
    speed: 1.5,
    spread: 0.55,
    forward: 0.7,
    yAccel: 2.9,
    size: [0.12, 0.3],
    life: [0.55, 1.0],
    ringScale: 0.9,
  },
  water: {
    colors: [0x9fe6ff, 0x4ea9d7, 0xe8fbff],
    ringColor: 0x6fd3ff,
    flashColor: 0xbfe9ff,
    lightColor: 0x6fd3ff,
    speed: 2.5,
    spread: 0.85,
    forward: 1.0,
    yAccel: -4.0,
    size: [0.1, 0.26],
    life: [0.45, 0.85],
    ringScale: 1.15,
  },
  leaf: {
    colors: [0xa8e063, 0x63b978, 0xf1f7a8],
    ringColor: 0x8fdc63,
    flashColor: 0xd8f5b0,
    lightColor: 0x8fdc63,
    speed: 1.8,
    spread: 1.15,
    forward: 0.6,
    yAccel: -1.1,
    size: [0.1, 0.24],
    life: [0.6, 1.1],
    ringScale: 1.0,
  },
};

export const DEFAULT_SKILL_TYPE = "electric";
export const MAX_SKILL_POWER = 5;

// skillProfile 把技能类型与强度档位收敛成一份可执行的参数表。
// options.density 用于低配/软件渲染（<1 减少粒子），options.lowMotion 用于
// prefers-reduced-motion（粒子归零，只留闪光与一次环）。
export function skillProfile(type, intensity, ultimate, options) {
  const key = SKILL_THEMES[type] ? type : DEFAULT_SKILL_TYPE;
  const theme = SKILL_THEMES[key];
  const opts = options || {};
  const density = opts.lowMotion ? 0 : clamp(
    Number.isFinite(Number(opts.density)) ? Number(opts.density) : 1,
    0.2,
    1
  );
  const power = ultimate
    ? MAX_SKILL_POWER + 1
    : Math.max(1, Math.min(MAX_SKILL_POWER, Math.round(Number(intensity) || 1)));
  const boost = ultimate ? 1.7 : 1;
  const particles = Math.round((18 + (power - 1) * 22) * boost * density);
  return {
    type: key,
    ultimate: !!ultimate,
    power,
    // 档位 1 起步就有一圈可见冲击；3 档之后每级多一圈，大招 4 圈。
    rings: Math.min(4, 1 + Math.floor((power - 1) / 2) + (ultimate ? 1 : 0)),
    particles,
    // 画面上真正「亮一下」的强度：1 档几乎只有轮廓光，5 档/大招能照亮整只宠物。
    flash: Number((0.05 + (power - 1) * 0.035).toFixed(3)),
    // 镜头冲击（世界单位）：1-2 档不动镜头，3 档起才有，大招最明显。
    punch: power <= 2 ? 0 : Number(((power - 2) * 0.035).toFixed(3)),
    duration: Math.round((520 + power * 66) * (ultimate ? 1.5 : 1)),
    releaseAt: ultimate ? 0.24 : 0.18,
    colors: theme.colors.slice(),
    ringColor: theme.ringColor,
    flashColor: theme.flashColor,
    lightColor: theme.lightColor,
    speed: theme.speed * (1 + (power - 1) * 0.06),
    spread: theme.spread,
    forward: theme.forward,
    yAccel: theme.yAccel,
    size: theme.size.slice(),
    life: theme.life.slice(),
    ringScale: theme.ringScale * (1 + (power - 1) * 0.08),
    // 大招在首发之后追加一轮小规模二次爆发，读起来像「连击」。
    secondary: !!ultimate,
  };
}

// skillTimeline 给出技能动画的三段包络：
// windup（蓄力下沉，0→谷底）、release（命中闪光/镜头冲击，指数衰减）、
// settle（收尾回正）。
export function skillTimeline(progress) {
  const p = clamp(progress, 0, 1);
  const windup = p < 0.18 ? p / 0.18 : Math.max(0, 1 - (p - 0.18) / 0.1);
  const release = p < 0.18 ? 0 : Math.exp(-(p - 0.18) * 6.2);
  const settle = p > 0.66 ? 1 - (p - 0.66) / 0.34 : 1;
  return {
    windup: clamp(windup, 0, 1),
    release: clamp(release, 0, 1),
    settle: clamp(settle, 0, 1),
  };
}
// ---- 捕获（投精灵球）参数 ----
//
// 交互按棒球投球设计：按住蓄力，力量从 0 涨到 1，最佳区间在
// CAPTURE_SWEET_SPOT 附近——到点就松手命中率最高，蓄过头和没蓄够都会下滑。
// 概率与躲避都在这里算完，3D 层只负责把结果演出来；这样概率可单测、
// 也可以在 UI 上展示「完美 / 偏了」。
export const CAPTURE_CHARGE_SECONDS = 1.6;
export const CAPTURE_SWEET_SPOT = 0.72;
export const CAPTURE_SWEET_HALF_WIDTH = 0.08;

// captureCharge 把按住时长换算成 0..1 的蓄力值；超过满蓄力后停住不再增长。
export function captureCharge(heldMs, seconds) {
  const span = Math.max(0.2, Number(seconds) || CAPTURE_CHARGE_SECONDS) * 1000;
  return clamp(Number(heldMs) / span, 0, 1);
}

// captureAccuracy 把蓄力值映射成 0..1 的命中质量：
// 甜点区给满分，偏离越远越低，0 与 1 两端都只有 35%。
export function captureAccuracy(charge) {
  const value = clamp(charge, 0, 1);
  const distance = Math.abs(value - CAPTURE_SWEET_SPOT);
  if (distance <= CAPTURE_SWEET_HALF_WIDTH) {
    // 区间内保留一点坡度：正中心 1.0，区间边缘 0.92，手感有「更准」的差别。
    return 1 - (distance / CAPTURE_SWEET_HALF_WIDTH) * 0.08;
  }
  const falloff = (distance - CAPTURE_SWEET_HALF_WIDTH) /
    Math.max(0.001, 1 - CAPTURE_SWEET_HALF_WIDTH);
  return Math.max(0.35, 0.92 - Math.pow(falloff, 0.85) * 0.57);
}

// captureOutcome 用两个随机数决定「躲没躲开」与「抓没抓到」。
// 返回的 catchChance/dodgeChance 供 UI 显示概率，caught 是最终结果。
export function captureOutcome(options) {
  const opts = options || {};
  const accuracy = captureAccuracy(opts.charge);
  const tier = clamp(Math.round(Number(opts.tier) || 1), 1, 5);
  // 技能等级越高，宠物越警觉：躲避概率小幅上升（更强的伙伴更难抓）。
  const dodgeChance = Math.min(0.75,
    (0.06 + 0.4 * (1 - accuracy)) * (1 + (tier - 3) * 0.05));
  const catchChance = Math.min(0.98, 0.5 + 0.48 * accuracy - (tier - 1) * 0.015);
  const dodgeRoll = Number.isFinite(opts.dodgeRandom) ? opts.dodgeRandom : Math.random();
  const catchRoll = Number.isFinite(opts.catchRandom) ? opts.catchRandom : Math.random();
  const dodged = dodgeRoll < dodgeChance;
  const caught = !dodged && catchRoll < catchChance;
  return {
    accuracy,
    dodgeChance,
    catchChance,
    dodged,
    caught,
    // 供文案使用的粗略评级。
    rating: accuracy >= 0.92 ? "perfect" : accuracy >= 0.7 ? "good" : "wild",
  };
}

// autoSkillDelay 决定两次自动施法之间隔多久：等级越高越勤，但带随机抖动，
// 避免变成机械的节拍器。返回值毫秒。
export function autoSkillDelay(tier, random) {
  const level = clamp(Math.round(Number(tier) || 1), 1, 5);
  const roll = Number.isFinite(random) ? random : Math.random();
  const base = 7200 - level * 620;
  return Math.round(base + roll * 2600);
}
