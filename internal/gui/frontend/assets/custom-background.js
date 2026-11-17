// 自定义背景：主窗口设置页与独立设置窗口共用。
// 图片经 canvas 压缩后存 localStorage（单源约 5 MB），只保存在本机。
// 98e4d2a 起内置背景是整窗极轻色调（body 铺 var(--bg)），自定义图片走
// body.has-custom-bg 铺 background-image；两个页面都要引同一份 CSS 规则
// （见各自页面内嵌样式）才能显示。

const CUSTOM_BACKGROUND_KEY = "qka.custom-background";
// 与 schema.go 里 background 的 custom 选项一致；数据存不进时不能把
// 设置切成 custom（会显示成一张空背景）。
export const BACKGROUND_CUSTOM = "custom";
export const BACKGROUND_DEFAULT = "mist";

// customBackgroundData 返回已保存的 dataURL（无图返回空串）。
export function customBackgroundData() {
  try {
    return localStorage.getItem(CUSTOM_BACKGROUND_KEY) || "";
  } catch (_) {
    return "";
  }
}

// hasCustomImage 是否已保存过自定义图片。
export function hasCustomImage() {
  return !!customBackgroundData();
}

// compressImageFile 把图片等比缩到最长相边 2400px、转 JPEG（PNG 透明处铺
// 白底）。原图 base64 极易超出 localStorage 配额且报错只是笼统的
// QuotaExceeded；压缩后实际存储通常 < 1 MB，对铺满全窗的背景足够清晰。
export function compressImageFile(file) {
  return new Promise(function (resolve, reject) {
    const reader = new FileReader();
    reader.onload = function () {
      const image = new Image();
      image.onload = function () {
        const maxEdge = 2400;
        const scale = Math.min(1, maxEdge / Math.max(image.width, image.height));
        const canvas = document.createElement("canvas");
        canvas.width = Math.max(1, Math.round(image.width * scale));
        canvas.height = Math.max(1, Math.round(image.height * scale));
        const context = canvas.getContext("2d");
        context.fillStyle = "#ffffff";
        context.fillRect(0, 0, canvas.width, canvas.height);
        context.drawImage(image, 0, 0, canvas.width, canvas.height);
        resolve(canvas.toDataURL("image/jpeg", 0.82));
      };
      image.onerror = function () { reject(new Error("decode")); };
      image.src = String(reader.result || "");
    };
    reader.onerror = function () { reject(new Error("read")); };
    reader.readAsDataURL(file);
  });
}

// saveCustomImage 压缩并保存图片；成功返回 null，失败返回给用户看的文案。
// 数据写不进 localStorage 时不改设置值，让界面保持在原背景上。
export async function saveCustomImage(file) {
  if (!/^image\/(png|jpeg|webp|gif)$/.test(file.type) || file.size > 8 * 1024 * 1024) {
    return "请选择 8 MB 以内的 PNG、JPEG、WebP 或 GIF 图片。";
  }
  let dataURL;
  try {
    dataURL = await compressImageFile(file);
  } catch (_) {
    return "无法读取图片，请重试。";
  }
  try {
    localStorage.setItem(CUSTOM_BACKGROUND_KEY, dataURL);
  } catch (_) {
    return "图片体积超出本机存储上限（压缩后仍超 5 MB），请换一张更小的图片。";
  }
  return null;
}

// clearCustomImage 删除已保存的图片。
export function clearCustomImage() {
  try {
    localStorage.removeItem(CUSTOM_BACKGROUND_KEY);
  } catch (_) {}
}

// applyCustomBackground 按 background 设置值同步 body.has-custom-bg 与
// --custom-image 变量。value 是设置里的 background 字段。
export function applyCustomBackground(value, overlayVar) {
  const image = customBackgroundData();
  const active = value === BACKGROUND_CUSTOM && !!image;
  document.body.classList.toggle("has-custom-bg", active);
  if (active) {
    document.documentElement.style.setProperty("--custom-image", 'url("' + image + '")');
    if (overlayVar) {
      document.documentElement.style.setProperty("--custom-overlay", overlayVar);
    }
  } else {
    document.documentElement.style.removeProperty("--custom-image");
    if (overlayVar) {
      document.documentElement.style.removeProperty("--custom-overlay");
    }
  }
}
