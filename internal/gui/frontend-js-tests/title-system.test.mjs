import assert from "node:assert/strict";
import test from "node:test";

import {
  titleThemeOrder,
  titleIconMarkup,
  titleThemeSigilMarkup,
} from "../frontend/assets/title-system.js";

// 每个主题的 13 个等级各用一枚专属图标：从徽记 SVG 里取出 <svg> 内的 path
// 片段，同主题内任意两级不得画成同一张图。
function sigilInner(markup) {
  const match = markup.match(/<svg[^>]*>([\s\S]*?)<\/svg>/);
  return match ? match[1] : null;
}

test("十三级图标在同一主题内两两不同", function () {
  for (const theme of titleThemeOrder) {
    const seen = new Map();
    for (let level = 1; level <= 13; level++) {
      const inner = sigilInner(titleIconMarkup(theme, level));
      assert.ok(inner, `${theme} 第 ${level} 级应渲染出 SVG 徽记`);
      assert.equal(
        seen.has(inner) ? seen.get(inner) : level,
        level,
        `${theme} 第 ${level} 级与第 ${seen.get(inner)} 级图标重复`
      );
      seen.set(inner, level);
    }
    assert.equal(seen.size, 13, `${theme} 应有 13 枚互不相同的等级图标`);
  }
});

test("待机也有专属图标徽记", function () {
  for (const theme of titleThemeOrder) {
    const idle = sigilInner(titleIconMarkup(theme, 0));
    assert.ok(idle && idle.length > 0, `${theme} 待机也应渲染出 SVG 徽记`);
    assert.match(idle, /<(path|circle|rect)/, `${theme} 待机徽记应含描边图元`);
  }
});

test("图标 markup 带等级环进度，但不带等级数字", function () {
  const markup = titleIconMarkup("career", 6, { active: true, progress: 42.5 });
  assert.match(markup, /--title-rank:42\.5%/);
  assert.match(markup, /title-icon-current/);
  assert.doesNotMatch(markup, /title-rank-count/);
  assert.doesNotMatch(markup, />06</);
});

test("themeSigil 选项可回退到主题统一徽记", function () {
  const themed = titleIconMarkup("vehicles", 6, { themeSigil: true });
  const plain = titleThemeSigilMarkup("vehicles", "title-rank-sigil");
  assert.equal(sigilInner(themed), sigilInner(plain));
});

test("非法主题回退到 vehicles，不抛异常", function () {
  const fallback = titleIconMarkup("__nope__", 3);
  assert.ok(sigilInner(fallback));
  assert.equal(sigilInner(fallback), sigilInner(titleIconMarkup("vehicles", 3)));
});
