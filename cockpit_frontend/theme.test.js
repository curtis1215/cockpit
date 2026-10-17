/* =============================================================
   cockpit · theme.test.js — node:test 零依賴
   鎖住四頁櫻色主題色票。色票數值以
   docs/superpowers/specs/2026-10-17-sakura-theme-design.md 為準。
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

// spec「共用變數」表全部名稱（15 個底色／文字／強調／終端機 + ok/warn/err/mut 各 3 = 27）。
// plan 散文寫 25，與 spec 表及 index.html 區塊實際宣告數不符；以 spec 表為準。
const PALETTE = {
  light: {
    "--bg": "#fbf6f4",
    "--bg-grid": "#f5ece9",
    "--surface": "#ffffff",
    "--surface-2": "#f7eeeb",
    "--surface-3": "#f0e3df",
    "--border": "#efe1dd",
    "--border-2": "#e3d0cb",
    "--text": "#463c3c",
    "--text-2": "#5f514f",
    "--text-3": "#7a6966",
    "--accent": "#b05462",
    "--accent-ink": "#ffffff",
    "--accent-weak": "#fbe9eb",
    "--term-bg": "#1f191c",
    "--term-text": "#e2d5d8",
    "--ok": "#3f7552",
    "--ok-bg": "#e7f2eb",
    "--ok-bd": "#c9e0d1",
    "--warn": "#8a5d1c",
    "--warn-bg": "#f9efdf",
    "--warn-bd": "#eedcb8",
    "--err": "#a8444a",
    "--err-bg": "#f9e4e4",
    "--err-bd": "#efc6c6",
    "--mut": "#6e5f5d",
    "--mut-bg": "#f5eeec",
    "--mut-bd": "#eadfdc",
  },
  dark: {
    "--bg": "#1a1517",
    "--bg-grid": "#1f191c",
    "--surface": "#231c1f",
    "--surface-2": "#2d2428",
    "--surface-3": "#362b30",
    "--border": "#33292d",
    "--border-2": "#45373c",
    "--text": "#f1e6e8",
    "--text-2": "#c2b1b5",
    "--text-3": "#a08f93",
    "--accent": "#e8a3ad",
    "--accent-ink": "#1a1517",
    "--accent-weak": "#3a252a",
    "--term-bg": "#120e10",
    "--term-text": "#e2d5d8",
    "--ok": "#9fceb0",
    "--ok-bg": "#1c2a22",
    "--ok-bd": "#2f4a3a",
    "--warn": "#e9c38a",
    "--warn-bg": "#2c2418",
    "--warn-bd": "#54432a",
    "--err": "#f08a8a",
    "--err-bg": "#311a1c",
    "--err-bd": "#5c2d31",
    "--mut": "#b3a1a5",
    "--mut-bg": "#2d2428",
    "--mut-bd": "#45373c",
  },
};

const PAGE_EXTRA = {
  "machine.html": {
    light: { "--chart-grid": "#f3e8e5" },
    dark: { "--chart-grid": "#2a2125" },
  },
  "manage.html": {
    light: { "--info": "#6c5aa6", "--info-bg": "#f0ecf8", "--info-bd": "#dcd3ef" },
    dark: { "--info": "#b9a7e0", "--info-bg": "#251f33", "--info-bd": "#3e3456" },
  },
  "topology.html": {
    light: { "--edge": "#dcc9c5", "--edge-dim": "#f0e5e2" },
    dark: { "--edge": "#4a3b40", "--edge-dim": "#261e21" },
  },
};

function block(html, selector) {
  const marker = `${selector} {`;
  const start = html.indexOf(marker);
  if (start < 0) throw new Error(`找不到 ${selector} 區塊`);
  const open = html.indexOf("{", start);
  let depth = 0;
  for (let i = open; i < html.length; i++) {
    if (html[i] === "{") depth++;
    else if (html[i] === "}") {
      depth--;
      if (depth === 0) return html.slice(open + 1, i);
    }
  }
  throw new Error(`${selector} 區塊沒有對應的 }`);
}

function vars(css) {
  const out = new Map();
  const re = /(--[a-zA-Z0-9-]+)\s*:\s*(#[0-9a-fA-F]+)/g;
  for (const m of css.matchAll(re)) out.set(m[1], m[2].toLowerCase());
  return out;
}

function assertPalette(file) {
  const html = read(file);
  for (const [selector, mode] of [[":root", "light"], ["html.dark", "dark"]]) {
    const css = block(html, selector);
    const found = vars(css);
    const extra = (PAGE_EXTRA[file] && PAGE_EXTRA[file][mode]) || {};
    const table = { ...PALETTE[mode], ...extra };
    for (const m of css.matchAll(/(--[a-zA-Z0-9-]+)\s*:/g)) {
      assert.ok(
        Object.prototype.hasOwnProperty.call(table, m[1]),
        `${file} ${mode} 未定義變數 ${m[1]}`,
      );
    }
    for (const [name, value] of found) {
      if (Object.prototype.hasOwnProperty.call(table, name)) {
        assert.equal(value, table[name], `${file} ${mode} ${name}`);
      }
    }
  }
}

test("index.html 兩種模式色票為櫻色", () => assertPalette("index.html"));

test("色票解析器在 index.html 深色區塊抓到 --accent", () => {
  const v = vars(block(read("index.html"), "html.dark"));
  assert.ok(v.size >= 20);
  assert.ok(v.has("--accent"));
});
