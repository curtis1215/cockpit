/* =============================================================
   cockpit · load-performance.test.js — node:test 零依賴
   鎖住首屏效能：正式頁面不得依賴 render-blocking Tailwind CDN，
   首頁資料載入也不得被 /api/version 查詢阻塞。
   執行：node --test cockpit_frontend/*.test.js
   ============================================================= */
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = __dirname;

function read(name) {
  return fs.readFileSync(path.join(root, name), "utf8");
}

test("首頁使用本地 Tailwind CSS，不載入 render-blocking cdn.tailwindcss.com", () => {
  const html = read("index.html");
  assert.equal(html.includes("https://cdn.tailwindcss.com"), false, "首頁不得載入 Tailwind CDN runtime script");
  assert.match(html, /<link\s+[^>]*href=["']tailwind\.css["'][^>]*>/, "首頁應載入本地 tailwind.css");
});

test("首頁啟動時先載入主要資料，/api/version 只能 best-effort 背景更新", () => {
  const js = read("app.js");
  const bootStart = js.indexOf("initTheme();");
  const versionFetch = js.indexOf('api("/api/version")', bootStart);
  const loadInstalls = js.indexOf("loadInstalls()", bootStart);
  assert.notEqual(bootStart, -1, "找不到首頁啟動區塊");
  assert.notEqual(versionFetch, -1, "找不到 /api/version 查詢");
  assert.notEqual(loadInstalls, -1, "找不到 loadInstalls()");
  assert.ok(loadInstalls < versionFetch, "主要資料載入應早於 /api/version");
  assert.equal(/await\s+api\(["']\/api\/version["']\)/.test(js), false, "/api/version 不應被 await 阻塞首屏資料載入");
});
