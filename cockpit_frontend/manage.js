/*
  cockpit · 管理頁 (vanilla JS) — real API edition
  機器：list / create (with enroll modal) / rename / delete / regen-token
  軟體：list / create / edit / delete
*/
(function () {
  "use strict";

  const $ = (s, r = document) => r.querySelector(s);

  // ── Theme ──────────────────────────────────────────────────────────────────
  function initTheme() {
    const saved = localStorage.getItem("cockpit-theme");
    document.documentElement.classList.toggle("dark", saved ? saved === "dark" : true);
    syncIcon();
  }
  function syncIcon() {
    const dark = document.documentElement.classList.contains("dark");
    $("#icon-moon").style.display = dark ? "" : "none";
    $("#icon-sun").style.display  = dark ? "none" : "";
  }
  $("#theme-btn").addEventListener("click", () => {
    const dark = document.documentElement.classList.toggle("dark");
    localStorage.setItem("cockpit-theme", dark ? "dark" : "light");
    syncIcon();
  });

  // ── API helper ─────────────────────────────────────────────────────────────
  async function api(path, opts) {
    const r = await fetch(path, opts);
    if (!r.ok) {
      const e = new Error(`${path} ${r.status}`);
      e.status = r.status;
      try { const b = await r.json(); e.message = b.error || e.message; } catch (_) {}
      throw e;
    }
    return r.status === 204 ? null : r.json();
  }

  // ── Toast ──────────────────────────────────────────────────────────────────
  let toastTimer;
  function toast(kind, msg) {
    const el = $("#toast");
    $("#toast-msg").textContent = msg;
    $("#toast-icon").innerHTML =
      kind === "ok"
        ? `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--ok)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`
        : kind === "warn"
        ? `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--warn)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><path d="M12 9v4M12 17h.01"/></svg>`
        : `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--err)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v5M12 16h.01"/></svg>`;
    el.style.display = "flex";
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (el.style.display = "none"), 2800);
  }

  // ── State ──────────────────────────────────────────────────────────────────
  let SYSTEMS      = [];  // from GET /api/systems
  let INSTALLS     = [];  // from GET /api/installs
  let serverVersion = ""; // from GET /api/version

  // ── Load data ──────────────────────────────────────────────────────────────
  async function loadAll() {
    try {
      [SYSTEMS, INSTALLS] = await Promise.all([
        api("/api/systems"),
        api("/api/installs"),
      ]);
    } catch (e) {
      toast("err", "資料載入失敗：" + e.message);
    }
    renderMachines();
    renderSoftware();
  }

  // ── Status helpers ─────────────────────────────────────────────────────────
  const HSTATUS = {
    online:  ["s-online", "b-ok",   "線上"],
    warn:    ["s-warn",   "b-warn",  "警告"],
    offline: ["s-offline","b-err",  "離線"],
    pending: ["s-pending pulse","b-info","待加入"],
  };

  const KINDS = [
    ["npm",          "npm 套件"],
    ["pypi",         "PyPI 套件"],
    ["github",       "GitHub Release"],
    ["brew",         "Homebrew"],
    ["claude-plugin","Claude Plugin"],
    ["custom",       "自訂"],
  ];
  const KIND_LABEL = Object.fromEntries(KINDS);

  const WSTATUS = {
    up_to_date: ["b-ok",  "最新"],
    behind:     ["b-warn","落後"],
    unknown:    ["b-mut", "未檢查"],
    error:      ["b-err", "錯誤"],
  };

  // ── Modal helpers ──────────────────────────────────────────────────────────
  function openModal(html) { $("#modal").innerHTML = html; $("#ov").classList.add("show"); }
  function closeModal() { $("#ov").classList.remove("show"); }
  $("#ov").addEventListener("click", (e) => { if (e.target.id === "ov") closeModal(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeModal(); });

  const modalHead = (title, sub) =>
    `<div style="display:flex;align-items:flex-start;justify-content:space-between;gap:12px;padding:17px 18px;border-bottom:1px solid var(--border);">
      <div><div class="font-display" style="font-size:16px;font-weight:700;">${title}</div>${sub ? `<div style="font-size:12px;color:var(--text-3);margin-top:2px;">${sub}</div>` : ""}</div>
      <button class="btn btn-ghost btn-xs" data-close style="flex:none;margin-top:2px;">✕</button>
    </div>`;

  const fieldGroup = (label, inner) =>
    `<div style="margin-bottom:14px;"><div style="font-size:12px;font-weight:550;color:var(--text-2);margin-bottom:6px;">${label}</div>${inner}</div>`;

  // ── Copy helper ────────────────────────────────────────────────────────────
  function copy(text, btn) {
    const done = () => {
      if (btn) { const o = btn.innerHTML; btn.innerHTML = "已複製 ✓"; setTimeout(() => (btn.innerHTML = o), 1400); }
      toast("ok", "已複製到剪貼簿");
    };
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).then(done).catch(() => fallbackCopy(text, done));
    } else {
      fallbackCopy(text, done);
    }
  }
  function fallbackCopy(text, done) {
    const ta = document.createElement("textarea");
    ta.value = text; ta.style.position = "fixed"; ta.style.opacity = "0";
    document.body.appendChild(ta); ta.focus(); ta.select();
    try { document.execCommand("copy"); done(); } catch (_) {}
    document.body.removeChild(ta);
  }

  // ── Enroll modal (shown after create OR regen-token) ───────────────────────
  function showEnrollModal(label, enrollToken) {
    const origin     = location.origin;
    const oneLiner   = `curl -fsSL https://raw.githubusercontent.com/curtis1215/cockpit/main/install.sh | sh -s -- agent ${origin} ${enrollToken}`;
    const configJson = JSON.stringify({ server_url: origin, enroll_token: enrollToken });
    const fallbackCmd = "cockpit agent -config agent.json";

    openModal(`
      ${modalHead("安裝指令", `機器：${label}`)}
      <div style="padding:18px 18px 22px;">
        <p style="font-size:13px;color:var(--text-2);margin:0 0 14px;">
          在目標機器上貼上以下一行指令即可完成安裝與啟動：
        </p>
        ${fieldGroup("一鍵安裝指令", `
          <div class="term" id="enroll-oneliner" style="word-break:break-all;">${escHtml(oneLiner)}
            <button class="btn btn-xs copy-btn" id="copy-oneliner">複製</button>
          </div>
        `)}
        <details style="margin-top:12px;">
          <summary style="font-size:12px;color:var(--text-3);cursor:pointer;user-select:none;">備用：手動設定</summary>
          <div style="margin-top:10px;">
            <p style="font-size:12px;color:var(--text-3);margin:0 0 8px;">
              若 curl 無法使用，可手動建立
              <code class="mono" style="background:var(--surface-2);padding:1px 5px;border-radius:4px;">agent.json</code>，再執行啟動指令：
            </p>
            ${fieldGroup("agent.json 內容", `
              <div class="term" id="enroll-config">${escHtml(configJson)}
                <button class="btn btn-xs copy-btn" id="copy-cfg">複製</button>
              </div>
            `)}
            ${fieldGroup("啟動指令", `
              <div class="term">${escHtml(fallbackCmd)}
                <button class="btn btn-xs copy-btn" id="copy-cmd">複製</button>
              </div>
            `)}
          </div>
        </details>
        <div style="display:flex;justify-content:flex-end;margin-top:16px;">
          <button class="btn btn-primary" data-close>完成</button>
        </div>
      </div>
    `);

    document.getElementById("copy-oneliner").addEventListener("click", function () { copy(oneLiner, this); });
    document.getElementById("copy-cfg").addEventListener("click", function () { copy(configJson, this); });
    document.getElementById("copy-cmd").addEventListener("click", function () { copy(fallbackCmd, this); });
  }

  function escHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  // ── Render: machines ───────────────────────────────────────────────────────
  function renderMachines() {
    const el = $("#machine-list");
    if (!SYSTEMS.length) {
      el.innerHTML = `<div style="padding:30px;text-align:center;color:var(--text-3);font-size:13px;">尚無機器，點「新增機器」開始。</div>`;
      return;
    }
    el.innerHTML = SYSTEMS.map((m) => {
      const st     = m.status || "offline";
      const [dot, , statusLabel] = HSTATUS[st] || HSTATUS.offline;
      const [, badgeCls] = HSTATUS[st] || HSTATUS.offline;
      const swCount = INSTALLS.filter((i) => i.machine === m.label).length;

      const agentVer = m.agent_version || "";
      const showUpgradeBtn = agentVer && serverVersion && agentVer !== serverVersion;

      const metaFrag = st === "pending"
        ? `<span class="badge b-info">待 agent 連線</span>`
        : `<span style="font-size:12px;color:var(--text-3);">${m.os || ""}${m.arch ? " / " + m.arch : ""}${agentVer ? " · v" + agentVer : ""}</span>`;
      const groupPlaceholder = m.kind === "vm" && !m.group && m.effective_group
        ? "繼承：" + m.effective_group
        : "群組";

      return `
        <div class="mrow" data-machine-id="${escHtml(m.id)}">
          <span class="sdot ${dot}"></span>
          <input class="inline-name" data-rename="${escHtml(m.id)}"
                 value="${escHtml(m.label)}" title="點擊可重新命名" style="flex:none;" />
          <span class="badge ${badgeCls}" style="flex:none;">${statusLabel}</span>
          ${metaFrag}
          <input class="inline-name" list="grp-datalist" data-grpedit="${escHtml(m.id)}"
                 value="${escHtml(m.group || "")}"
                 placeholder="${escHtml(groupPlaceholder)}"
                 title="群組（留空 = ${m.kind === "vm" ? "繼承宿主機" : "未分組"}）"
                 style="flex:none;width:104px;font-size:12px;" />
          <span style="flex:1;"></span>
          <span style="font-size:12px;color:var(--text-3);flex:none;">${swCount} 套軟體</span>
          ${showUpgradeBtn ? `<button class="btn btn-xs" data-upgrade-agent="${escHtml(m.id)}" title="升級 agent 至 v${escHtml(serverVersion)}" style="color:var(--warn);">⬆ 升級 agent</button>` : ""}
          <button class="btn btn-xs" data-regen-token="${escHtml(m.id)}" title="重新產生 Enroll Token">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 4v6h-6"/><path d="M1 20v-6h6"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
            重生 Token
          </button>
          <button class="btn btn-danger btn-xs" data-rmmachine="${escHtml(m.id)}" title="移除機器">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
            移除
          </button>
        </div>`;
    }).join("");
    const grpNames = [...new Set(SYSTEMS.map((s) => s.group).filter(Boolean))].sort((a, b) => a.localeCompare(b));
    el.insertAdjacentHTML("beforeend",
      `<datalist id="grp-datalist">${grpNames.map((g) => `<option value="${escHtml(g)}">`).join("")}</datalist>`);
  }

  // ── Render: software ───────────────────────────────────────────────────────
  function renderSoftware() {
    const el = $("#software-list");
    if (!SYSTEMS.length) {
      el.innerHTML = `<div style="padding:30px;text-align:center;color:var(--text-3);font-size:13px;">尚無機器，請先新增機器。</div>`;
      return;
    }

    let html = "";
    let anyInstalls = false;

    SYSTEMS.forEach((m) => {
      const list = INSTALLS.filter((i) => i.machine === m.label);
      const [dot] = HSTATUS[m.status || "offline"] || HSTATUS.offline;

      html += `<div class="grp-label"><span class="sdot ${dot}"></span>${escHtml(m.label)} <span class="tag">${escHtml(m.id)}</span><span style="margin-left:auto;font-weight:400;text-transform:none;letter-spacing:0;">${list.length} 項</span></div>`;

      if (!list.length) {
        html += `<div class="mrow" style="color:var(--text-3);font-size:12.5px;">（此機器尚無追蹤軟體）</div>`;
      } else {
        anyInstalls = true;
        list.forEach((w) => {
          const [scls, slbl] = WSTATUS[w.status] || WSTATUS.unknown;
          const statusBadge = `<span class="badge ${scls}">${slbl}${w.status === "behind" && w.behind_count ? " " + w.behind_count : ""}</span>`;
          const kindLbl     = KIND_LABEL[w.kind] || w.kind || "—";
          const updateLabel = w.update_kind === "agent" ? "agent" : "command";

          html += `
            <div class="mrow">
              <div style="flex:1;min-width:0;">
                <div style="font-weight:600;font-size:14px;margin-bottom:2px;">${escHtml(w.software)}</div>
                <div style="font-size:11.5px;color:var(--text-3);">${kindLbl} · 更新：${updateLabel}</div>
              </div>
              ${statusBadge}
              <span style="font-size:12px;color:var(--text-3);flex:none;">${escHtml(w.current_version || "—")}</span>
              <button class="btn btn-xs" data-editsw="${escHtml(w.software + "::" + w.machine)}">編輯</button>
              <button class="btn btn-danger btn-xs" data-rmsw="${escHtml(w.software + "::" + w.machine)}">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
                移除
              </button>
            </div>`;
        });
      }
    });

    el.innerHTML = html || `<div style="padding:30px;text-align:center;color:var(--text-3);font-size:13px;">尚無軟體追蹤。</div>`;
  }

  // ── Add machine modal ──────────────────────────────────────────────────────
  function addMachineModal() {
    openModal(`
      ${modalHead("新增機器", "建立後會產生 Enroll Token 及安裝指令")}
      <div style="padding:18px 18px 22px;">
        ${fieldGroup("機器名稱 *", `<input id="am-name" class="field" placeholder="例：prod-web-01" />`)}
        ${fieldGroup("角色（選填）", `<input id="am-role" class="field" placeholder="例：web / db / worker" />`)}
        <div style="display:flex;justify-content:flex-end;gap:8px;margin-top:6px;">
          <button class="btn" data-close>取消</button>
          <button class="btn btn-primary" id="am-gen">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M5 12h14"/></svg>
            建立並取得安裝指令
          </button>
        </div>
      </div>
    `);

    $("#am-name").focus();
    $("#am-gen").addEventListener("click", async () => {
      const label = ($("#am-name").value || "").trim();
      const role  = ($("#am-role").value || "").trim();
      if (!label) { toast("warn", "請填寫機器名稱"); return; }

      const btn = $("#am-gen");
      btn.disabled = true;
      try {
        const res = await api("/api/systems", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ label, role: role || undefined }),
        });
        await loadAll();
        showEnrollModal(label, res.enroll_token);
      } catch (e) {
        if (e.status === 409) toast("warn", "機器名稱已存在");
        else toast("err", "新增失敗：" + e.message);
        btn.disabled = false;
      }
    });
  }

  // ── Regen enroll token ─────────────────────────────────────────────────────
  async function regenToken(id) {
    const m = SYSTEMS.find((s) => s.id === id);
    if (!m) return;
    try {
      const res = await api(`/api/systems/${encodeURIComponent(id)}/enroll-token`, { method: "POST" });
      showEnrollModal(m.label, res.enroll_token);
    } catch (e) {
      toast("err", "重生 Token 失敗：" + e.message);
    }
  }

  // ── Rename machine ─────────────────────────────────────────────────────────
  async function renameMachine(id, newLabel, inputEl) {
    const m = SYSTEMS.find((s) => s.id === id);
    if (!m) return;
    if (newLabel === m.label) return;
    try {
      await api(`/api/systems/${encodeURIComponent(id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ label: newLabel }),
      });
      toast("ok", "已重新命名");
      await loadAll();
    } catch (e) {
      if (e.status === 409) toast("warn", "該機器有軟體綁定，暫不支援改名");
      else toast("err", "改名失敗：" + e.message);
      if (inputEl) inputEl.value = m.label; // revert
    }
  }

  // ── Set machine group ──────────────────────────────────────────────────────
  async function setMachineGroup(id, grp, inputEl) {
    const m = SYSTEMS.find((s) => s.id === id);
    if (!m) return;
    if (grp === (m.group || "")) return;
    try {
      await api(`/api/systems/${encodeURIComponent(id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ group: grp }),
      });
      toast("ok", grp ? `已設定群組：${grp}` : "已清除群組");
      await loadAll();
    } catch (e) {
      toast("err", "群組更新失敗：" + e.message);
      if (inputEl) inputEl.value = m.group || ""; // revert
    }
  }

  // ── Delete machine ─────────────────────────────────────────────────────────
  async function deleteMachine(id) {
    const m = SYSTEMS.find((s) => s.id === id);
    if (!m) return;
    if (!confirm(`確定移除機器「${m.label}」？其追蹤的軟體也會一併移除。`)) return;
    try {
      await api(`/api/systems/${encodeURIComponent(id)}`, { method: "DELETE" });
      toast("ok", `已移除 ${m.label}`);
      await loadAll();
    } catch (e) {
      toast("err", "移除失敗：" + e.message);
    }
  }

  // ── Software modal (add / edit) ────────────────────────────────────────────
  function softwareModal(existing) {
    // existing is an install row from INSTALLS; for edit we know software+machine
    const w   = existing || {};
    // machine dropdown: use SYSTEMS labels (since installs.machine == label)
    const machineOpts = SYSTEMS.map((m) =>
      `<option value="${escHtml(m.label)}" ${w.machine === m.label ? "selected" : ""}>${escHtml(m.label)}</option>`
    ).join("");
    const kindOpts = KINDS.map(([v, l]) =>
      `<option value="${v}" ${w.kind === v ? "selected" : ""}>${l}</option>`
    ).join("");

    const strat = w.update_kind === "agent" ? "agent" : "command";

    openModal(`
      ${modalHead(existing ? "編輯軟體追蹤" : "新增軟體追蹤", "")}
      <div style="padding:18px 18px 4px;overflow-y:auto;max-height:calc(88vh - 120px);">
        ${fieldGroup("軟體名稱 *", `<input id="sw-name" class="field mono" placeholder="例：@anthropic-ai/claude-code" value="${escHtml(w.software || "")}" ${existing ? "readonly" : ""} />`)}
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;">
          <div>
            ${fieldGroup("機器 *", `<select id="sw-machine" class="field">${machineOpts}</select>`)}
          </div>
          <div>
            ${fieldGroup("類型", `<select id="sw-kind" class="field">${kindOpts}</select>`)}
          </div>
        </div>
        ${fieldGroup("來源 / 最新版本識別 *", `<input id="sw-source" class="field mono" placeholder="例：@anthropic-ai/claude-code 或 owner/repo" value="${escHtml(w.latest_source || "")}" />`)}
        ${fieldGroup("目前版本指令 *", `<input id="sw-current-cmd" class="field mono" placeholder="例：node -e &quot;console.log(require('/usr/lib/claude-code/package.json').version)&quot;" value="${escHtml(w.current_cmd || "")}" />`)}
        ${fieldGroup("版本正規式（選填）", `<input id="sw-version-regex" class="field mono" placeholder="例：v?([\\d.]+)" value="${escHtml(w.version_regex || "")}" />`)}
        ${fieldGroup("Changelog（選填）", `<input id="sw-changelog" class="field" placeholder="例：owner/repo 或空白" value="${escHtml(w.changelog || "")}" />`)}
        ${fieldGroup("更新策略", `
          <div class="seg" id="sw-strat">
            <button data-strat="command" class="${strat === "command" ? "active" : ""}">指令更新</button>
            <button data-strat="agent"   class="${strat === "agent"   ? "active" : ""}">委派 agent</button>
          </div>
          <div id="sw-strat-hint" style="font-size:11.5px;color:var(--text-3);margin-top:6px;"></div>
          <div id="sw-strat-field" style="margin-top:10px;"></div>
        `)}
        <div style="display:flex;justify-content:flex-end;gap:8px;padding:14px 0 4px;">
          <button class="btn" data-close>取消</button>
          <button class="btn btn-primary" id="sw-save">${existing ? "儲存" : "新增"}</button>
        </div>
      </div>
    `);

    // strategy toggle state
    let curStrat = strat;
    let cmdVal    = w.update_command || "";
    let promptVal = w.update_prompt  || "";
    let runnerVal = w.update_runner  || "";
    let cwdVal    = w.update_cwd     || "";

    const hint = $("#sw-strat-hint");
    const setHint = () => {
      hint.textContent = curStrat === "agent"
        ? "由 AI agent 決定如何更新"
        : "以 shell 指令執行更新";
    };

    function renderStratField() {
      const box = $("#sw-strat-field");
      if (curStrat === "agent") {
        box.innerHTML = `
          ${fieldGroup("Runner", `<input id="sw-runner" class="field mono" placeholder="例：claude-code" value="${escHtml(runnerVal)}" />`)}
          ${fieldGroup("Prompt", `<textarea id="sw-prompt" class="field" rows="3" placeholder="更新指示…">${escHtml(promptVal)}</textarea>`)}
          ${fieldGroup("工作目錄（選填）", `<input id="sw-cwd" class="field mono" placeholder="/path/to/dir" value="${escHtml(cwdVal)}" />`)}
        `;
        $("#sw-runner").addEventListener("input", (e) => (runnerVal = e.target.value));
        $("#sw-prompt").addEventListener("input", (e) => (promptVal = e.target.value));
        $("#sw-cwd").addEventListener("input",    (e) => (cwdVal    = e.target.value));
      } else {
        box.innerHTML = fieldGroup("更新指令", `<input id="sw-cmd" class="field mono" placeholder="例：npm update -g @anthropic-ai/claude-code" value="${escHtml(cmdVal)}" />`);
        $("#sw-cmd").addEventListener("input", (e) => (cmdVal = e.target.value));
      }
    }

    setHint();
    renderStratField();

    $("#sw-strat").addEventListener("click", (e) => {
      const b = e.target.closest("[data-strat]");
      if (!b) return;
      curStrat = b.getAttribute("data-strat");
      $("#sw-strat").querySelectorAll("button").forEach((btn) =>
        btn.classList.toggle("active", btn.getAttribute("data-strat") === curStrat)
      );
      setHint();
      renderStratField();
    });

    $("#sw-save").addEventListener("click", async () => {
      const name     = ($("#sw-name").value || "").trim();
      const machine  = ($("#sw-machine").value || "").trim();
      const kind     = ($("#sw-kind").value || "").trim();
      const source   = ($("#sw-source").value || "").trim();
      const curCmd   = ($("#sw-current-cmd").value || "").trim();
      const vRegex   = ($("#sw-version-regex").value || "").trim();
      const changelog= ($("#sw-changelog").value || "").trim();

      if (!name)    { toast("warn", "請填寫軟體名稱"); return; }
      if (!machine) { toast("warn", "請選擇機器"); return; }
      if (!curCmd)  { toast("warn", "請填寫目前版本指令"); return; }
      if (!existing && !source) { toast("warn", "請填寫來源"); return; }

      // Build update object
      let update;
      if (curStrat === "agent") {
        if (!runnerVal) { toast("warn", "Agent 更新需填寫 Runner"); return; }
        if (!promptVal) { toast("warn", "Agent 更新需填寫 Prompt"); return; }
        update = { type: "agent", runner: runnerVal, prompt: promptVal, cwd: cwdVal || undefined };
      } else if (cmdVal) {
        update = { type: "command", cmd: cmdVal };
      }

      const btn = $("#sw-save");
      btn.disabled = true;

      try {
        if (existing) {
          await api(`/api/software/${encodeURIComponent(name)}/${encodeURIComponent(machine)}`, {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              kind:          kind   || undefined,
              latest_source: source || undefined,
              changelog:     changelog || undefined,
              current_cmd:   curCmd,
              version_regex: vRegex || undefined,
              update:        update,
            }),
          });
          toast("ok", `已更新 ${name}`);
        } else {
          await api("/api/software", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name, kind, latest_source: source, changelog: changelog || undefined,
              machine, current_cmd: curCmd, version_regex: vRegex || undefined,
              update: update,
            }),
          });
          toast("ok", `已新增 ${name}`);
        }
        closeModal();
        await loadAll();
      } catch (e) {
        if (e.status === 409) toast("warn", "軟體（同機器）已存在");
        else toast("err", "儲存失敗：" + e.message);
        btn.disabled = false;
      }
    });
  }

  // ── Delete software install ────────────────────────────────────────────────
  async function deleteSoftware(name, machine) {
    if (!confirm(`確定不再追蹤軟體「${name}」（${machine}）？`)) return;
    try {
      await api(`/api/software/${encodeURIComponent(name)}/${encodeURIComponent(machine)}`, {
        method: "DELETE",
      });
      toast("ok", `已移除 ${name}`);
      await loadAll();
    } catch (e) {
      toast("err", "移除失敗：" + e.message);
    }
  }

  // ── Event delegation ───────────────────────────────────────────────────────
  $("#add-machine").addEventListener("click", addMachineModal);
  $("#add-software").addEventListener("click", () => softwareModal(null));

  // modal 關閉委派
  $("#modal").addEventListener("click", (e) => {
    if (e.target.closest("[data-close]")) closeModal();
  });

  // 機器清單
  $("#machine-list").addEventListener("click", (e) => {
    const upgrade = e.target.closest("[data-upgrade-agent]");
    if (upgrade) return upgradeAgent(upgrade.getAttribute("data-upgrade-agent"));

    const regen = e.target.closest("[data-regen-token]");
    if (regen) return regenToken(regen.getAttribute("data-regen-token"));

    const rm = e.target.closest("[data-rmmachine]");
    if (rm) return deleteMachine(rm.getAttribute("data-rmmachine"));
  });

  // rename on blur / enter
  $("#machine-list").addEventListener("change", (e) => {
    const g = e.target.closest("[data-grpedit]");
    if (g) {
      setMachineGroup(g.getAttribute("data-grpedit"), g.value.trim(), g);
      return;
    }
    const r = e.target.closest("[data-rename]");
    if (!r) return;
    const id = r.getAttribute("data-rename");
    const v  = r.value.trim();
    const m  = SYSTEMS.find((s) => s.id === id);
    if (v && m && v !== m.label) renameMachine(id, v, r);
    else if (m) r.value = m.label;
  });
  $("#machine-list").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.target.closest("[data-rename]") || e.target.closest("[data-grpedit]"))) e.target.blur();
  });

  // 軟體清單
  $("#software-list").addEventListener("click", (e) => {
    const ed = e.target.closest("[data-editsw]");
    if (ed) {
      const key  = ed.getAttribute("data-editsw"); // "software::machine"
      const sep  = key.indexOf("::");
      const name    = key.slice(0, sep);
      const machine = key.slice(sep + 2);
      const row  = INSTALLS.find((i) => i.software === name && i.machine === machine);
      if (row) softwareModal(row);
      return;
    }
    const rm = e.target.closest("[data-rmsw]");
    if (rm) {
      const key     = rm.getAttribute("data-rmsw");
      const sep     = key.indexOf("::");
      const name    = key.slice(0, sep);
      const machine = key.slice(sep + 2);
      deleteSoftware(name, machine);
    }
  });

  // ── Upgrade agent ─────────────────────────────────────────────────────────
  async function upgradeAgent(id) {
    try {
      await api(`/api/systems/${encodeURIComponent(id)}/upgrade-agent`, { method: "POST" });
      toast("ok", "已派送升級（macOS 約 30 秒；Linux 視服務設定最長 2 分鐘）");
      setTimeout(loadAll, 35000);
    } catch (e) {
      toast("err", "升級派送失敗：" + e.message);
    }
  }

  // ── Init ───────────────────────────────────────────────────────────────────
  initTheme();
  // 顯示 server 版本（best-effort）並存入 serverVersion
  function refreshServerVersion() {
    return api("/api/version").then((vr) => {
      const el = document.getElementById("server-ver");
      if (vr && vr.version) {
        serverVersion = vr.version;
        if (el) el.textContent = vr.version;
      }
      const btn = document.getElementById("server-upgrade-btn");
      if (btn) {
        if (vr && vr.update_available) {
          btn.textContent = `↑ 升級 Server 到 v${vr.latest}`;
          btn.style.display = "inline-flex";
        } else {
          btn.style.display = "none";
        }
      }
      return vr;
    }).catch(() => null);
  }

  async function pollServerRestart(oldVersion, deadline) {
    while (Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 2500));
      const vr = await refreshServerVersion();
      if (vr && vr.version && vr.version !== oldVersion) return vr;
      if (vr && vr.update_available === false && vr.latest && vr.version === vr.latest) return vr;
    }
    return null;
  }

  const upgradeBtn = document.getElementById("server-upgrade-btn");
  if (upgradeBtn) {
    upgradeBtn.addEventListener("click", async () => {
      if (!confirm("升級會重啟 server（約 10–30 秒），確定？")) return;
      const oldVersion = serverVersion;
      upgradeBtn.disabled = true;
      try {
        toast("info", "升級中… server 將會重啟");
        const res = await api("/api/server/upgrade", { method: "POST" });
        if (res && res.status === "up_to_date") {
          toast("ok", "Server 已是最新版本");
          await refreshServerVersion();
          return;
        }
        const vr = await pollServerRestart(oldVersion, Date.now() + 120000);
        if (vr && vr.version) {
          toast("ok", `已升級到 v${vr.version}`);
        } else {
          toast("warn", "升級已觸發，但尚未確認新版本；請稍後重新整理");
        }
      } catch (e) {
        toast("err", "Server 升級失敗：" + (e.message || e));
      } finally {
        upgradeBtn.disabled = false;
        refreshServerVersion();
      }
    });
  }

  refreshServerVersion();
  loadAll();
})();
