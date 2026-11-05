// petAimPoint 把指针位置换算成 -1..1 的归一化朝向。
//
// 归一化分母决定「移动多少像素算看满」：之前用舞台宽度的 0.38 / 高度的 0.5，
// 指针横穿整块舞台也到不了 ±1，再乘上关节权重后几乎看不出转头。现在按头到
// 舞台边缘的实际比例取 0.22 / 0.30，并保留最小值兜住窄舞台。
export function petAimPoint(clientX, clientY, stageRect, avatarRect) {
  if (!stageRect || !avatarRect ||
      !Number.isFinite(clientX) || !Number.isFinite(clientY) ||
      stageRect.width <= 0 || stageRect.height <= 0 ||
      avatarRect.width <= 0 || avatarRect.height <= 0) {
    return null;
  }
  if (clientX < stageRect.left || clientX > stageRect.right ||
      clientY < stageRect.top || clientY > stageRect.bottom) {
    return null;
  }
  const headX = avatarRect.left - stageRect.left + avatarRect.width * 0.5;
  const headY = avatarRect.top - stageRect.top + avatarRect.height * 0.42;
  const x = clientX - stageRect.left;
  const y = clientY - stageRect.top;
  return {
    x: Math.max(-1, Math.min(1,
      (x - headX) / Math.max(44, stageRect.width * 0.22))),
    y: Math.max(-1, Math.min(1,
      (y - headY) / Math.max(34, stageRect.height * 0.3))),
  };
}