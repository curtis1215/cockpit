/* =============================================================
   cockpit · topo-state.test.js — node:test 零依賴
   鎖死 stale-reference bug：loadAll 必須原地更新同一個共享狀態
   物件，topo.js / trends.js 頂層解構的參考才能看到新一輪資料。
   執行：node --test cockpit_frontend/
   ============================================================= */
const { test } = require("node:test");
const assert = require("node:assert/strict");
const {
  applyObjectInPlace,
  applyArrayInPlace,
  REFRESH_INTERVAL_MS,
} = require("./topo-state.js");

test("applyObjectInPlace 回傳同一 target 參考並覆寫既有 key", () => {
  const target = { a: 1, b: 2 };
  const ref = target;
  const ret = applyObjectInPlace(target, { a: 9, c: 3 });
  assert.equal(ret, target, "必須回傳同一個 target 參考（不可 new）");
  assert.equal(ref, target);
  assert.equal(target.a, 9, "既有 key 應被覆寫");
  assert.equal(target.c, 3, "新 key 應加入");
});

test("applyObjectInPlace 移除 source 不存在的 key", () => {
  const target = { a: 1, b: 2 };
  applyObjectInPlace(target, { a: 1 });
  assert.equal("b" in target, false, "source 沒有的 key 應被刪除");
  assert.deepEqual(Object.keys(target), ["a"]);
});

test("applyObjectInPlace：舊解構參考在下一輪看得到新值（bug 不復發）", () => {
  const stable = {};
  const holder = stable; // 模擬 topo.js:9 頂層解構綁定首次參考
  applyObjectInPlace(stable, { cpu: 10 });
  assert.equal(holder.cpu, 10);
  applyObjectInPlace(stable, { cpu: 55 }); // 下一輪 loadAll
  assert.equal(holder.cpu, 55, "舊參考必須反映新一輪資料");
});

test("applyArrayInPlace 回傳同一 target 參考並原地替換內容", () => {
  const target = [1, 2, 3];
  const ref = target;
  const ret = applyArrayInPlace(target, ["a", "b"]);
  assert.equal(ret, target, "必須回傳同一個 target 參考（不可 new）");
  assert.equal(ref, target);
  assert.equal(target.length, 2);
  assert.deepEqual(target, ["a", "b"]);
});

test("applyArrayInPlace：舊解構參考在下一輪看得到新內容", () => {
  const stable = [];
  const holder = stable; // 模擬 topo.js:9 解構出的 MACHINE_ORDER/SERVICES
  applyArrayInPlace(stable, [{ id: "x" }]);
  assert.equal(holder.length, 1);
  applyArrayInPlace(stable, [{ id: "y" }, { id: "z" }]);
  assert.equal(holder.length, 2);
  assert.equal(holder[0].id, "y");
});

test("REFRESH_INTERVAL_MS 為 15000（P1：30s→15s）", () => {
  assert.equal(REFRESH_INTERVAL_MS, 15000);
});
