// 数字滚动（odometer）：每一位是一列 0-9，数值变化时纵向滚到目标数字。
// 取自 design/win-ui/prototype.html 的 setRoll，供悬浮球与精简面板共用。
// 非数字字符（小数点、k/M/万/亿）按固定字符渲染，不参与滚动。
const DIGITS = "0123456789";

function isDigit(ch) {
  return ch >= "0" && ch <= "9";
}

/**
 * 把 el 渲染成 value 的滚动数字。位数变化时重建 DOM；首次落位不播放动画，
 * 避免从 0 滚上来。
 * @param {HTMLElement|null} el
 * @param {string|number} value
 */
export function setRoll(el, value) {
  if (!el) return;
  const text = String(value);
  if (el.dataset.text === text) return;

  const chars = text.split("");
  const needRebuild = el.dataset.len !== String(chars.length) || !el.querySelector(".reel");

  if (needRebuild) {
    el.innerHTML = chars
      .map(function (ch) {
        if (!isDigit(ch)) return '<span class="fix">' + ch + "</span>";
        return (
          '<span class="cell"><span class="reel">' +
          DIGITS.split("")
            .map(function (d) {
              return "<span>" + d + "</span>";
            })
            .join("") +
          "</span></span>"
        );
      })
      .join("");
    el.dataset.len = String(chars.length);

    const reels = Array.from(el.querySelectorAll(".reel"));
    let index = 0;
    chars.forEach(function (ch) {
      if (!isDigit(ch)) return;
      reels[index].style.transition = "none";
      reels[index].style.transform = "translateY(" + -Number(ch) + "em)";
      index++;
    });
    // 强制回流后再恢复过渡，避免首帧就滚。
    void el.offsetHeight;
    reels.forEach(function (reel) {
      reel.style.transition = "";
    });
  } else {
    const reels = Array.from(el.querySelectorAll(".reel"));
    let index = 0;
    chars.forEach(function (ch) {
      if (!isDigit(ch)) return;
      reels[index].style.transform = "translateY(" + -Number(ch) + "em)";
      index++;
    });
  }
  el.dataset.text = text;
}