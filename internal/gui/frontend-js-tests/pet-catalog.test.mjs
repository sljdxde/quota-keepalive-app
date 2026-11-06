import assert from "node:assert/strict";
import { existsSync, statSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { POKEMON_CATALOG } from "../frontend/assets/pokemon-art.js";

// 球面宠物模式只读 2D 立绘：任何可选伙伴的 sprite 缺失，球面都会变成
// 只有数字的空白球。这里直接检查真实文件，防止目录与目录项漂移。
const FRONTEND = fileURLToPath(new URL("../frontend/", import.meta.url));

test("球面宠物立绘：每个可选伙伴的 2D 图都存在且非空", function () {
  assert.ok(POKEMON_CATALOG.length > 0, "宠物目录不能为空");
  for (const pet of POKEMON_CATALOG) {
    assert.ok(Array.isArray(pet.stages) && pet.stages.length > 0, `${pet.id} 缺阶段`);
    const sprite = pet.stages[0].sprite;
    assert.ok(sprite, `${pet.id} 缺 sprite`);
    const file = path.join(FRONTEND, "assets", "pokemon", sprite);
    assert.ok(existsSync(file), `${pet.id} 的立绘不存在: ${file}`);
    assert.ok(statSync(file).size > 0, `${pet.id} 的立绘是空文件: ${file}`);
  }
});