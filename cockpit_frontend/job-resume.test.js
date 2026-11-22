/* =============================================================
   cockpit · job-resume.test.js — node:test 零依賴
   鎖住「點選執行中的工作」的行為：/api/jobs 列表的 log 只是尾端快照。
   點選時不得預先清空快照（SSE 連不上會讓列表變空白）；streamJob 以 replay 模式
   接上 SSE，收到第一行完整 log 時才以它取代快照，連線出錯且尚未收到 log 時保留快照。
   點選分支以原始碼層級斷言（與 load-performance.test.js 同一模式）；
   streamJob 則抽出原始碼，以假 EventSource 實際執行。
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

test("點選執行中且無串流的工作：不預先清空 cached.log，render 後以 replay 模式 streamJob", () => {
  const render = branch.indexOf("renderCurrentJob(cached)");
  const stream = branch.indexOf("streamJob(cached, { replay: true })");
  assert.doesNotMatch(branch, /cached\.log = \[\]/, "不可預先清空快照，SSE 連不上時列表會變成空白");
  assert.notEqual(stream, -1, "缺少 streamJob(cached, { replay: true })");
  assert.ok(render < stream, "應在 render 後接上 SSE");
});

test("已結束的工作仍保留重抓 /api/jobs/{id} 的分支", () => {
  assert.match(branch, /if \(cached\.status !== "running"\) \{/);
  assert.ok(branch.includes("api(`/api/jobs/${encodeURIComponent(jobId)}`)"), "缺少重抓單一工作的 api 呼叫");
});

// ---- streamJob 行為：抽出原始碼，以假 EventSource 實際執行 ----
const streamStart = js.indexOf("function streamJob(");
const streamEnd = js.indexOf("async function abortJob(", streamStart);
const streamSrc = streamStart >= 0 && streamEnd > streamStart ? js.slice(streamStart, streamEnd) : "";

class FakeEventSource {
  constructor(url) { this.url = url; this.listeners = {}; this.closed = false; FakeEventSource.last = this; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  emit(type, data) { for (const fn of this.listeners[type] || []) fn({ data }); }
  close() { this.closed = true; }
}

// calls 依序記錄 render:<渲染當下的 log 行數> 與 append:<行>
function loadStreamJob(activeJobId, calls) {
  const state = { activeJobId, streams: {} };
  const factory = new Function(
    "state", "EventSource", "appendLogLine", "renderCurrentJob", "loadInstalls", "loadJobs",
    "JOBS", "finishJob", "render", "renderRecentJobs",
    `${streamSrc}\nreturn streamJob;`
  );
  return factory(
    state, FakeEventSource,
    (job, line) => calls.push(`append:${line}`),
    (job) => calls.push(`render:${job.log.length}`),
    async () => {}, async () => {}, [], () => {}, () => {}, () => {}
  );
}

test("replay：收到第一行完整 log 才清空快照並重繪，之後照常 append", () => {
  const job = { id: "7", log: ["old-1", "old-2"] };
  const calls = [];
  loadStreamJob("7", calls)(job, { replay: true });
  const es = FakeEventSource.last;
  es.emit("log", "full-1");
  assert.deepEqual(job.log, ["full-1"]);
  es.emit("log", "full-2");
  assert.deepEqual(job.log, ["full-1", "full-2"]);
  assert.deepEqual(calls, ["render:0", "append:full-1", "append:full-2"]);
});

test("replay：只重繪作用中的工作，避免把別的工作畫進目前面板", () => {
  const job = { id: "7", log: ["old-1"] };
  const calls = [];
  loadStreamJob("8", calls)(job, { replay: true });
  FakeEventSource.last.emit("log", "full-1");
  assert.deepEqual(job.log, ["full-1"]);
  assert.deepEqual(calls.filter((c) => c.startsWith("render")), []);
});

test("replay：連線出錯且尚未收到 log 時保留快照並關閉串流", () => {
  const job = { id: "7", log: ["old-1", "old-2"] };
  loadStreamJob("7", []) (job, { replay: true });
  FakeEventSource.last.onerror();
  assert.deepEqual(job.log, ["old-1", "old-2"]);
  assert.equal(FakeEventSource.last.closed, true);
});
