/* =============================================================
   cockpit · groups.js — 機器群組全域切換器（共用元件）
   -------------------------------------------------------------
   清單 / 拓樸 / 機器 三頁共用。頁面在資料載入後呼叫
   CockpitGroups.init(groupList, hasUngrouped)；切換時通知 onChange。
   localStorage["cockpit-machine-group"]：
     ""         = 全部
     "__none__" = 未分組
     其他字串    = 群組名
   注意：localStorage["cockpit-group"] 已被軟體頁顯示模式占用，勿混用。
   ============================================================= */
(() => {
  const KEY = "cockpit-machine-group";
  const NONE = "__none__";
  let current = localStorage.getItem(KEY) || "";
  let groups = [];          // 既有群組名（已排序）
  let hasUngrouped = false;
  const listeners = [];

  /* 樣式注入（避免三頁重複貼 CSS） */
  const style = document.createElement("style");
  style.textContent = `
    #group-switcher { display:inline-flex; gap:2px; padding:3px; border:1px solid var(--border); border-radius:9px; background:var(--surface-2); }
    #group-switcher .grp-btn { font-size:12px; font-weight:550; padding:3px 10px; border-radius:6px; border:none; background:transparent; color:var(--text-3); cursor:pointer; transition:.14s; font-family:inherit; }
    #group-switcher .grp-btn:hover { color:var(--text); }
    #group-switcher .grp-btn.active { background:var(--surface); color:var(--text); box-shadow:inset 0 0 0 1px var(--border); }
  `;
  document.head.appendChild(style);

  const escHtml = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");

  /** effectiveGroup（可為空字串）是否落在目前選擇的群組視圖內 */
  function matches(effectiveGroup) {
    if (current === "") return true;
    if (current === NONE) return !effectiveGroup;
    return effectiveGroup === current;
  }

  function set(g) {
    current = g;
    localStorage.setItem(KEY, g);
    renderBar();
    listeners.forEach((cb) => { try { cb(g); } catch (e) { console.error("[groups] onChange:", e); } });
  }

  function renderBar() {
    const el = document.getElementById("group-switcher");
    if (!el) return;
    if (groups.length === 0) { el.innerHTML = ""; el.style.display = "none"; return; }
    el.style.display = "";
    const opts = [["", "全部"], ...groups.map((g) => [g, g])];
    if (hasUngrouped) opts.push([NONE, "未分組"]);
    el.innerHTML = opts.map(([v, label]) =>
      `<button class="grp-btn ${v === current ? "active" : ""}" data-grp="${escHtml(v)}">${escHtml(label)}</button>`
    ).join("");
  }

  /**
   * 資料載入後呼叫。groupList = 各機器 effective_group 的值（可含重複/空字串，
   * 此處會去重排序）；hasUngrouped = 是否存在未分組機器。
   */
  function init(groupList, hasUngroupedIn) {
    groups = [...new Set((groupList || []).filter(Boolean))].sort((a, b) => a.localeCompare(b));
    hasUngrouped = !!hasUngroupedIn;
    // 已選群組消失 → 退回全部
    if (current && current !== NONE && !groups.includes(current)) current = "";
    if (current === NONE && !hasUngrouped) current = "";
    renderBar();
  }

  document.addEventListener("click", (e) => {
    const b = e.target.closest("#group-switcher [data-grp]");
    if (!b) return;
    const v = b.getAttribute("data-grp");
    if (v !== current) set(v);
  });

  window.CockpitGroups = {
    init,
    matches,
    get: () => current,
    onChange: (cb) => listeners.push(cb),
  };
})();
