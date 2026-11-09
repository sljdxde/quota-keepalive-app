// petAimPoint 把指针位置换算成 -1..1 的归一化朝向（相对宠物头部锚点）。
//
// 设计目标：指针从头部移到舞台边缘，对应轴恰好到 ±1——这样「跟得准」：
// 竖直移动得到干净的俯仰、水平移动得到干净的偏航，斜向按角度等比分配。
// 之前用固定的 0.22 / 0.30 分母，指针要走很远才勉强到 ±1，追踪发虚；
// 而且水平/竖直分母不一致，导致「往下看」永远比「往左右看」弱，头看着
// 一直停在默认的侧脸上。这里改成按「头到该侧边缘的实际距离」归一化。
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
  // 头部锚点：水平取中心，竖直取眼睛所在（约 0.42 高度，略偏上）。
  const headX = avatarRect.left - stageRect.left + avatarRect.width * 0.5;
  const headY = avatarRect.top - stageRect.top + avatarRect.height * 0.42;
  const x = clientX - stageRect.left;
  const y = clientY - stageRect.top;
  // 每个方向单独取「头到该侧舞台边缘的距离」做分母，保证移到边缘即 ±1；
  // clamp 到下限防止极窄舞台被放大过头。
  const rightSpan = Math.max(40, stageRect.width - headX);
  const leftSpan = Math.max(40, headX);
  const downSpan = Math.max(40, stageRect.height - headY);
  const upSpan = Math.max(40, headY);
  const dx = x - headX;
  const dy = y - headY;
  const normX = dx >= 0 ? dx / rightSpan : dx / leftSpan;
  const normY = dy >= 0 ? dy / downSpan : dy / upSpan;
  return {
    x: Math.max(-1, Math.min(1, normX)),
    y: Math.max(-1, Math.min(1, normY)),
  };
}
