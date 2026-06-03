/* =============================================================
   cockpit · 管理頁 (vanilla JS)
   機器：新增（Tailscale 式 token 安裝指令）/ 命名 / 移除
   軟體：新增 / 編輯（來源 + 更新策略）/ 移除
   所有變更透過 CockpitStore 持久化，並反映到清單/拓樸/機器頁。
   ============================================================= */
(() => {
  const { MACHINE_META, MACHINE_ORDER, SERVICES } = window.TOPO;
  const { INSTALLS } = window.MOCK;
  const S = window.CockpitStore;
  const $ = (s, r = document) => r.querySelector(s);

  /* ---- 主題 ---- */
  function initTheme() {
    const saved = localStorage.getItem("cockpit-theme");
    document.documentElement.classList.toggle("dark", saved ? saved === "dark" : true);
    syncIcon();
  }
  function syncIcon() {
    const dark = document.documentElement.classList.contains("dark");
    $("#icon-moon").style.display = dark ? "" : "none";
    $("#icon-sun").style.display = dark ? "none" : "";
  }
  $("#theme-btn").addEventListener("click", () => {
    const dark = document.documentElement.classList.toggle("dark");
    localStorage.setItem("cockpit-theme", dark ? "dark" : "light"); syncIcon();
  });

  const HSTATUS = {
    online:  ["s-online",  "b-ok",   "線上"],
    warn:    ["s-warn",    "b-warn", "注意"],
    offline: ["s-offline", "b-err",  "離線"],
    pending: ["s-pending", "b-info", "等待連線"],
  };
  const KINDS = [
    ["npm", "npm 套件"], ["pypi", "PyPI 套件"], ["github", "GitHub Release"],
    ["brew", "Homebrew"], ["claude-plugin", "Claude 外掛"], ["custom", "自訂指令"],
  ];
  const KIND_LABEL = Object.fromEntries(KINDS);
  function sourceText(w) {
    if (w.source) return w.source;
    switch (w.kind) {
      case "npm": return "npm: " + w.software;
      case "pypi": return "pip: " + w.software;
      case "github": return "gh: …/" + w.software;
      case "brew": return "brew: " + w.software;
      case "claude-plugin": return "plugin: " + w.software;
      default: return "custom script";
    }
  }
  const WSTATUS = { up_to_date: ["b-ok", "最新"], behind: ["b-warn", "落後 " ], unknown: ["b-mut", "未檢查"], error: ["b-err", "錯誤"] };

  /* ============================================================
     機器清單
     ============================================================ */
  function renderMachines() {
    const rows = MACHINE_ORDER.map((id) => {
      const m = MACHINE_META[id];
      const [dot, badge, label] = HSTATUS[m.status] || HSTATUS.offline;
      const svcCount = SERVICES.filter((s) => s.machine === id).length;
      const swCount = INSTALLS.filter((i) => i.machine === id).length;
      const meta = m.status === "pending"
        ? `<span class="tag">${m.os}</span><span>·</span><span>${m.last_seen}</span>`
        : `<span class="tag">${m.os}</span><span class="tag">${m.arch}</span><span>·</span><span>${svcCount} 服務 · ${swCount} 軟體</span><span>·</span><span>agent ${m.agent}</span>`;
      const installBtn = m.status === "pending"
        ? `<button class="btn btn-xs" data-install="${id}"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>安裝指令</button>`
        : "";
      return `<div class="mrow" data-mid="${id}">
        <span class="sdot ${dot} ${m.status!=="online"?"pulse":""}"></span>
        <div style="flex:1;min-width:0;">
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
            <input class="inline-name" value="${m.label.replace(/"/g,"&quot;")}" data-rename="${id}" aria-label="機器名稱" />
            <span class="tag">${id}</span>
            <span class="badge ${badge}">${label}</span>
          </div>
          <div style="display:flex;align-items:center;gap:7px;margin-top:5px;font-size:11px;color:var(--text-3);flex-wrap:wrap;">${meta}</div>
        </div>
        ${installBtn}
        <button class="btn btn-danger btn-xs" data-rmmachine="${id}" aria-label="移除">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
        </button>
      </div>`;
    }).join("");
    $("#machine-list").innerHTML = rows || `<div style="padding:30px;text-align:center;color:var(--text-3);font-size:13px;">尚無機器，點「新增機器」開始。</div>`;
  }

  /* ============================================================
     軟體清單（依機器分組）
     ============================================================ */
  function renderSoftware() {
    let html = "";
    MACHINE_ORDER.forEach((mid) => {
      const m = MACHINE_META[mid];
      const list = INSTALLS.filter((i) => i.machine === mid);
      if (!list.length) return;
      html += `<div class="grp-label"><span class="sdot ${(HSTATUS[m.status]||HSTATUS.offline)[0]}"></span>${m.label} <span class="tag">${mid}</span><span style="margin-left:auto;font-weight:400;text-transform:none;letter-spacing:0;">${list.length} 項</span></div>`;
      html += list.map((w) => {
        const strat = w.update_kind === "agent"
          ? `<span class="badge b-info">agent 更新</span>`
          : `<span class="tag">指令更新</span>`;
        const [scls, slbl] = WSTATUS[w.status] || WSTATUS.unknown;
        const statusBadge = `<span class="badge ${scls}">${slbl}${w.status==="behind"?w.behind_count:""}</span>`;
        const detail = w.update_kind === "agent" ? w.update_prompt : w.update_command;
        const preview = detail
          ? `<div style="margin-top:4px;font-size:11px;color:var(--text-3);font-family:'JetBrains Mono',monospace;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">${w.update_kind==="agent"?'<span style="color:var(--accent);">prompt:</span> ':'<span style="color:var(--text-3);">$</span> '}${detail.replace(/</g,"&lt;").replace(/\n/g," ")}</div>`
          : "";
        return `<div class="mrow" data-swid="${w.id}">
          <div style="flex:1;min-width:0;">
            <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
              <span style="font-size:13.5px;font-weight:600;">${w.software}</span>
              <span class="tag">${KIND_LABEL[w.kind]||w.kind}</span>${statusBadge}
            </div>
            <div style="display:flex;align-items:center;gap:8px;margin-top:5px;font-size:11px;color:var(--text-3);flex-wrap:wrap;">
              <span class="mono">${sourceText(w)}</span><span>·</span>${strat}
              ${w.current_version&&w.current_version!=="—"?`<span>·</span><span class="mono">${w.current_version}</span>`:""}
            </div>
            ${preview}
          </div>
          <button class="btn btn-xs" data-editsw="${w.id}">編輯</button>
          <button class="btn btn-danger btn-xs" data-rmsw="${w.id}" aria-label="移除">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
          </button>
        </div>`;
      }).join("");
    });
    $("#software-list").innerHTML = html || `<div style="padding:30px;text-align:center;color:var(--text-3);font-size:13px;">尚無追蹤的軟體。</div>`;
  }

  /* ============================================================
     Modal 基礎
     ============================================================ */
  function openModal(html) { $("#modal").innerHTML = html; $("#ov").classList.add("show"); }
  function closeModal() { $("#ov").classList.remove("show"); }
  $("#ov").addEventListener("click", (e) => { if (e.target.id === "ov") closeModal(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeModal(); });

  const modalHead = (title, sub) => `<div style="display:flex;align-items:flex-start;justify-content:space-between;gap:12px;padding:17px 18px;border-bottom:1px solid var(--border);">
    <div><div class="font-display" style="font-size:16px;font-weight:700;">${title}</div>${sub?`<div style="font-size:12px;color:var(--text-3);margin-top:3px;">${sub}</div>`:""}</div>
    <button class="btn btn-ghost btn-xs" data-close aria-label="關閉"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg></button></div>`;
  const fieldGroup = (label, inner) => `<div style="margin-bottom:14px;"><div style="font-size:12px;font-weight:550;color:var(--text-2);margin-bottom:6px;">${label}</div>${inner}</div>`;

  /* ============================================================
     新增機器（Tailscale 式 token 安裝）
     ============================================================ */
  function addMachineModal() {
    openModal(`${modalHead("新增機器", "命名後產生一段帶 token 的安裝指令，在目標機器執行即可。")}
      <div style="padding:18px;overflow-y:auto;" id="am-body">
        ${fieldGroup("顯示名稱", `<input id="am-name" class="field" placeholder="例：訓練節點 02" />`)}
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;">
          ${fieldGroup("作業系統", `<select id="am-os" class="field"><option>Ubuntu 24.04</option><option>Ubuntu 22.04</option><option>Debian 12</option><option>macOS 15</option><option>其他 Linux</option></select>`)}
          ${fieldGroup("架構", `<select id="am-arch" class="field"><option value="amd64">amd64 (x86_64)</option><option value="arm64">arm64</option></select>`)}
        </div>
        ${fieldGroup("Token 類型", `<select id="am-tok" class="field"><option value="reusable">可重複使用（多台共用）</option><option value="oneoff">一次性（裝完即失效）</option><option value="90d">90 天後過期</option></select>`)}
      </div>
      <div style="display:flex;gap:9px;justify-content:flex-end;padding:14px 18px;border-top:1px solid var(--border);">
        <button class="btn" data-close>取消</button>
        <button class="btn btn-primary" id="am-gen"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2v6h-6M3 12a9 9 0 0 1 15-6.7L21 8M3 22v-6h6M21 12a9 9 0 0 1-15 6.7L3 16"/></svg>產生安裝指令</button>
      </div>`);
    $("#am-gen").addEventListener("click", () => {
      const name = ($("#am-name").value || "").trim();
      if (!name) { $("#am-name").focus(); $("#am-name").style.borderColor = "var(--err)"; return; }
      const rec = S.addMachine({ label: name, os: $("#am-os").value, arch: $("#am-arch").value });
      renderMachines(); renderSoftware();
      showInstall(rec.id);   // 進到指令畫面
    });
  }

  function installCmd(m) {
    return `curl -fsSL https://hub.cockpit.local/install.sh | sh -s -- \\
  --hub  https://hub.cockpit.local \\
  --token ${m.token} \\
  --name "${m.label}"`;
  }
  function showInstall(id) {
    const m = MACHINE_META[id];
    openModal(`${modalHead(`安裝 cockpit-agent`, `在「${m.label}」上以 root 執行下列指令。`)}
      <div style="padding:18px;overflow-y:auto;">
        <div class="term" id="cmd"><button class="btn btn-xs copy-btn" id="copy-cmd"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>複製</button>${installCmd(m)}</div>
        <div style="display:flex;align-items:center;gap:9px;margin-top:14px;padding:11px 13px;background:var(--info-bg);border:1px solid var(--info-bd);border-radius:10px;font-size:12.5px;color:var(--text-2);">
          <span class="sdot s-pending pulse"></span>
          <span>等待 <b style="color:var(--text);">${m.label}</b> 的 agent 回報… 連線後狀態會自動轉為線上，並開始顯示指標。</span>
        </div>
        <div style="font-size:11.5px;color:var(--text-3);margin-top:12px;line-height:1.6;">
          token <span class="mono" style="color:var(--text-2);">${m.token.slice(0,18)}…</span> · 也支援 Docker：<span class="mono">docker run … -e COCKPIT_TOKEN=…</span>
        </div>
      </div>
      <div style="display:flex;gap:9px;justify-content:flex-end;padding:14px 18px;border-top:1px solid var(--border);">
        <button class="btn btn-primary" data-close>完成</button>
      </div>`);
    $("#copy-cmd").addEventListener("click", () => copy(installCmd(m), $("#copy-cmd")));
  }

  function copy(text, btn) {
    const done = () => { if (btn) { const o = btn.innerHTML; btn.innerHTML = "已複製 ✓"; setTimeout(() => (btn.innerHTML = o), 1400); } toast("ok", "已複製到剪貼簿"); };
    if (navigator.clipboard?.writeText) navigator.clipboard.writeText(text).then(done, () => fallbackCopy(text, done));
    else fallbackCopy(text, done);
  }
  function fallbackCopy(text, done) {
    const ta = document.createElement("textarea"); ta.value = text; ta.style.position = "fixed"; ta.style.opacity = "0";
    document.body.appendChild(ta); ta.select(); try { document.execCommand("copy"); } catch (e) {} ta.remove(); done && done();
  }

  /* ============================================================
     新增 / 編輯軟體
     ============================================================ */
  function softwareModal(existing) {
    const w = existing || {};
    const machineOpts = MACHINE_ORDER.map((id) => `<option value="${id}" ${w.machine===id?"selected":""}>${MACHINE_META[id].label}（${id}）</option>`).join("");
    const kindOpts = KINDS.map(([v, l]) => `<option value="${v}" ${w.kind===v?"selected":""}>${l}</option>`).join("");
    const strat = w.update_kind === "agent" ? "agent" : "command";
    openModal(`${modalHead(existing ? "編輯軟體" : "新增軟體", "設定追蹤來源與更新方式。")}
      <div style="padding:18px;overflow-y:auto;">
        ${fieldGroup("名稱", `<input id="sw-name" class="field" placeholder="例：claude-code" value="${(w.software||"").replace(/"/g,"&quot;")}" />`)}
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;">
          ${fieldGroup("機器", `<select id="sw-machine" class="field">${machineOpts}</select>`)}
          ${fieldGroup("類型 / 來源", `<select id="sw-kind" class="field">${kindOpts}</select>`)}
        </div>
        ${fieldGroup("來源識別", `<input id="sw-source" class="field mono" placeholder="例：@anthropic-ai/claude-code 或 owner/repo" value="${(w.source||"").replace(/"/g,"&quot;")}" />`)}
        ${fieldGroup("更新策略", `<div class="seg" id="sw-strat">
          <button data-strat="command" class="${strat==="command"?"active":""}">指令更新</button>
          <button data-strat="agent" class="${strat==="agent"?"active":""}">委派 agent</button>
        </div><div style="font-size:11px;color:var(--text-3);margin:6px 0 0;" id="sw-strat-hint"></div>
        <div id="sw-strat-field" style="margin-top:10px;"></div>`)}
      </div>
      <div style="display:flex;gap:9px;justify-content:flex-end;padding:14px 18px;border-top:1px solid var(--border);">
        <button class="btn" data-close>取消</button>
        <button class="btn btn-primary" id="sw-save">${existing?"儲存":"新增"}</button>
      </div>`);
    let curStrat = strat;
    let cmdVal = w.update_command || "";
    let promptVal = w.update_prompt || "";
    const hint = $("#sw-strat-hint");
    const setHint = () => hint.textContent = curStrat === "agent"
      ? "委派 AI agent 多步執行（重 build、重啟、健康檢查、失敗 rollback）。"
      : "更新時執行你指定的指令。";
    function renderStratField() {
      const box = $("#sw-strat-field");
      if (curStrat === "agent") {
        box.innerHTML = `<div style="font-size:11.5px;font-weight:550;color:var(--text-2);margin-bottom:5px;">Agent 任務 prompt</div>
          <textarea id="sw-prompt" class="field mono" rows="4" style="resize:vertical;line-height:1.55;" placeholder="例：上游有新版時，git pull、依新版 Dockerfile 重 build 鏡像、以 compose 滾動重啟，healthcheck 通過才算成功，失敗則 rollback 並回報原因。">${(promptVal||"").replace(/</g,"&lt;")}</textarea>`;
        $("#sw-prompt").addEventListener("input", (e) => (promptVal = e.target.value));
      } else {
        box.innerHTML = `<div style="font-size:11.5px;font-weight:550;color:var(--text-2);margin-bottom:5px;">更新指令</div>
          <textarea id="sw-cmd" class="field mono" rows="2" style="resize:vertical;line-height:1.55;" placeholder="例：npm i -g @anthropic-ai/claude-code@latest">${(cmdVal||"").replace(/</g,"&lt;")}</textarea>`;
        $("#sw-cmd").addEventListener("input", (e) => (cmdVal = e.target.value));
      }
    }
    setHint(); renderStratField();
    $("#sw-strat").addEventListener("click", (e) => {
      const b = e.target.closest("[data-strat]"); if (!b) return;
      curStrat = b.getAttribute("data-strat");
      $("#sw-strat").querySelectorAll("button").forEach((x) => x.classList.toggle("active", x === b));
      setHint(); renderStratField();
    });
    $("#sw-save").addEventListener("click", () => {
      const name = ($("#sw-name").value || "").trim();
      if (!name) { $("#sw-name").focus(); $("#sw-name").style.borderColor = "var(--err)"; return; }
      // 直接讀取目前顯示的輸入框（不依賴 input 事件）
      if (curStrat === "command" && $("#sw-cmd")) cmdVal = $("#sw-cmd").value;
      if (curStrat === "agent" && $("#sw-prompt")) promptVal = $("#sw-prompt").value;
      const patch = {
        software: name, machine: $("#sw-machine").value, kind: $("#sw-kind").value,
        source: ($("#sw-source").value || "").trim(),
        update_kind: curStrat === "agent" ? "agent" : undefined,
        update_command: curStrat === "command" ? cmdVal.trim() : undefined,
        update_prompt: curStrat === "agent" ? promptVal.trim() : undefined,
      };
      if (existing) { S.editSoftware(existing.id, patch); toast("ok", `已更新 ${name}`); }
      else { S.addSoftware(patch); toast("ok", `已新增 ${name}`); }
      renderSoftware(); closeModal();
    });
  }

  /* ============================================================
     確認移除
     ============================================================ */
  function confirmModal(title, body, onYes) {
    openModal(`${modalHead(title)}
      <div style="padding:18px;font-size:13.5px;color:var(--text-2);line-height:1.6;">${body}</div>
      <div style="display:flex;gap:9px;justify-content:flex-end;padding:14px 18px;border-top:1px solid var(--border);">
        <button class="btn" data-close>取消</button>
        <button class="btn" id="cf-yes" style="background:var(--err);border-color:var(--err);color:#fff;">移除</button>
      </div>`);
    $("#cf-yes").addEventListener("click", () => { onYes(); closeModal(); });
  }

  /* ============================================================
     toast
     ============================================================ */
  let tt;
  function toast(kind, msg) {
    $("#toast-msg").textContent = msg;
    $("#toast-icon").innerHTML = kind === "ok"
      ? `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--ok)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`
      : `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--err)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v5M12 16h.01"/></svg>`;
    $("#toast").style.display = "block";
    clearTimeout(tt); tt = setTimeout(() => ($("#toast").style.display = "none"), 2400);
  }

  /* ============================================================
     事件
     ============================================================ */
  $("#add-machine").addEventListener("click", addMachineModal);
  $("#add-software").addEventListener("click", () => softwareModal(null));

  // modal 關閉委派
  $("#modal").addEventListener("click", (e) => { if (e.target.closest("[data-close]")) closeModal(); });

  // 機器清單
  $("#machine-list").addEventListener("click", (e) => {
    const inst = e.target.closest("[data-install]"); if (inst) return showInstall(inst.getAttribute("data-install"));
    const rm = e.target.closest("[data-rmmachine]");
    if (rm) {
      const id = rm.getAttribute("data-rmmachine"); const m = MACHINE_META[id];
      return confirmModal("移除機器", `確定移除 <b style="color:var(--text);">${m.label}</b>（${id}）？其服務與追蹤的軟體也會一併移除，且會反映到其他頁面。`, () => {
        S.removeMachine(id); renderMachines(); renderSoftware(); toast("ok", `已移除 ${m.label}`);
      });
    }
  });
  $("#machine-list").addEventListener("change", (e) => {
    const r = e.target.closest("[data-rename]"); if (!r) return;
    const id = r.getAttribute("data-rename"); const v = r.value.trim();
    if (v && v !== MACHINE_META[id].label) { S.renameMachine(id, v); toast("ok", "已重新命名"); renderSoftware(); }
    else r.value = MACHINE_META[id].label;
  });
  $("#machine-list").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.target.closest("[data-rename]")) e.target.blur();
  });

  // 軟體清單
  $("#software-list").addEventListener("click", (e) => {
    const ed = e.target.closest("[data-editsw]");
    if (ed) { const w = INSTALLS.find((i) => i.id === ed.getAttribute("data-editsw")); return softwareModal(w); }
    const rm = e.target.closest("[data-rmsw]");
    if (rm) {
      const w = INSTALLS.find((i) => i.id === rm.getAttribute("data-rmsw"));
      return confirmModal("移除軟體", `不再追蹤 <b style="color:var(--text);">${w.software}</b>（${w.machine}）？`, () => {
        S.removeSoftware(w.id); renderSoftware(); toast("ok", `已移除 ${w.software}`);
      });
    }
  });

  /* ---- 啟動 ---- */
  initTheme();
  renderMachines();
  renderSoftware();
})();
