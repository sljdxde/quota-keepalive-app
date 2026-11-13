import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  loadPokemon3D,
  resetPokemon3DLoaderForTest,
} from "../frontend/assets/pokemon-3d-loader.js";

// three.js + GLTFLoader + DRACOLoader 合起来约 2.5MB：三个页面都只在真正要
// 渲染 3D 时才 import。这里用注入的假 importer 验证懒加载语义，不碰真实
// three.js / WebGL，node 下即可跑。
const FRONTEND = fileURLToPath(new URL("../frontend/", import.meta.url));

function fakeFactory() {
  return { tag: "fake-viewer" };
}

function countingImporter() {
  const state = { calls: 0 };
  state.import = function () {
    state.calls += 1;
    return Promise.resolve({ createPokemon3D: fakeFactory });
  };
  return state;
}

test("首次调用才 import，且只在真正调用时发生", async function () {
  resetPokemon3DLoaderForTest();
  const spy = countingImporter();
  assert.equal(spy.calls, 0, "import 模块本身不应触发下载");
  const factory = await loadPokemon3D(spy.import);
  assert.equal(factory, fakeFactory);
  assert.equal(spy.calls, 1);
});

test("并发调用共享同一次 import", async function () {
  resetPokemon3DLoaderForTest();
  const spy = countingImporter();
  const [a, b, c] = await Promise.all([
    loadPokemon3D(spy.import),
    loadPokemon3D(spy.import),
    loadPokemon3D(spy.import),
  ]);
  assert.equal(spy.calls, 1, "并发调用必须复用 in-flight Promise");
  assert.equal(a, b);
  assert.equal(b, c);
});

test("成功后缓存工厂，后续调用不再 import", async function () {
  resetPokemon3DLoaderForTest();
  const spy = countingImporter();
  await loadPokemon3D(spy.import);
  const again = await loadPokemon3D(spy.import);
  assert.equal(spy.calls, 1);
  assert.equal(again, fakeFactory);
  // 缓存后即使传一个新 importer 也不应再触发加载。
  const other = countingImporter();
  await loadPokemon3D(other.import);
  assert.equal(other.calls, 0);
});

test("失败会清空 in-flight，下一次可以重试成功", async function () {
  resetPokemon3DLoaderForTest();
  let calls = 0;
  const importer = function () {
    calls += 1;
    if (calls === 1) return Promise.reject(new Error("network down"));
    return Promise.resolve({ createPokemon3D: fakeFactory });
  };
  await assert.rejects(function () { return loadPokemon3D(importer); }, /network down/);
  const factory = await loadPokemon3D(importer);
  assert.equal(factory, fakeFactory);
  assert.equal(calls, 2, "失败后必须允许重新 import");
});

test("失败时并发等待者都拿到同一个错误，且只 import 一次", async function () {
  resetPokemon3DLoaderForTest();
  let calls = 0;
  const importer = function () {
    calls += 1;
    return Promise.reject(new Error("boom"));
  };
  const results = await Promise.allSettled([
    loadPokemon3D(importer),
    loadPokemon3D(importer),
  ]);
  assert.equal(calls, 1);
  assert.deepEqual(results.map(function (r) { return r.status; }), ["rejected", "rejected"]);
});

test("模块缺少 createPokemon3D 时按失败处理并可重试", async function () {
  resetPokemon3DLoaderForTest();
  let calls = 0;
  const importer = function () {
    calls += 1;
    return Promise.resolve(calls === 1 ? {} : { createPokemon3D: fakeFactory });
  };
  await assert.rejects(function () { return loadPokemon3D(importer); }, /createPokemon3D/);
  assert.equal(await loadPokemon3D(importer), fakeFactory);
});

test("resetPokemon3DLoaderForTest 清空缓存，下次重新 import", async function () {
  resetPokemon3DLoaderForTest();
  const spy = countingImporter();
  await loadPokemon3D(spy.import);
  resetPokemon3DLoaderForTest();
  await loadPokemon3D(spy.import);
  assert.equal(spy.calls, 2);
});

// 静态 import 会进模块图：页面一加载就拉 three.js/GLTF/Draco。三个入口都
// 必须改走 loader 的动态 import，否则懒加载等于没做。
const PAGES = ["main.html", "petmini.html", "assets/panel.js"];

test("三个页面不再静态导入 pokemon-3d.js", function () {
  for (const page of PAGES) {
    const source = readFileSync(FRONTEND + page, "utf8");
    for (const forbidden of [
      'from "./pokemon-3d.js"',
      'from "./assets/pokemon-3d.js"',
      "from './pokemon-3d.js'",
      "from './assets/pokemon-3d.js'",
    ]) {
      assert.equal(
        source.includes(forbidden),
        false,
        `${page} 仍在静态导入 3D 模块：${forbidden}`,
      );
    }
  }
});

test("三个页面改为通过 loader 触发加载", function () {
  for (const page of PAGES) {
    const source = readFileSync(FRONTEND + page, "utf8");
    assert.ok(
      source.includes('from "./assets/pokemon-3d-loader.js"') ||
        source.includes('from "./pokemon-3d-loader.js"'),
      `${page} 未接入 pokemon-3d-loader.js`,
    );
    assert.ok(source.includes("loadPokemon3D("), `${page} 未调用 loadPokemon3D`);
  }
});

test("主窗口与面板只在宠物页可见时才加载 3D", function () {
  const main = readFileSync(FRONTEND + "main.html", "utf8");
  assert.ok(main.includes("if (!petTabVisible()) {"), "主窗口缺少宠物页可见性门控");
  const panel = readFileSync(FRONTEND + "assets/panel.js", "utf8");
  assert.ok(
    panel.includes('if (activeTab === "pet") ensurePetViewer(pet);'),
    "面板 renderPetAvatar 缺少宠物页门控",
  );
  // ensurePetViewer 只应被带门控的调用点触发，不允许无条件调用。
  assert.equal(
    /^\s*ensurePetViewer\(pet\);/m.test(panel),
    false,
    "面板存在无门控的 ensurePetViewer 调用",
  );
});

test("异步创建都有竞态检查（序号 + id）", function () {
  // 只比对 id 不够：A→B→A 快速切换时两个同 id 的 Promise 会同时满足 id 检查，
  // 先回来的那个会在已废弃的 canvas 上再建一个 WebGL 上下文。
  const pages = [
    ["main.html", "mainPetViewerGen", "mainPetViewerPending"],
    ["assets/panel.js", "petViewerGen", "petViewerPending"],
    ["petmini.html", "viewerGen", "viewerPending"],
  ];
  for (const [page, genVar, pendingVar] of pages) {
    const source = readFileSync(FRONTEND + page, "utf8");
    assert.ok(source.includes(`if (gen !== ${genVar}) return;`), `${page} 缺 then 内序号竞态检查`);
    assert.ok(source.includes(`${genVar}++;`), `${page} 缺序号自增`);
    assert.ok(source.includes(`if (${pendingVar} === `), `${page} 缺重复排队检查`);
  }
});

test("供应商登录 3D 艺术图改为 showModal 前懒创建", function () {
  const main = readFileSync(FRONTEND + "main.html", "utf8");
  assert.ok(
    main.includes("function ensureProviderLoginPokemonViewer()"),
    "缺少供应商登录艺术图懒创建函数",
  );
  const create = main.indexOf("ensureProviderLoginPokemonViewer();\n    providerLoginDialog.showModal();");
  assert.ok(create >= 0, "必须在 showModal 前才创建 3D 艺术图");
});
