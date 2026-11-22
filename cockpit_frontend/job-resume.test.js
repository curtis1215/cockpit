/* =============================================================
   cockpit · job-resume.test.js — node:test 零依賴
   鎖住「點選執行中的工作」的行為：/api/jobs 列表的 log 只是尾端快照，
   重新整理後點選執行中的工作必須清空 log，並重新 streamJob，
   讓 SSE 從第一行重送完整 log。DOM 行為無法在 node 內驗證，
   這裡只做原始碼層級的斷言（與 load-performance.test.js 同一模式）。
   執行：node --test cockpit_frontend/*.test.js
   ============================================================= */
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const js = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
const start = js.indexOf('const jb = e.target.closest("[data-job]");');
const end = js.indexOf("// 鍵盤：Esc", start);
const branch = start >= 0 && end > start ? js.slice(start, end) : "";

test("app.js 含 [data-job] 點選分支", () => {
  assert.ok(branch.length > 0, "找不到 [data-job] 點選分支");
});

test("點選執行中且無串流的工作：判斷條件存在", () => {
  assert.match(branch, /cached\.status === "running" && !state\.streams\?\.\[cached\.id\]/);
});

test("點選執行中且無串流的工作：先清空 log，再 renderCurrentJob，之後才 streamJob", () => {
  const clear = branch.indexOf("cached.log = []");
  const render = branch.indexOf("renderCurrentJob(cached)");
  const stream = branch.indexOf("streamJob(cached)");
  assert.notEqual(clear, -1, "缺少 cached.log = []");
  assert.notEqual(stream, -1, "缺少 streamJob(cached)");
  assert.ok(clear < render, "必須先清空 log 再 render，否則會先閃出截斷快照");
  assert.ok(render < stream, "應在 render 後接上 SSE");
});

test("已結束的工作仍保留重抓 /api/jobs/{id} 的分支", () => {
  assert.match(branch, /if \(cached\.status !== "running"\) \{/);
  assert.ok(branch.includes("api(`/api/jobs/${encodeURIComponent(jobId)}`)"), "缺少重抓單一工作的 api 呼叫");
});
