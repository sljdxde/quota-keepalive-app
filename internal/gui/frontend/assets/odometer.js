// 速度数字连滚引擎（odometer）：每一位是一条 0-9 竖向 reel，数值变化时
// 按「到目标的路径 + 额外整圈」连续滚过多位，像机械里程表；位数变化
// （85↔128、999→1.0k）不再整组重建——新列宽度展开、从 0 滚入，消失列
// 宽度收起、淡出后移除。
//
// 取自 design/win-ui/number-roll-prototype.html 的验证版引擎， replacing
// 旧的单步 setRoll。主窗口速度页（speed-display 模式的 roll 分支）、悬浮球
// bar.html 与菜单栏面板（assets/panel.js）三处共用；显示方式设置（roll/
// fade/direct）由各调用方自己接，本文件只负责 roll 的连滚渲染。
//
// 约束（沿用既定规则）：首次渲染不播动画；位数更新被打断时先落位上一目标
// 再起滚，不产生累积漂移；reduced-motion 全部退化为直接落位；没有
// infinite 动画——只有数值变化触发的 transform / width / opacity 过渡。
const DIGITS = "0123456789";
const STRIP_COPIES = 5; // reel 重复遍数：最多「两圈+单步」= 30 位位移
const EASE_SPIN = "cubic-bezier(.6,.05,.28,1)"; // 多位连滚：缓入缓出，机芯感
const EASE_STEP = "cubic-bezier(.22,.9,.24,1)"; // 单步：缓出
const GROW_MS = 320; // 列宽展开/收起的时长

function isDigit(ch) {
  return ch >= "0" && ch <= "9";
}

/**
 * 结构 diff（右对齐逐位配对，里程表对位）：把旧字符序列变成新序列需要的
 * 最小操作。返回步骤数组，type：
 *   keep    沿用旧列并滚到 ch（old = 旧序列下标）
 *   enter   新增列（宽度展开、从 0 滚入 ch）
 *   exit    旧列退场（宽度收起、淡出后移除；old = 旧序列下标）
 *   replace 旧列槽位字符类型变了（如数字位变 "k"）：旧列退场 + 新列入场
 * 纯函数，导出供单测。
 * @param {string[]} oldChars
 * @param {string[]} newChars
 * @returns {Array<{type:string, ch:string, old?:number}>}
 */
export function planColumns(oldChars, newChars) {
  const steps = [];
  const maxLen = Math.max(oldChars.length, newChars.length);
  for (let i = 0; i < maxLen; i++) { // i = 从右数第 i 位
    const oc = i < oldChars.length ? oldChars[oldChars.length - 1 - i] : null;
    const nch = i < newChars.length ? newChars[newChars.length - 1 - i] : null;
    const oldIdx = oldChars.length - 1 - i; // 旧序列从左数下标（cols 数组坐标系）
    if (oc !== null && nch === null) steps.unshift({ type: "exit", ch: "", old: oldIdx });
    else if (oc === null && nch !== null) steps.unshift({ type: "enter", ch: nch });
    else if (oc === nch) steps.unshift({ type: "keep", ch: nch, old: oldIdx });
    else if (isDigit(oc) === isDigit(nch)) steps.unshift({ type: "keep", ch: nch, old: oldIdx });
    else steps.unshift({ type: "replace", ch: nch, old: oldIdx });
  }
  return steps;
}

/**
 * 每次更新的额外整圈数：恒为 1。两条约束的共同结论：
 * 1) 必须是整数：reel 是 0-9 的循环，只有走满整圈才回到同一数字。早期返回
 *    0.5（「半圈」）会让每列正好偏移 5 个数字——209 会显示成 754（3f87dc3）。
 * 2) 不能是 0：「快档不绕圈」会让 200ms 采样档（rollDurationFor 给 110ms）
 *    退化成快速跳变，「数字不断滚动」的里程表观感是本组件存在的意义。
 *    快档 110ms 走 10 位依然利落，慢档（260-520ms）一圈更从容。
 *
 * duration 由调用方按采样间隔自适应（主窗口走 --roll-duration，即
 * speed-display.js 的 rollDurationFor 公式；悬浮球没设时回退 SPIN_MS）。
 *
 * @param {number} duration
 * @returns {number} 额外整圈数（恒为 1）
 */
export function extraLoopsFor(duration) {
  void duration;
  return 1;
}

// 默认时长：球面与精简面板没有 --roll-duration，用这个；主窗口覆盖它。
const SPIN_MS = 520;

const rollers = new WeakMap(); // el → roller（闭包状态；元素内容被外部改写后失配重建）

function createRoller(el) {
  let text = null;
  let cols = [];    // {kind:"d",cell,reel,pos,logical,settled,entering} | {kind:"f",cell}
  let pending = []; // 出场列：收起动画结束后移除
  function makeDigit(startCh) {
    const cell = document.createElement("span"); cell.className = "cell";
    const reel = document.createElement("span"); reel.className = "reel";
    for (let c = 0; c < STRIP_COPIES; c++) {
      for (const d of DIGITS) {
        const s = document.createElement("span"); s.textContent = d;
        reel.appendChild(s);
      }
    }
    cell.appendChild(reel);
    const pos = Number(startCh);
    reel.style.transition = "none";
    reel.style.transform = "translateY(" + -pos + "em)";
    void reel.offsetHeight;
    reel.style.transition = "";
    return { kind: "d", cell, reel, pos, logical: pos, settled: true, entering: false };
  }

  function makeFix(ch) {
    const cell = document.createElement("span"); cell.className = "fix"; cell.textContent = ch;
    return { kind: "f", cell };
  }

  function place(col, pos, withTransition) {
    col.reel.style.transition = withTransition ? "" : "none";
    col.reel.style.transform = "translateY(" + -pos + "em)";
    col.pos = pos;
    if (!withTransition) void col.reel.offsetHeight;
  }

  function rollColumn(col, target, cfg, dir) {
    if (!col.settled) place(col, col.logical, false); // 打断：先落位上一目标
    // 归一化起步区（视觉不变：模 10 同位）
    const norm = ((col.pos % 10) + 10) % 10;
    let from = norm;
    if (dir < 0 && from < 10) from += 30; // 反向滚预留向下空间
    if (from !== col.pos) place(col, from, false);

    const forward = ((target - from) % 10 + 10) % 10;
    let dist = dir < 0 ? forward - 10 : forward;
    dist += cfg.extra * 10;
    const to = from + dist;
    col.logical = target;

    if (Math.round(dist) === 0) { place(col, target, false); col.settled = true; return; }
    col.settled = false;
    const ease = Math.abs(dist) > 10 || dir < 0 ? EASE_SPIN : EASE_STEP;
    col.reel.style.transition = "transform " + cfg.duration + "ms " + ease;
    col.reel.style.transform = "translateY(" + -to + "em)";
    col.pos = to;
    const onEnd = function (ev) {
      if (ev.propertyName !== "transform") return;
      col.reel.removeEventListener("transitionend", onEnd);
      place(col, ((col.pos % 10) + 10) % 10, false);
      col.settled = true;
    };
    col.reel.addEventListener("transitionend", onEnd);
  }

  function build(next) { // 静态重建：首帧 / 跳变 / reduced-motion
    finalizePending();
    el.textContent = "";
    cols = [];
    for (const ch of next.split("")) cols.push(isDigit(ch) ? makeDigit(ch) : makeFix(ch));
    for (const col of cols) el.appendChild(col.cell);
    text = next;
  }

  function finalizePending() { // 收掉上一轮的入场/出场与在途滚动（隐藏窗口恢复后自愈）
    for (const col of pending.splice(0)) col.cell.remove();
    for (const col of cols) {
      if (col.kind !== "d") continue;
      if (col.entering) {
        col.cell.style.width = ""; col.cell.style.opacity = "";
        col.entering = false;
        place(col, col.logical, false); col.settled = true;
      } else if (!col.settled) place(col, col.logical, false);
    }
  }

  function set(next, cfg, reduced) {
    next = String(next);
    if (next === text) return;
    if (text === null || reduced || !cfg.spin) { build(next); return; }
    finalizePending();

    // 结构 diff：按「从右数第 i 位」配对。同型列保留并滚动；新列宽度展开 +
    // 从 0 滚入；消失列宽度收起 + 淡出后移除。
    const steps = planColumns(text.split(""), next.split(""));
    const children = [], newCols = [], entries = [], exits = [], rolls = [];
    let oldIdx = 0;
    for (const step of steps) {
      if (step.type === "exit" || step.type === "replace") {
        const oc = cols[step.old];
        if (oc) { exits.push(oc); children.push(oc.cell); }
      }
      if (step.type === "keep") {
        const oc = cols[step.old];
        if (oc) {
          if (oc.kind === "d") rolls.push([oc, Number(step.ch)]);
          else if (oc.cell.textContent !== step.ch) oc.cell.textContent = step.ch;
          newCols.push(oc); children.push(oc.cell);
        }
      }
      if (step.type === "enter" || step.type === "replace") {
        const col = isDigit(step.ch) ? makeDigit("0") : makeFix(step.ch);
        if (col.kind === "d") { col.entering = true; rolls.push([col, Number(step.ch), true]); }
        entries.push(col); newCols.push(col); children.push(col.cell);
      }
    }

    const structureChanged = entries.length > 0 || exits.length > 0;
    if (structureChanged) {
      el.replaceChildren(...children);
      for (const col of entries) { col.cell.style.width = "0ch"; col.cell.style.opacity = "0"; }
      void el.offsetWidth; // 提交入场初始态
      for (const col of exits) {
        col.cell.style.width = "0ch";
        col.cell.style.opacity = "0";
        pending.push(col);
        const cell = col.cell;
        const onEnd = function (ev) {
          if (ev.propertyName !== "width") return;
          cell.removeEventListener("transitionend", onEnd);
          cell.remove();
          const k = pending.indexOf(col);
          if (k >= 0) pending.splice(k, 1);
        };
        cell.addEventListener("transitionend", onEnd);
      }
      for (const col of entries) {
        col.cell.style.width = ""; // 回到类的列宽（.62em / 1ch）
        col.cell.style.opacity = "";
        const cell = col.cell;
        const onEnd = function (ev) {
          if (ev.propertyName !== "width") return;
          cell.removeEventListener("transitionend", onEnd);
          col.entering = false;
        };
        cell.addEventListener("transitionend", onEnd);
      }
    }
    for (const [col, target, fresh] of rolls) {
      if (fresh) { col.pos = 0; col.logical = 0; col.settled = true; }
      rollColumn(col, target, cfg, 1); // 始终前进：机械里程表感
    }
    text = next;
    cols = newCols;
  }

  return {
    el,
    set,
    settle: finalizePending,
  };
}

function reducedMotion() {
  return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
}

/**
 * 把 el 渲染成 value 的连滚数字。
 * @param {HTMLElement|null} el
 * @param {string|number} value
 * @param {{duration?:number, extra?:number, spin?:boolean}} [opts]
 *   duration 覆盖默认时长；extra 覆盖额外整圈数；spin=false 直接落位。
 */
export function setRoll(el, value, opts) {
  if (!el) return;
  const next = String(value);
  const options = opts || {};
  let roller = rollers.get(el);
  if (!roller) { roller = createRoller(el); rollers.set(el, roller); }
  // 元素内容被外部改写过（如显示模式切走又切回）：列已脱离 DOM，重建。
  if (roller.el !== el || (roller.cols && roller.cols.length &&
      roller.cols[0].cell && roller.cols[0].cell.parentNode !== el)) {
    roller = createRoller(el); rollers.set(el, roller);
  }

  let duration = Number(options.duration);
  if (!Number.isFinite(duration) || duration <= 0) {
    const cssValue = parseFloat(getComputedStyle(el).getPropertyValue("--roll-duration"));
    duration = Number.isFinite(cssValue) && cssValue > 0 ? cssValue : SPIN_MS;
  }
  let extra = Number(options.extra);
  if (!Number.isFinite(extra)) extra = extraLoopsFor(duration);

  roller.el = el;
  roller.set(next, { duration: Math.round(duration), extra, spin: options.spin !== false }, reducedMotion());
  el.dataset.text = next; // 供调试与外部清理逻辑参照（旧实现的 data-text）
}
