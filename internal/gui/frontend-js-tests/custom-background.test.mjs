import assert from "node:assert/strict";
import test from "node:test";

// 为 custom-background.js 提供最小 DOM / 存储 mock。测试在 import 之前
// 把全局装好，模块的函数体在运行期才读这些全局。
const store = new Map();
const bodyClasses = new Set();
const cssVars = new Map();

globalThis.localStorage = {
  getItem(key) { return store.has(key) ? store.get(key) : null; },
  setItem(key, value) { store.set(key, String(value)); },
  removeItem(key) { store.delete(key); },
  clear() { store.clear(); },
};
globalThis.document = {
  body: {
    classList: {
      toggle(name, force) { force ? bodyClasses.add(name) : bodyClasses.delete(name); },
    },
  },
  documentElement: {
    style: {
      setProperty(name, value) { cssVars.set(name, value); },
      removeProperty(name) { cssVars.delete(name); },
    },
  },
  createElement() { throw new Error("canvas 不应在纯逻辑测试里被创建"); },
};

const {
  BACKGROUND_CUSTOM,
  BACKGROUND_DEFAULT,
  applyCustomBackground,
  clearCustomImage,
  customBackgroundData,
  hasCustomImage,
  saveCustomImage,
} = await import("../frontend/assets/custom-background.js");

function reset() {
  store.clear();
  bodyClasses.clear();
  cssVars.clear();
}

test("默认常量与 schema 选项一致", function () {
  assert.equal(BACKGROUND_CUSTOM, "custom");
  assert.equal(BACKGROUND_DEFAULT, "mist");
});

test("无图片时选 custom 不会挂自定义背景类", function () {
  reset();
  applyCustomBackground(BACKGROUND_CUSTOM);
  assert.equal(bodyClasses.has("has-custom-bg"), false);
  assert.equal(cssVars.has("--custom-image"), false);
  assert.equal(hasCustomImage(), false);
});

test("保存图片后选 custom 会铺图并写入 --custom-image", function () {
  reset();
  store.set("qka.custom-background", "data:image/jpeg;base64,AAAA");
  applyCustomBackground(BACKGROUND_CUSTOM);
  assert.equal(bodyClasses.has("has-custom-bg"), true);
  assert.equal(cssVars.get("--custom-image"), 'url("data:image/jpeg;base64,AAAA")');
  assert.equal(hasCustomImage(), true);
  assert.equal(customBackgroundData(), "data:image/jpeg;base64,AAAA");
});

test("切回内置背景会移除自定义背景类与变量", function () {
  reset();
  store.set("qka.custom-background", "data:image/jpeg;base64,AAAA");
  applyCustomBackground(BACKGROUND_CUSTOM);
  applyCustomBackground("mist");
  assert.equal(bodyClasses.has("has-custom-bg"), false);
  assert.equal(cssVars.has("--custom-image"), false);
});

test("非法文件类型/过大文件在压缩前就被拒绝", async function () {
  reset();
  const badType = await saveCustomImage({ type: "text/plain", size: 100 });
  assert.match(badType, /PNG/);
  const tooBig = await saveCustomImage({ type: "image/png", size: 9 * 1024 * 1024 });
  assert.match(tooBig, /8 MB/);
  assert.equal(store.size, 0);
});

test("清除图片后 hasCustomImage 为假", function () {
  reset();
  store.set("qka.custom-background", "data:image/jpeg;base64,AAAA");
  clearCustomImage();
  assert.equal(hasCustomImage(), false);
});
