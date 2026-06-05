/* =============================================================
   cockpit · 版本追蹤器 — 前端互動 (vanilla JS)
   -------------------------------------------------------------
   接後端時的對應（htmx / SSE 友善）：
     · 篩選       → 目前純前端過濾；可改 hx-get="/api/installs?machine=…"
     · 立即檢查    → POST /api/check  然後重抓 installs（或 SSE 推送）
     · changelog  → GET  /api/changelog/:software/:version
     · 更新       → POST /api/jobs {install_id} → 回 job_id → 開 SSE 串 log
   標 [API] 處即為需要替換成真實呼叫的地方。
   完整端點 / SSE / htmx partial 契約見：cockpit/api-contract.md
   ============================================================= */
(() => {
  const $ = (s, r = document) => r.querySelector(s);

  /* ---- module-level mutable state (loaded from real APIs) ---- */
  let INSTALLS = [], MACHINES = [], JOBS = [];

  async function api(path, opts) {
    const r = await fetch(path, opts);
    if (!r.ok) { const e = new Error(`${path} → ${r.status}`); e.status = r.status; throw e; }
    return r.status === 204 ? null : r.json();
  }
  async function loadInstalls() {
    const rows = await api("/api/installs");
    INSTALLS = rows.map((r) => ({ ...r, checked_at: r.last_checked }));
    MACHINES = [...new Set(INSTALLS.map((i) => i.machine))].sort();
  }
  async function loadJobs() {
    const rows = await api("/api/jobs");
    JOBS = rows.map((j) => ({
      ...j,
      id: String(j.id),
      installId: j.software + "::" + j.machine,
      log: (j.log || "").split("\n").filter(Boolean),
    }));
  }

  function showLoadError() {
    const tbody = $("#table-body");
    if (tbody) {
      tbody.innerHTML = `<tr><td colspan="7" class="px-4 py-10 text-center" style="color: var(--err);">
        <div class="flex flex-col items-center gap-2">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v5M12 16h.01"/></svg>
          <span class="text-[13px]">無法連線後端，請確認服務是否正常運作。</span>
        </div>
      </td></tr>`;
    }
    const tableWrap = $("#table-wrap");
    if (tableWrap) {
      tableWrap.classList.remove("hidden");
      $("#empty-state").classList.add("hidden");
      $("#empty-state").classList.remove("flex");
    }
  }

  /* ---- 可變狀態（render 都讀這裡）---- */
  const state = {
    installs: [],   // 從 loadInstalls() 填充
    jobs: [],       // 從 loadJobs() 填充
    filters: { machine: "", onlyUpdates: false, q: "" },
    group: "flat",            // flat | machine | status
    activeJobId: null,
    streams: {},               // 每個 job 各自的 EventSource → 支援多 job 並行串流
    layout: "side",            // side | bottom
  };

  const KIND_LABEL = {
    npm: "npm", github: "github", pypi: "pypi", brew: "brew",
    "claude-plugin": "plugin", custom: "custom",
  };
  const STATUS = {
    up_to_date: { cls: "b-ok",   label: "最新" },
    behind:     { cls: "b-warn", label: "落後" },
    unknown:    { cls: "b-mut",  label: "未知" },
    error:      { cls: "b-err",  label: "錯誤" },
    updating:   { cls: "b-warn", label: "更新中" },
  };

  /* ============================================================
     主題
     ============================================================ */
  function initTheme() {
    const saved = localStorage.getItem("cockpit-theme");
    const dark = saved ? saved === "dark" : true;
    document.documentElement.classList.toggle("dark", dark);
    syncThemeIcon();
  }
  function syncThemeIcon() {
    const dark = document.documentElement.classList.contains("dark");
    $("#icon-moon").classList.toggle("hidden", !dark);
    $("#icon-sun").classList.toggle("hidden", dark);
  }
  $("#theme-btn").addEventListener("click", () => {
    const dark = document.documentElement.classList.toggle("dark");
    localStorage.setItem("cockpit-theme", dark ? "dark" : "light");
    syncThemeIcon();
  });

  /* ============================================================
     篩選
     ============================================================ */
  function populateMachineFilter() {
    const sel = $("#filter-machine");
    // 清除舊選項（除了第一個「全部機器」佔位符）
    while (sel.options.length > 1) sel.remove(1);
    MACHINES.forEach((m) => {
      const o = document.createElement("option");
      o.value = m; o.textContent = m;
      sel.appendChild(o);
    });
  }

  function initFilters() {
    const sel = $("#filter-machine");
    sel.addEventListener("change", (e) => { state.filters.machine = e.target.value; render(); });

    $("#filter-search").addEventListener("input", (e) => {
      state.filters.q = e.target.value.trim().toLowerCase(); render();
    });

    $("#filter-updates").addEventListener("click", () => {
      state.filters.onlyUpdates = !state.filters.onlyUpdates;
      const on = state.filters.onlyUpdates;
      $("#filter-updates").setAttribute("aria-pressed", on);
      $("#toggle-ui").style.background = on ? "var(--accent)" : "var(--border-2)";
      $("#toggle-knob").style.transform = on ? "translateX(12px)" : "translateX(0)";
      render();
    });

    // 分組切換
    document.querySelectorAll("[data-group-btn]").forEach((b) =>
      b.addEventListener("click", () => setGroup(b.getAttribute("data-group-btn"))));
  }

  function setGroup(mode) {
    state.group = mode;
    document.querySelectorAll("[data-group-btn]").forEach((b) =>
      b.classList.toggle("active", b.getAttribute("data-group-btn") === mode));
    localStorage.setItem("cockpit-group", mode);
    render();
  }

  function filtered() {
    const { machine, onlyUpdates, q } = state.filters;
    return state.installs.filter((it) => {
      if (machine && it.machine !== machine) return false;
      if (onlyUpdates && it.status !== "behind") return false;
      if (q && !it.software.toLowerCase().includes(q)) return false;
      return true;
    });
  }

  /* ============================================================
     render：表格 + 卡片 + 摘要 + 空狀態
     ============================================================ */
  // 狀態分組時，「更新中」歸到「有更新」那組
  const statusKey = (s) => (s === "updating" ? "behind" : s);

  function buildGroups(rows) {
    if (state.group === "machine") {
      return MACHINES
        .map((m) => ({ label: m, color: "var(--text-3)", mono: true, rows: rows.filter((it) => it.machine === m) }))
        .filter((g) => g.rows.length);
    }
    if (state.group === "status") {
      const meta = {
        behind:     ["有更新", "var(--warn)"],
        error:      ["錯誤",   "var(--err)"],
        unknown:    ["未知",   "var(--mut)"],
        up_to_date: ["最新",   "var(--ok)"],
      };
      return ["behind", "error", "unknown", "up_to_date"]
        .map((k) => ({ label: meta[k][0], color: meta[k][1], rows: rows.filter((it) => statusKey(it.status) === k) }))
        .filter((g) => g.rows.length);
    }
    return [{ label: null, rows }];   // flat
  }

  function groupHeadRow(g) {
    return `<tr class="group-head"><td colspan="7" class="px-4 py-2">
        <div class="flex items-center gap-2">
          <span class="w-2 h-2 rounded-full" style="background: ${g.color};"></span>
          <span class="text-[12px] font-semibold ${g.mono ? "mono" : ""}" style="color: var(--text-2);">${g.label}</span>
          <span class="text-[11px]" style="color: var(--text-3);">${g.rows.length}</span>
        </div></td></tr>`;
  }
  function groupHeadCard(g) {
    return `<div class="flex items-center gap-2 px-1 pt-3 pb-0.5 first:pt-1">
        <span class="w-2 h-2 rounded-full" style="background: ${g.color};"></span>
        <span class="text-[12px] font-semibold ${g.mono ? "mono" : ""}" style="color: var(--text-2);">${g.label}</span>
        <span class="text-[11px]" style="color: var(--text-3);">${g.rows.length}</span>
      </div>`;
  }

  function statusBadge(it) {
    const s = STATUS[it.status];
    if (it.status === "behind")
      return `<span class="badge b-warn"><span class="dot"></span>落後 ${it.behind_count} 版</span>`;
    if (it.status === "updating")
      return `<span class="badge b-warn"><svg class="spin" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round"><path d="M21 12a9 9 0 1 1-6.22-8.56"/></svg>更新中</span>`;
    if (it.status === "error")
      return `<span class="badge b-err" title="${(it.error||"").replace(/"/g,"&quot;")}"><span class="dot"></span>錯誤</span>`;
    return `<span class="badge ${s.cls}"><span class="dot"></span>${s.label}</span>`;
  }

  function changelogLink(it) {
    const key = `${it.software}@${it.latest_version}`;
    if (it.latest_version)
      return `<button class="text-[12px] font-medium hover:underline" style="color: var(--accent);" data-changelog="${key}">中文</button>`;
    return `<span class="text-[12px]" style="color: var(--text-3);">—</span>`;
  }

  function actionBtn(it) {
    if (it.status === "behind")
      return `<button class="btn btn-primary btn-xs" data-update="${it.id}">更新</button>`;
    if (it.status === "updating")
      return `<button class="btn btn-xs" disabled><svg class="spin" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round"><path d="M21 12a9 9 0 1 1-6.22-8.56"/></svg>執行中</button>`;
    if (it.status === "up_to_date")
      return `<button class="btn btn-xs" disabled>更新</button>`;
    if (it.status === "error" || it.status === "unknown")
      return `<button class="btn btn-ghost btn-xs" data-recheck="${it.id}">重新檢查</button>`;
    return "";
  }

  function rowHTML(it) {
    return `
      <tr class="row-hover border-t transition-colors" style="border-color: var(--row-bd);" data-row="${it.id}">
        <td class="px-4 py-3">
          <div class="flex items-center gap-2">
            <span class="font-medium text-[13.5px]">${it.software}</span>
            <span class="kind">${KIND_LABEL[it.kind] || it.kind}</span>
            ${it.update_kind === "agent" ? `<span class="kind" style="color: var(--accent); border-color: var(--accent);" title="此更新會委派 AI agent 多步執行">agent</span>` : ""}
          </div>
        </td>
        <td class="px-4 py-3"><span class="text-[12.5px]" style="color: var(--text-2);">${it.machine}</span></td>
        <td class="px-4 py-3"><span class="mono text-[12.5px]">${it.current_version}</span></td>
        <td class="px-4 py-3"><span class="mono text-[12.5px] ${it.status==="behind"?"":"opacity-70"}" style="${it.status==="behind"?"color: var(--accent);":"color: var(--text-3);"}">${it.latest_version || "—"}</span></td>
        <td class="px-4 py-3">${statusBadge(it)}</td>
        <td class="px-4 py-3 text-center">${changelogLink(it)}</td>
        <td class="px-4 py-3 text-right">${actionBtn(it)}</td>
      </tr>`;
  }

  function cardHTML(it) {
    return `
      <div class="surface border rounded-xl p-3.5" data-row="${it.id}">
        <div class="flex items-start justify-between gap-3">
          <div class="flex items-center gap-2 flex-wrap">
            <span class="font-medium text-[14px]">${it.software}</span>
            <span class="kind">${KIND_LABEL[it.kind] || it.kind}</span>
            ${it.update_kind === "agent" ? `<span class="kind" style="color: var(--accent); border-color: var(--accent);">agent</span>` : ""}
          </div>
          ${statusBadge(it)}
        </div>
        <div class="flex items-center gap-2 mt-2 text-[12px]" style="color: var(--text-3);">
          <span>${it.machine}</span><span>·</span>
          <span class="mono" style="color: var(--text-2);">${it.current_version}</span>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14M13 6l6 6-6 6"/></svg>
          <span class="mono" style="${it.status==="behind"?"color: var(--accent);":""}">${it.latest_version || "—"}</span>
        </div>
        <div class="flex items-center justify-between mt-3">
          ${changelogLink(it)}
          ${actionBtn(it)}
        </div>
      </div>`;
  }

  function render() {
    const rows = filtered();
    const tbody = $("#table-body");
    const cards = $("#card-list");
    const empty = $("#empty-state");
    const tableWrap = $("#table-wrap");

    if (rows.length === 0) {
      tbody.innerHTML = ""; cards.innerHTML = "";
      tableWrap.classList.add("hidden");
      cards.classList.add("hidden");
      empty.classList.remove("hidden"); empty.classList.add("flex");
      // 區分「全部最新」 vs 「篩選無結果」
      const noResultsByFilter = state.installs.some((i) => i.status === "behind");
      $("#empty-title").textContent = noResultsByFilter ? "沒有符合的結果" : "全部都是最新版 🎉";
      $("#empty-sub").textContent = noResultsByFilter
        ? "試著清除篩選或搜尋條件。"
        : "沒有待處理的更新。下次上游有新版時會出現在這裡。";
    } else {
      empty.classList.add("hidden"); empty.classList.remove("flex");
      tableWrap.classList.remove("hidden");
      cards.classList.remove("hidden");
      const groups = buildGroups(rows);
      tbody.innerHTML = groups
        .map((g) => (g.label == null ? "" : groupHeadRow(g)) + g.rows.map(rowHTML).join(""))
        .join("");
      cards.innerHTML = groups
        .map((g) => (g.label == null ? "" : groupHeadCard(g)) + g.rows.map(cardHTML).join(""))
        .join("");
    }
    renderSummary();
  }

  function renderSummary() {
    const all = state.installs;
    const behind = all.filter((i) => i.status === "behind").length;
    const issues = all.filter((i) => i.status === "error" || i.status === "unknown").length;
    const ok = all.filter((i) => i.status === "up_to_date").length;
    $("#summary").innerHTML = `
      <span class="flex items-center gap-1.5"><span class="w-2 h-2 rounded-full" style="background: var(--warn);"></span>${behind} 可更新</span>
      <span class="flex items-center gap-1.5"><span class="w-2 h-2 rounded-full" style="background: var(--ok);"></span>${ok} 最新</span>
      ${issues ? `<span class="flex items-center gap-1.5"><span class="w-2 h-2 rounded-full" style="background: var(--err);"></span>${issues} 需注意</span>` : ""}`;
  }

  /* ============================================================
     Changelog Modal
     ============================================================ */
  async function openChangelog(key) {
    // key 格式：software@latest_version（由 changelogLink 產生）
    const atIdx = key.lastIndexOf("@");
    const sw  = key.slice(0, atIdx);
    const ver = key.slice(atIdx + 1);

    // 先開 modal，填「載入中」
    $("#modal-software").textContent = sw;
    $("#modal-version").textContent  = "v" + ver;
    $("#modal-date").textContent     = "";
    $("#modal-zh").innerHTML         = `<p style="color: var(--text-3);">載入中…</p>`;
    $("#modal-raw").textContent      = "";
    $("#modal-raw-wrap").open        = false;
    const ov = $("#modal-overlay");
    ov.classList.remove("hidden"); ov.classList.add("flex");
    requestAnimationFrame(() => (ov.style.opacity = "1"));

    try {
      const v = await api(`/api/changelog/${encodeURIComponent(sw)}/${encodeURIComponent(ver)}`);
      $("#modal-date").textContent = v.released_at ? "發布於 " + v.released_at : "";
      $("#modal-zh").innerHTML     = mdToHtml(v.changelog_zh || "");
      $("#modal-raw").textContent  = v.changelog_raw || "";
    } catch (e) {
      const msg = e.status === 404 ? "尚無 changelog" : "無法載入 changelog";
      $("#modal-zh").innerHTML = `<p style="color: var(--text-3);">${msg}</p>`;
    }
  }
  function closeModal() {
    const ov = $("#modal-overlay");
    ov.style.opacity = "0";
    setTimeout(() => { ov.classList.add("hidden"); ov.classList.remove("flex"); }, 150);
  }
  $("#modal-close").addEventListener("click", closeModal);
  $("#modal-overlay").addEventListener("click", (e) => { if (e.target.id === "modal-overlay") closeModal(); });

  // 極簡 markdown：**粗體**、`code`、- 條列
  function mdToHtml(src) {
    const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    const inline = (s) =>
      esc(s).replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>").replace(/`(.+?)`/g, "<code>$1</code>");
    const lines = src.split("\n");
    let html = "", inList = false;
    for (const ln of lines) {
      const m = ln.match(/^\s*-\s+(.*)$/);
      if (m) { if (!inList) { html += "<ul>"; inList = true; } html += `<li>${inline(m[1])}</li>`; }
      else { if (inList) { html += "</ul>"; inList = false; } if (ln.trim()) html += `<p>${inline(ln)}</p>`; }
    }
    if (inList) html += "</ul>";
    return html;
  }

  /* ============================================================
     更新 Job：建立 → 開 drawer → 串流 log → 完成更新主清單
     ============================================================ */
  function openDrawer() {
    $("#overlay").classList.remove("hidden");
    requestAnimationFrame(() => $("#drawer").classList.add("open"));
  }
  function closeDrawer() {
    $("#drawer").classList.remove("open");
    setTimeout(() => $("#overlay").classList.add("hidden"), 300);
  }
  $("#drawer-close").addEventListener("click", closeDrawer);
  $("#overlay").addEventListener("click", closeDrawer);

  /* ---- 版面切換：側欄 ⇄ 底部 ---- */
  function setLayout(mode) {
    state.layout = mode;
    const d = $("#drawer");
    d.classList.toggle("side", mode === "side");
    d.classList.toggle("bottom", mode === "bottom");
    d.setAttribute("data-layout", mode);
    document.querySelectorAll("[data-layout-btn]").forEach((b) =>
      b.classList.toggle("active", b.getAttribute("data-layout-btn") === mode));
    localStorage.setItem("cockpit-panel-layout", mode);
  }
  document.querySelectorAll("[data-layout-btn]").forEach((b) =>
    b.addEventListener("click", () => setLayout(b.getAttribute("data-layout-btn"))));

  async function startUpdate(id) {
    const it = state.installs.find((i) => i.id === id) || INSTALLS.find((i) => i.id === id);
    if (!it) return;
    const [sw, machine] = id.split("::");
    let resp;
    try {
      resp = await api(`/api/installs/${encodeURIComponent(sw)}/${encodeURIComponent(machine)}/update`, { method: "POST" });
    } catch (e) {
      if (e.status === 409) {
        toast("warn", `${sw} 已有進行中的更新`);
      } else if (e.status === 404) {
        toast("err", `找不到 ${sw} 的安裝記錄`);
      } else {
        toast("err", `觸發更新失敗：${e.message}`);
      }
      return;
    }
    const job = {
      id: String(resp.job_id),
      software: sw,
      machine: machine,
      kind: it.update_kind || "command",
      status: "running",
      installId: id,
      started_at: new Date().toISOString(),
      log: [],
    };

    it.status = "updating";
    state.activeJobId = job.id;
    state.jobs.unshift(job);
    render();
    renderCurrentJob(job);
    openDrawer();
    streamJob(job);
  }

  function streamJob(job) {
    const es = new EventSource(`/api/jobs/${job.id}/log/stream`);
    state.streams = state.streams || {};
    state.streams[job.id] = es;
    es.addEventListener("log", (e) => {
      job.log.push(e.data);
      appendLogLine(job, e.data);
    });
    es.addEventListener("done", async (e) => {
      es.close();
      delete state.streams[job.id];
      job.status = e.data;           // "success" | "failed" | "aborted"
      job.finished_at = new Date().toISOString();
      try { await loadInstalls(); await loadJobs(); } catch (_) {}
      // SSE done 只給 status；從剛刷新的 JOBS 回填版本/退出碼，避免「已更新至 undefined」
      const fresh = JOBS.find((j) => j.id === job.id);
      if (fresh) { job.new_version = fresh.new_version; job.exit_code = fresh.exit_code; }
      finishJob(job);
      render();
      renderRecentJobs();
    });
    es.addEventListener("error", (e) => {
      // SSE 錯誤事件（job 找不到等）：關閉，不重連
      if (e.data) {
        job.log.push(`[error] ${e.data}`);
        appendLogLine(job, `[error] ${e.data}`);
      }
      es.close();
      delete state.streams?.[job.id];
    });
    es.onerror = () => {
      // 連線中斷：關閉，不無限重連
      es.close();
      if (state.streams) delete state.streams[job.id];
    };
  }

  async function abortJob(jobId) {
    const job = state.jobs.find((j) => j.id === jobId);
    if (!job || job.status !== "running") return;
    // 先 disable abort 鈕，避免重複點擊
    const btn = document.querySelector(`[data-abort="${jobId}"]`);
    if (btn) btn.disabled = true;
    try {
      await api(`/api/jobs/${jobId}/abort`, { method: "POST" });
    } catch (_) {
      // 忽略錯誤；SSE done event 仍會收到並完成收尾
    }
    // 不在本地 finalize job；由 SSE done (data="aborted") 完成 UI 收尾
  }

  function finishJob(job) {
    // job.status is already set by SSE done event ("success" | "failed" | "aborted")
    // INSTALLS / state.installs already refreshed by loadInstalls() before this call
    if (job.status === "success") {
      const fresh = INSTALLS.find((i) => i.id === job.installId);
      const it = state.installs.find((i) => i.id === job.installId);
      if (fresh && it) {
        it.current_version = fresh.current_version;
        it.status = fresh.status;
        it.behind_count = fresh.behind_count ?? 0;
      }
      toast("ok", `${job.software} 更新成功`);
    } else if (job.status === "aborted") {
      const it = state.installs.find((i) => i.id === job.installId);
      if (it) it.status = "behind";
      toast("warn", `已中止 ${job.software} 的更新`);
    } else {
      // "failed" or anything unexpected
      const it = state.installs.find((i) => i.id === job.installId);
      if (it) it.status = "behind";
      toast("err", `${job.software} 更新失敗`);
    }
    if (state.activeJobId === job.id) renderCurrentJob(job);
    renderRecentJobs();
  }

  /* ---- 當前 job 視圖 ---- */
  function jobStatusPill(status) {
    const map = {
      queued:  ["b-mut",  "排隊中"],
      running: ["b-warn", "執行中"],
      success: ["b-ok",   "成功"],
      failed:  ["b-err",  "失敗"],
      aborted: ["b-mut",  "已中止"],
    };
    const [cls, label] = map[status] || map.queued;
    const icon = status === "running"
      ? `<svg class="spin" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round"><path d="M21 12a9 9 0 1 1-6.22-8.56"/></svg>`
      : `<span class="dot"></span>`;
    return `<span class="badge ${cls}">${icon}${label}</span>`;
  }

  function renderCurrentJob(job) {
    const el = $("#job-current");
    const isAgent = job.kind === "agent";
    el.innerHTML = `
      <div class="px-4 py-3.5 border-b" style="border-color: var(--border);">
        <div class="flex items-center justify-between gap-3">
          <div class="flex items-center gap-2 flex-wrap">
            <span class="font-display font-semibold text-[14px]">${job.software}</span>
            <span class="text-[12px]" style="color: var(--text-3);">${job.machine}</span>
            <span class="kind" style="${isAgent?"color: var(--accent); border-color: var(--accent);":""}">${isAgent ? "Agent" : "指令"}</span>
          </div>
          ${jobStatusPill(job.status)}
        </div>
        <div class="flex items-center gap-2 mt-2.5">
          ${job.status === "running"
            ? `<button class="btn btn-ghost btn-xs" style="color: var(--err);" data-abort="${job.id}"><svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><rect x="6" y="6" width="12" height="12" rx="1.5"/></svg>中止</button>`
            : ""}
          ${(job.status === "failed" || job.status === "aborted")
            ? `<button class="btn btn-primary btn-xs" data-retry="${job.installId}"><svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.64-6.36"/><path d="M21 3v6h-6"/></svg>重試</button>`
            : ""}
          ${job.status === "success"
            ? `<span class="text-[12px] flex items-center gap-1.5" style="color: var(--ok);"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>已更新至 <span class="mono">${job.new_version}</span></span>`
            : ""}
        </div>
        ${isAgent ? `
          <div class="mt-2.5 text-[12px]" style="color: var(--text-2);">
            <span style="color: var(--text-3);">runner</span>
            <span class="mono ml-1">${job.runner}</span>
          </div>
          <details class="mt-2 group">
            <summary class="cursor-pointer text-[12px] font-medium select-none flex items-center gap-1.5" style="color: var(--text-3);">
              <svg class="transition-transform group-open:rotate-90" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg>
              所用 prompt
            </summary>
            <pre class="mono text-[11.5px] mt-1.5 p-2.5 rounded-lg whitespace-pre-wrap" style="background: var(--surface-2); color: var(--text-2); border: 1px solid var(--border);">${(job.prompt||"").replace(/</g,"&lt;")}</pre>
          </details>` : ""}
      </div>
      <div class="flex-1 flex flex-col min-h-[220px]">
        <div class="flex items-center justify-between px-4 py-2 text-[11px] uppercase tracking-wider" style="color: var(--text-3);">
          <span>即時 log</span>
          <span class="mono normal-case" style="color: var(--text-3);">${job.machine}</span>
        </div>
        <div id="term" class="term flex-1 overflow-y-auto px-4 py-3 mx-3 mb-3 rounded-lg"></div>
      </div>`;
    const term = $("#term");
    term.innerHTML = job.log.map((l) => `<div class="term-line">${colorize(l)}</div>`).join("");
    term.scrollTop = term.scrollHeight;
  }

  function appendLogLine(job, line) {
    if (state.activeJobId !== job.id) return;
    const term = $("#term");
    if (!term) return;
    const div = document.createElement("div");
    div.className = "term-line in";
    div.innerHTML = colorize(line);
    term.appendChild(div);
    term.scrollTop = term.scrollHeight;   // 自動捲到底
  }

  // 依符號上色（▶ 標題 / → 步驟 / ✓ 成功 / ✗ 失敗）
  function colorize(line) {
    const esc = line.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    if (/^✓/.test(line)) return `<span style="color:#4ade80;">${esc}</span>`;
    if (/^✗/.test(line)) return `<span style="color:#f87171;">${esc}</span>`;
    if (/^■/.test(line)) return `<span style="color:#9aa4b2;">${esc}</span>`;
    if (/^▶/.test(line)) return `<span style="color:#7dd3fc;font-weight:600;">${esc}</span>`;
    if (/^→/.test(line)) return `<span style="color:#c4b5fd;">${esc}</span>`;
    return `<span style="opacity:.82;">${esc}</span>`;
  }

  /* ---- 最近工作清單 ---- */
  function renderRecentJobs() {
    const list = $("#recent-list");
    $("#recent-count").textContent = `(${state.jobs.length})`;
    // 頭部「進行中 N」徽章
    const running = state.jobs.filter((j) => j.status === "running").length;
    const ac = $("#active-count");
    if (running > 0) { ac.style.display = ""; ac.innerHTML = `<span class="dot pulse-dot"></span>進行中 ${running}`; }
    else ac.style.display = "none";
    list.innerHTML = state.jobs.map((j) => {
      const active = j.id === state.activeJobId;
      const map = { running: ["var(--warn)","執行中"], success: ["var(--ok)","成功"], failed: ["var(--err)","失敗"], aborted: ["var(--mut)","已中止"], queued: ["var(--mut)","排隊中"] };
      const [c, lbl] = map[j.status] || map.queued;
      return `
        <button class="text-left rounded-lg border px-3 py-2 transition-colors ${active?"":"row-hover"}"
                style="border-color: var(--border); ${active?"background: var(--surface-2);":""}" data-job="${j.id}">
          <div class="flex items-center justify-between gap-2">
            <span class="text-[12.5px] font-medium">${j.software}</span>
            <span class="flex items-center gap-1.5 text-[11px]" style="color:${c};">
              <span class="w-1.5 h-1.5 rounded-full ${j.status==="running"?"pulse-dot":""}" style="background:${c};"></span>${lbl}
            </span>
          </div>
          <div class="flex items-center gap-2 mt-1 text-[11px]" style="color: var(--text-3);">
            <span>${j.machine}</span><span>·</span>
            <span class="kind" style="padding:.02rem .3rem;">${j.kind==="agent"?"agent":"cmd"}</span>
            ${j.new_version && j.status==="success" ? `<span class="mono">→ ${j.new_version}</span>` : ""}
          </div>
        </button>`;
    }).join("");
  }

  $("#recent-toggle").addEventListener("click", () => {
    const list = $("#recent-list");
    const open = list.classList.toggle("hidden");
    $("#recent-chevron").style.transform = open ? "" : "rotate(180deg)";
  });

  /* ============================================================
     立即檢查（模擬：loading → 還原 + 更新時間）
     ============================================================ */
  function showLoading() {
    const ls = $("#loading-state");
    $("#table-wrap").classList.add("hidden");
    $("#card-list").classList.add("hidden");
    $("#empty-state").classList.add("hidden");
    ls.classList.remove("hidden"); ls.classList.add("flex");
    ls.innerHTML = Array.from({ length: 6 }).map(() => `
      <div class="flex items-center gap-3 px-1 py-2.5 animate-pulse">
        <div class="h-3.5 rounded" style="background: var(--surface-2); width: 22%;"></div>
        <div class="h-3.5 rounded" style="background: var(--surface-2); width: 12%;"></div>
        <div class="h-3.5 rounded" style="background: var(--surface-2); width: 14%;"></div>
        <div class="flex-1"></div>
        <div class="h-5 rounded-full" style="background: var(--surface-2); width: 70px;"></div>
      </div>`).join("");
  }
  function hideLoading() {
    const ls = $("#loading-state");
    ls.classList.add("hidden"); ls.classList.remove("flex");
    render();
  }
  $("#check-btn").addEventListener("click", async () => {
    const btn = $("#check-btn"), icon = $("#check-icon"), label = $("#check-label");
    btn.disabled = true; icon.classList.add("spin"); label.textContent = "檢查中…";
    showLoading();
    try {
      await api("/api/check", { method: "POST" });
    } catch (_) {
      // 即使 check 失敗也繼續重新拉資料
    }
    // 等約 2s 讓後端非同步刷新後再拉新資料
    await new Promise((r) => setTimeout(r, 2000));
    try {
      await loadInstalls();
      state.installs = structuredClone(INSTALLS);
      populateMachineFilter();
    } catch (_) {}
    hideLoading();
    btn.disabled = false; icon.classList.remove("spin"); label.textContent = "立即檢查";
    $("#last-checked").textContent = "剛剛";
    toast("ok", "已重新檢查所有來源");
  });

  /* ============================================================
     toast
     ============================================================ */
  let toastTimer;
  function toast(kind, msg) {
    const el = $("#toast");
    $("#toast-msg").textContent = msg;
    $("#toast-icon").innerHTML = kind === "ok"
      ? `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--ok)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`
      : kind === "warn"
      ? `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--warn)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><path d="M12 9v4M12 17h.01"/></svg>`
      : `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--err)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v5M12 16h.01"/></svg>`;
    el.classList.remove("hidden");
    el.firstElementChild.classList.add("pop-in");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.add("hidden"), 2600);
  }

  /* ============================================================
     事件委派（清單 / drawer）
     ============================================================ */
  document.addEventListener("click", (e) => {
    const cl = e.target.closest("[data-changelog]");
    if (cl) return openChangelog(cl.getAttribute("data-changelog"));

    const up = e.target.closest("[data-update]");
    if (up) return startUpdate(up.getAttribute("data-update"));

    const ab = e.target.closest("[data-abort]");
    if (ab) return abortJob(ab.getAttribute("data-abort"));

    const rt = e.target.closest("[data-retry]");
    if (rt) return startUpdate(rt.getAttribute("data-retry"));

    const rc = e.target.closest("[data-recheck]");
    if (rc) { toast("ok", "已重新檢查此來源"); return; }

    const jb = e.target.closest("[data-job]");
    if (jb) {
      const jobId = jb.getAttribute("data-job");
      const cached = state.jobs.find((j) => j.id === jobId);
      if (cached) {
        state.activeJobId = cached.id; renderCurrentJob(cached); renderRecentJobs();
        // 若 job 已結束，重抓最新資料再更新顯示
        if (cached.status !== "running") {
          api(`/api/jobs/${encodeURIComponent(jobId)}`).then((raw) => {
            const fresh = {
              ...raw,
              id: String(raw.id),
              installId: raw.software + "::" + raw.machine,
              log: (raw.log || "").split("\n").filter(Boolean),
            };
            const idx = state.jobs.findIndex((j) => j.id === fresh.id);
            if (idx >= 0) state.jobs[idx] = fresh;
            if (state.activeJobId === fresh.id) renderCurrentJob(fresh);
            renderRecentJobs();
          }).catch(() => {/* silent – already showing cached data */});
        }
      }
    }
  });

  // 鍵盤：Esc 先關 modal，再關 drawer
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    if (!$("#modal-overlay").classList.contains("hidden")) return closeModal();
    if (!$("#overlay").classList.contains("hidden")) return closeDrawer();
  });

  /* ============================================================
     啟動
     ============================================================ */
  function emptyCurrentJob() {
    $("#job-current").innerHTML = `
      <div class="flex-1 grid place-items-center text-center px-6 py-10">
        <div>
          <div class="grid place-items-center w-11 h-11 rounded-xl mx-auto mb-3" style="background: var(--surface-2);">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--text-3)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m22 2-7 20-4-9-9-4Z"/><path d="M22 2 11 13"/></svg>
          </div>
          <div class="text-[13px]" style="color: var(--text-2);">尚未選擇工作</div>
          <div class="text-[12px] mt-1" style="color: var(--text-3);">在清單按「更新」開始，或從下方挑一個最近工作。</div>
        </div>
      </div>`;
  }

  (async () => {
    initTheme();
    initFilters();
    setGroup(localStorage.getItem("cockpit-group") || "flat");
    setLayout(localStorage.getItem("cockpit-panel-layout") || "side");
    emptyCurrentJob();
    // 顯示 server 版本（best-effort）
    try {
      const vr = await api("/api/version");
      const el = $("#server-ver");
      if (el && vr && vr.version) el.textContent = vr.version;
    } catch (_) {}
    try {
      await loadInstalls();
      await loadJobs();
      // 把真實資料填入 state
      state.installs = structuredClone(INSTALLS);
      state.jobs     = structuredClone(JOBS);
      populateMachineFilter();  // MACHINES 已從 loadInstalls 填入
    } catch (e) {
      console.error("cockpit: failed to load data", e);
      showLoadError();
    }
    render();
    renderRecentJobs();
  })();
})();
