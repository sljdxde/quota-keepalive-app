// 3D 资源（three.js + GLTF + Draco，约 2.5MB）按需加载。
//
// 三个页面（main.html / panel.js / petmini.html）都只在真正要渲染 3D 宠物时
// 才调用 loadPokemon3D()，首屏不再为隐藏页面付出下载与解析成本。同一个
// WebView 内并发调用共享同一个 in-flight Promise；加载成功后缓存
// createPokemon3D 工厂，后续调用直接复用；失败时清空 in-flight，允许重试。

const DEFAULT_IMPORT = function () {
  return import("./pokemon-3d.js");
};

let cachedFactory = null;
let inFlight = null;

// 返回 Promise<createPokemon3D>。importer 仅测试注入用，生产走默认动态 import。
export function loadPokemon3D(importer) {
  if (cachedFactory) return Promise.resolve(cachedFactory);
  if (inFlight) return inFlight;
  const load = typeof importer === "function" ? importer : DEFAULT_IMPORT;
  const pending = Promise.resolve()
    .then(function () { return load(); })
    .then(function (module) {
      const factory = module && module.createPokemon3D;
      if (typeof factory !== "function") {
        throw new Error("pokemon-3d.js 未导出 createPokemon3D");
      }
      cachedFactory = factory;
      inFlight = null;
      return factory;
    })
    .catch(function (error) {
      // 失败不缓存：下次调用重新 import（动态 import 的失败在模块图里本身
      // 会被记住，不会重复下载，但允许用户重试拿到成功结果）。
      inFlight = null;
      throw error;
    });
  inFlight = pending;
  return pending;
}

export function resetPokemon3DLoaderForTest() {
  cachedFactory = null;
  inFlight = null;
}
