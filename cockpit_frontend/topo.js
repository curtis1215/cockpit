/* =============================================================
   cockpit · 拓樸頁 — 互動邏輯 (vanilla JS)
   三層：機器 → 服務 → 軟體；SVG 貝茲連線；滑過高亮關聯、點選看詳情。
   軟體層重用 mock-data.js 的 INSTALLS / VERSIONS。
   [API] 標記處延續 api-contract.md，接後端時替換。
   ============================================================= */
(() => {
  const { INSTALLS, VERSIONS, JOB_SCRIPTS } = window.MOCK;
  const { MACHINE_META, SERVICES, MACHINE_ORDER } = window.TOPO;
  const $ = (s, r = document) => r.querySelector(s);
  const SVGNS = "http://www.w3.org/2000/svg";

  /* ---- VM 分群輔助 ----
   * buildVmGroups()：每次 render 時重新計算，支援 _reloadTopoData 刷新。
   * 回傳 { vmsByHost, orphanVMs, vmIds }
   *   vmsByHost[hostId] = { linked: [id,...], unlinked: [id,...] }
   *   所有 kind==="vm" 且 host_id 在 MACHINE_META 的，歸到對應 host。
   *   host 不在清單裡的 VM → orphanVMs（正常渲染，不巢狀）。
   */
  function buildVmGroups() {
    const vmsByHost = {};
    const orphanVMs = [];
    const vmIds = new Set();
    MACHINE_ORDER.forEach((id) => {
      const m = MACHINE_META[id];
      if (!m || m.kind !== "vm") return;
      vmIds.add(id);
      const hid = m.host_id;
      if (hid && MACHINE_META[hid]) {
        if (!vmsByHost[hid]) vmsByHost[hid] = { linked: [], unlinked: [] };
        if (m.status === "pending") {
          vmsByHost[hid].unlinked.push(id);
        } else {
          vmsByHost[hid].linked.push(id);
        }
      } else {
        orphanVMs.push(id);
      }
    });
    return { vmsByHost, orphanVMs, vmIds };
  }

  /* 快取（每次 renderAll 更新）*/
  let vmsByHost = {}, orphanVMs = [], vmIds = new Set();

  /* 渲染時用的機器排序：host + 其 VMs，再非 VM 主機，再孤兒 VM */
  function buildRenderOrder() {
    ({ vmsByHost, orphanVMs, vmIds } = buildVmGroups());
    const order = [];
    const nonVmHosts = MACHINE_ORDER.filter((id) => !vmIds.has(id));
    nonVmHosts.forEach((id) => {
      order.push({ id, role: "host" });
      const grp = vmsByHost[id];
      if (grp) {
        grp.linked.forEach((vid) => order.push({ id: vid, role: "vm-linked" }));
        grp.unlinked.forEach((vid) => order.push({ id: vid, role: "vm-unlinked" }));
      }
    });
    orphanVMs.forEach((id) => order.push({ id, role: "orphan-vm" }));
    return order;
  }

  /** 給定一個 machineId，判斷它是否因為 host 收起而應隱藏 */
  function isHiddenViaHost(machineId) {
    const m = MACHINE_META[machineId];
    if (!m || m.kind !== "vm") return false;
    const hid = m.host_id;
    return !!(hid && collapsedMachines.has(hid));
  }

  const installById = Object.fromEntries(INSTALLS.map((i) => [i.id, i]));

  /* ---- 排序：服務依機器順序，軟體依服務順序（降低連線交叉）---- */
  const services = [...SERVICES].sort(
    (a, b) => MACHINE_ORDER.indexOf(a.machine) - MACHINE_ORDER.indexOf(b.machine)
  );
  const softwareOrder = [];
  services.forEach((s) => s.software.forEach((id) => { if (!softwareOrder.includes(id)) softwareOrder.push(id); }));
  INSTALLS.forEach((i) => { if (!softwareOrder.includes(i.id)) softwareOrder.push(i.id); });
  const software = softwareOrder.map((id) => installById[id]).filter(Boolean);
  // 機器分群排序：軟體依機器順序重排，讓同機器的節點成帶狀
  const softwareGrouped = [...software].sort(
    (a, b) => MACHINE_ORDER.indexOf(a.machine) - MACHINE_ORDER.indexOf(b.machine)
  );
  const currentSoftware = () => (tweaks.grouping === "grouped" ? softwareGrouped : software);

  /* ---- Tweaks（佈局探索）。host 會就地改寫 EDITMODE 區塊以持久化 ---- */
  const TWEAK_DEFAULTS = /*EDITMODE-BEGIN*/{
    "connector": "bezier",
    "grouping": "flow",
    "colGap": 76,
    "showDepEdges": true,
    "healthColors": true,
    "density": "regular"
  }/*EDITMODE-END*/;
  const tweaks = { ...TWEAK_DEFAULTS };

  /* ---- 邊：machine→service / service→software / service→service(dep) ---- */
  const edges = [];
  services.forEach((s) => {
    edges.push({ from: "m:" + s.machine, to: "s:" + s.id, kind: "run" });
    s.software.forEach((wid) => edges.push({ from: "s:" + s.id, to: "w:" + wid, kind: "run" }));
    (s.depends || []).forEach((dep) => edges.push({ from: "s:" + s.id, to: "s:" + dep, kind: "dep" }));
  });

  /* ---- 鄰接表（雙向）供高亮走訪 ---- */
  const adj = {};
  const link = (a, b) => { (adj[a] ||= new Set()).add(b); (adj[b] ||= new Set()).add(a); };
  edges.forEach((e) => link(e.from, e.to));

  /* ---- 健康判定（全部對缺資料防衛：查無 → 中性 "online"，不可 throw）---- */
  const machineHealth = (id) => (MACHINE_META[id] ? MACHINE_META[id].status : "online"); // online|warn|offline|pending
  function serviceHealth(s) {
    if (!s) return "online"; // 查無服務 → 中性
    if (machineHealth(s.machine) === "offline" || s.status === "stopped") return "offline";
    if (s.status === "restarting") return "warn";
    if ((s.software || []).some((w) => installById[w]?.status === "error")) return "offline";
    if ((s.software || []).some((w) => installById[w]?.status === "behind")) return "warn";
    return "online";
  }
  function softwareHealth(w) {
    if (!w) return "online"; // install 查無（service.software[] 引用了不存在的 id）→ 中性，不可讀 undefined.status
    if (w.status === "error") return "offline";
    if (w.status === "behind" || w.status === "unknown") return "warn";
    return "online";
  }
  const HCLS = { online: "s-online", warn: "s-warn", offline: "s-offline", pending: "s-pending" };
  const barColor = (v) => (v >= 85 ? "var(--err)" : v >= 60 ? "var(--warn)" : "var(--ok)");

  /* ============================================================
     主題
     ============================================================ */
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
    localStorage.setItem("cockpit-theme", dark ? "dark" : "light");
    syncIcon(); drawEdges();
  });

  /* ============================================================
     摺疊狀態（localStorage 持久化）
     ============================================================ */
  const COLLAPSED_KEY = "cockpit-topo-collapsed";
  const collapsedMachines = new Set(
    JSON.parse(localStorage.getItem(COLLAPSED_KEY) || "[]")
  );

  function saveCollapsed() {
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...collapsedMachines]));
  }

  /** 某個軟體節點是否應該隱藏：
   *  所有連到它的服務，所屬機器全部都已收起（含 host 收起導致 VM 隱藏），才隱藏。
   *  若有任何一台展開的機器的服務連到它，就保持可見。
   */
  function softwareShouldHide(installId) {
    const ownerSvcs = services.filter((s) => s.software.includes(installId));
    if (ownerSvcs.length === 0) return false; // 無連線軟體不隱藏
    return ownerSvcs.every((s) =>
      collapsedMachines.has(s.machine) || isHiddenViaHost(s.machine)
    );
  }

  /** 套用摺疊可見性（renderAll 後呼叫）*/
  function applyCollapse() {
    // 服務節點：machine 自身收起 或 所屬 VM 被 host 收起
    document.querySelectorAll(".s-node").forEach((el) => {
      const machine = el.dataset.machine;
      el.style.display = (collapsedMachines.has(machine) || isHiddenViaHost(machine)) ? "none" : "";
    });
    // 軟體節點
    document.querySelectorAll(".w-node").forEach((el) => {
      const key = el.dataset.node; // "w:installId"
      const installId = key.slice(2);
      el.style.display = softwareShouldHide(installId) ? "none" : "";
    });
    // VM 群組包裹容器 + 其中的 m-node：host 收起時整組隱藏
    document.querySelectorAll(".vm-group").forEach((el) => {
      const hostId = el.dataset.hostId;
      const hide = !!(hostId && collapsedMachines.has(hostId));
      el.style.display = hide ? "none" : "";
      // 同時對每張 VM m-node 設 display，讓 drawEdges 的 display:none 判斷正常運作
      el.querySelectorAll(".m-node").forEach((mn) => {
        mn.style.display = hide ? "none" : "";
      });
    });
    // 更新每張機器卡的收起摘要行（僅 host 卡，非 VM）
    document.querySelectorAll(".m-node").forEach((el) => {
      const id = el.dataset.machine;
      const summary = el.querySelector(".m-collapse-summary");
      const collapsed = collapsedMachines.has(id);
      if (summary) {
        summary.style.display = collapsed ? "" : "none";
      }
      // 切換按鈕圖示
      const btn = el.querySelector(".m-collapse-btn");
      if (btn) {
        btn.textContent = collapsed ? "＋" : "−";
        btn.title = collapsed ? "展開" : "收起";
      }
    });
  }

  /** 切換收起/展開，並重繪邊 */
  function toggleCollapse(machineId) {
    if (collapsedMachines.has(machineId)) {
      collapsedMachines.delete(machineId);
    } else {
      collapsedMachines.add(machineId);
    }
    saveCollapsed();
    applyCollapse();
    drawEdges();
  }

  /* ============================================================
     render 節點
     ============================================================ */
  /* ============================================================
     VM 手動連結 Dropdown Overlay
     ============================================================ */
  let linkOverlay = null;
  function closeLinkOverlay() {
    if (linkOverlay) { linkOverlay.remove(); linkOverlay = null; }
  }
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeLinkOverlay(); });

  function openLinkOverlay(anchorEl, pendingId) {
    closeLinkOverlay();
    const m = MACHINE_META[pendingId];
    if (!m || !m._vmRaw) return;
    const { host_system_id, uuid } = m._vmRaw;

    // Build list of physical systems not yet linked to a VM
    const allSystems = window._allSystems || [];
    const linkedIDs = window._linkedSystemIDs || new Set();
    // 排除：已被連結者、宿主機本身（會自我循環）、任何 VM 宿主（不應被標成 VM）
    const hostIDs = new Set(Object.values(MACHINE_META).filter((x) => x && x.kind === "vm" && x.host_id).map((x) => x.host_id));
    if (host_system_id) hostIDs.add(host_system_id);
    const candidates = allSystems.filter(
      (s) => s.kind === "physical" && !linkedIDs.has(s.id) && !hostIDs.has(s.id)
    );

    const overlay = document.createElement("div");
    overlay.style.cssText = `position:fixed; inset:0; z-index:9998;`;
    overlay.addEventListener("click", closeLinkOverlay);

    const rect = anchorEl.getBoundingClientRect();
    const menu = document.createElement("div");
    menu.style.cssText = `position:fixed; z-index:9999; left:${rect.left}px; top:${rect.bottom + 6}px;
      min-width:220px; max-width:300px; background:var(--surface); border:1px solid var(--border-2);
      border-radius:10px; box-shadow:0 8px 32px rgba(0,0,0,.28); padding:6px 0; font-size:13px;`;
    menu.addEventListener("click", (e) => e.stopPropagation());

    if (candidates.length === 0) {
      menu.innerHTML = `<div style="padding:10px 14px; color:var(--text-3); font-size:12px;">無可連結的 physical system</div>`;
    } else {
      menu.innerHTML = `<div style="padding:6px 14px 4px; font-size:11px; color:var(--text-3); text-transform:uppercase; letter-spacing:.06em;">此 VM 內運行的機器是…</div>` +
        candidates.map((s) =>
          `<button data-link-sys="${s.id}" style="display:block; width:100%; text-align:left; padding:8px 14px; border:none; background:none; color:var(--text); cursor:pointer; transition:.1s;"
            onmouseover="this.style.background='var(--surface-2)'" onmouseout="this.style.background='none'">
            ${s.label} <span style="color:var(--text-3); font-size:11px;">${s.id}</span>
          </button>`
        ).join("");
    }

    menu.addEventListener("click", async (e) => {
      const btn = e.target.closest("[data-link-sys]");
      if (!btn) return;
      const systemId = btn.dataset.linkSys;
      try {
        const res = await fetch(`/api/vms/${host_system_id}/${uuid}/link`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ system_id: systemId }),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
      } catch (err) {
        alert("連結失敗：" + err.message);
      }
      closeLinkOverlay();
      // Reload data to reflect the link.
      window.dispatchEvent(new Event("topo:refresh-data"));
    });

    overlay.appendChild(menu);
    document.body.appendChild(overlay);
    linkOverlay = overlay;
  }

  // Listen for the data-refresh trigger (fired after manual link/unlink).
  window.addEventListener("topo:refresh-data", async () => {
    // Re-run loadAll (api-data.js exports loadAll into window if needed, else reload page).
    if (window._reloadTopoData) {
      await window._reloadTopoData();
      window.dispatchEvent(new Event("topo:refresh"));
    } else {
      location.reload();
    }
  });

  function machineNode(id) {
    const m = MACHINE_META[id];
    const h = machineHealth(id);
    const svcCount = services.filter((s) => s.machine === id).length;
    // 連到此機器服務的軟體數（去重）
    const swIds = new Set(
      services.filter((s) => s.machine === id).flatMap((s) => s.software)
    );
    const swCount = swIds.size;
    const collapsed = collapsedMachines.has(id);
    const offline = h === "offline" || h === "pending";
    const lcol = h === "offline" ? "err" : h === "warn" ? "warn" : h === "pending" ? "accent" : "ok";
    const metrics = offline ? "" : ["CPU", "MEM", "DISK"].map((lbl, i) => {
      const v = [m.cpu, m.mem, m.disk][i];
      return `<div class="metric-row"><span class="lbl">${lbl}</span>
        <span class="bar"><i style="width:${v}%; background:${barColor(v)};"></i></span>
        <span class="val">${v}%</span></div>`;
    }).join("");
    const gpu = (!offline && m.gpu != null)
      ? `<div class="metric-row"><span class="lbl">GPU</span><span class="bar"><i style="width:${m.gpu}%; background:${barColor(m.gpu)};"></i></span><span class="val">${m.gpu}%</span></div>` : "";
    const agentBadge = m.agent_status === "behind"
      ? `<span class="badge b-warn">agent ${m.agent}↑</span>`
      : m.agent_status === "stale"
      ? `<span class="badge b-err">agent 失聯</span>`
      : m.agent_status === "pending"
      ? `<span class="badge b-mut">未連線</span>`
      : `<span class="tag">agent ${m.agent}</span>`;
    // 未連線 VM pending 卡：加連結按鈕
    const linkBtn = (h === "pending" && m._vmRaw)
      ? `<button class="vm-link-btn" data-link-vm="${id}"
           style="font-size:11px; padding:2px 8px; border-radius:6px; border:1px solid var(--accent); background:transparent; color:var(--accent); cursor:pointer; flex:none; transition:.14s;"
           >連結</button>`
      : "";
    // VM tag（此機器是 VM）
    const vmTag = (m.kind === "vm")
      ? `<span class="vm-tag">VM</span>` : "";
    // host 的 VM 數量（用於 footer chip 及收起摘要）
    const hostVmGrp = vmsByHost[id];
    const vmCount = hostVmGrp ? (hostVmGrp.linked.length + hostVmGrp.unlinked.length) : 0;
    const vmChipStr = vmCount > 0 ? `<span>${vmCount} VM</span><span>·</span>` : "";
    const foot = h === "pending"
      ? `<div class="m-foot"><span class="badge b-mut">等待連線</span><span>尚未回報</span>${agentBadge}${linkBtn}</div>`
      : offline
      ? `<div class="m-foot"><span class="badge b-err">離線</span><span>最後回報 ${m.last_seen}</span>${agentBadge}</div>`
      : `<div class="m-foot">${agentBadge}<span>${m.uptime}</span><span>·</span>${vmChipStr}<span>${svcCount} 服務</span>${m.temp!=null?`<span>·</span><span>${m.temp}°C</span>`:""}</div>`;
    // 收起摘要行：含 VM 數（若有）
    const vmSummaryPart = vmCount > 0 ? ` · ${vmCount} VM（已收起）` : "（已收起）";
    const summaryLine = `<div class="m-collapse-summary" style="display:${collapsed ? "" : "none"}; margin-top:7px; padding-top:7px; border-top:1px solid var(--border); font-size:11px; color:var(--text-3);">${svcCount} 服務 · ${swCount} 軟體${vmSummaryPart}</div>`;
    // 是否顯示收起按鈕：VM 卡自身也有收起按鈕，但不影響 host 的 vm 群組
    const collapseBtn = `<button class="m-collapse-btn" data-collapse-machine="${id}" title="${collapsed ? "展開" : "收起"}"
            style="font-size:12px; font-weight:700; line-height:1; padding:1px 5px; border-radius:5px; border:1px solid var(--border-2); background:var(--surface-2); color:var(--text-3); cursor:pointer; transition:.14s; flex:none;"
            >${collapsed ? "＋" : "−"}</button>`;
    return `<div class="node m-node" data-node="m:${id}" data-machine="${id}" data-health="${h}">
      <div class="node-l" style="background:var(--${lcol});"></div>
      <div class="m-top">
        <div style="min-width:0;">
          <div style="display:flex; align-items:center; gap:7px;"><span class="sdot ${HCLS[h]} ${h!=="online"?"pulse":""}"></span><span class="m-name">${m.label}</span>${vmTag}</div>
          <div class="m-role">${id} · ${m.role}</div>
        </div>
        <div style="display:flex; align-items:flex-start; gap:6px; flex:none;">
          <span class="tag" style="margin-top:2px;">${m.arch}</span>
          ${collapseBtn}
        </div>
      </div>
      ${metrics}${gpu}${foot}${summaryLine}
    </div>`;
  }

  const KIND_ICON = {
    docker:  `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="9" width="18" height="9" rx="1.5"/><rect x="6" y="6" width="3" height="3"/><rect x="10" y="6" width="3" height="3"/></svg>`,
    proxy:   `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 12h16M4 12l4-4M4 12l4 4M20 12l-4-4M20 12l-4 4"/></svg>`,
    db:      `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.66 3.58 3 8 3s8-1.34 8-3V5"/><path d="M4 12c0 1.66 3.58 3 8 3s8-1.34 8-3"/></svg>`,
    daemon:  `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3"/></svg>`,
    service: `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 2v20M2 12h20"/><circle cx="12" cy="12" r="4"/></svg>`,
    plugin:  `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M10 3v4M14 3v4M6 7h12v5a6 6 0 0 1-12 0Z"/><path d="M12 17v4"/></svg>`,
    runtime: `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><polygon points="5 3 19 12 5 21 5 3"/></svg>`,
    bundle:  `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><path d="m3.3 7 8.7 5 8.7-5M12 22V12"/></svg>`,
  };

  function serviceNode(s) {
    const h = serviceHealth(s);
    const stat = s.status === "running" ? `<span class="badge b-ok" style="padding:.06rem .34rem;">running</span>`
      : s.status === "restarting" ? `<span class="badge b-warn" style="padding:.06rem .34rem;">restart</span>`
      : `<span class="badge b-err" style="padding:.06rem .34rem;">stopped</span>`;
    const res = (s.cpu != null)
      ? `<span class="mono">${s.cpu}%</span><span style="opacity:.5">cpu</span><span class="mono">${s.mem}%</span><span style="opacity:.5">mem</span>`
      : `<span style="opacity:.6">系統層</span>`;
    return `<div class="node s-node" data-node="s:${s.id}" data-machine="${s.machine}" data-health="${h}">
      <div class="node-l" style="background:var(--${h==="offline"?"err":h==="warn"?"warn":"ok"});"></div>
      <div class="s-top">
        <span style="color:var(--text-3); display:flex;">${KIND_ICON[s.kind] || KIND_ICON.service}</span>
        <span class="s-name">${s.name}</span>${stat}
      </div>
      <div class="s-meta"><span class="tag">${s.kind}</span>${s.port ? `<span class="mono">:${s.port}</span>` : ""}<span style="flex:1"></span>${res}</div>
    </div>`;
  }

  const WSTATUS = {
    up_to_date: ["b-ok", "最新"], behind: ["b-warn", null],
    unknown: ["b-mut", "未知"], error: ["b-err", "錯誤"],
  };
  function softwareNode(w) {
    const h = softwareHealth(w);
    const [cls, lbl] = WSTATUS[w.status] || WSTATUS.unknown; // 未知 status 不可炸渲染
    const badge = w.status === "behind"
      ? `<span class="badge b-warn">落後 ${w.behind_count}</span>`
      : `<span class="badge ${cls}">${lbl}</span>`;
    return `<div class="node w-node" data-node="w:${w.id}" data-machine="${w.machine}" data-health="${h}">
      <div class="node-l" style="background:var(--${h==="offline"?"err":h==="warn"?"warn":"ok"});"></div>
      <div class="w-top"><span class="w-name">${w.software}</span>${badge}</div>
      <div class="w-meta"><span class="tag">${w.kind}</span><span class="w-ver">${w.current_version}${w.status==="behind"?` → ${w.latest_version}`:""}</span>
      ${w.update_kind==="agent"?`<span class="tag" style="color:var(--accent);border-color:var(--accent);">agent</span>`:""}</div>
    </div>`;
  }

  function renderAll() {
    // 機器欄：按 host+VM 群組渲染
    const renderOrder = buildRenderOrder();
    const machineColParts = [];
    let i = 0;
    while (i < renderOrder.length) {
      const entry = renderOrder[i];
      if (entry.role === "host") {
        // 渲染 host 卡
        const hostHtml = machineNode(entry.id);
        // 找出連續的 vm-linked / vm-unlinked 項目（它們是這台 host 的 VM）
        const vmParts = [];
        let j = i + 1;
        while (j < renderOrder.length && (renderOrder[j].role === "vm-linked" || renderOrder[j].role === "vm-unlinked")) {
          const vid = renderOrder[j].id;
          const wrapCls = renderOrder[j].role === "vm-unlinked" ? "vm-card-wrap vm-pending" : "vm-card-wrap";
          vmParts.push(`<div class="${wrapCls}">${machineNode(vid)}</div>`);
          j++;
        }
        if (vmParts.length > 0) {
          // host + vm-group 包在一個外層 div 以便 col-stack gap 計算正常
          machineColParts.push(
            `<div class="host-vm-block">${hostHtml}<div class="vm-group" data-host-id="${entry.id}">${vmParts.join("")}</div></div>`
          );
        } else {
          machineColParts.push(`<div class="host-vm-block">${hostHtml}</div>`);
        }
        i = j;
      } else {
        // orphan-vm：正常渲染，不巢狀
        machineColParts.push(`<div class="host-vm-block">${machineNode(entry.id)}</div>`);
        i++;
      }
    }
    $("#col-machines").innerHTML = machineColParts.join("");
    $("#col-services").innerHTML = services.map(serviceNode).join("");
    $("#col-software").innerHTML = currentSoftware().map(softwareNode).join("");
    $("#c-machine").textContent = MACHINE_ORDER.length;
    $("#c-service").textContent = services.length;
    $("#c-software").textContent = software.length;
    applyGrouping();
    applyCollapse();
  }

  // 機器分群：同機器節點間留白成帶狀（#col-machines 由 host-vm-block 結構自然分群，不需額外間距）
  function applyGrouping() {
    const on = tweaks.grouping === "grouped";
    ["#col-services", "#col-software"].forEach((sel) => {
      let prev = null;
      [...$(sel).children].forEach((n, i) => {
        const m = n.dataset.machine;
        n.style.marginTop = on && i > 0 && m !== prev ? "28px" : "";
        prev = m;
      });
    });
    // host-vm-block 間的間距
    [...$("#col-machines").children].forEach((n, i) => {
      n.style.marginTop = on && i > 0 ? "28px" : "";
    });
  }

  /* ============================================================
     SVG 連線（content 座標，隨捲動一起移動，免重算）
     ============================================================ */
  const topo = $("#topo"), svg = $("#edges");
  const nodeEl = (key) => topo.querySelector(`[data-node="${CSS.escape(key)}"]`);

  function drawEdges() {
    const base = topo.getBoundingClientRect();
    svg.setAttribute("width", topo.scrollWidth);
    svg.setAttribute("height", topo.scrollHeight);
    svg.innerHTML = "";
    edges.forEach((e, idx) => {
      // 單一邊失敗不可炸全圖：包 try/catch
      try {
        if (e.kind === "dep" && !tweaks.showDepEdges) return;
        const a = nodeEl(e.from), b = nodeEl(e.to);
        if (!a || !b) return;
        if (a.style.display === "none" || b.style.display === "none") return;
        const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
        let sx, tx;
        if (e.kind === "dep") { sx = ra.left - base.left; tx = rb.left - base.left; }
        else { sx = ra.right - base.left; tx = rb.left - base.left; }
        const sy = ra.top - base.top + ra.height / 2;
        const ty = rb.top - base.top + rb.height / 2;
        addPath(connectorPath(sx, sy, tx, ty, e.kind), e.kind === "dep" ? "edge dep" : "edge", e, idx);
      } catch (err) {
        console.warn("drawEdges: skip edge", e, err);
      }
    });
    if (tweaks.healthColors) {
      try { applyHealthEdges(); } catch (err) { console.warn("applyHealthEdges failed", err); }
    }
  }
  // 連線樣式：曲線 / 直角 / 直線
  function connectorPath(sx, sy, tx, ty, kind) {
    const mode = tweaks.connector;
    if (mode === "straight") return `M ${sx} ${sy} L ${tx} ${ty}`;
    if (mode === "elbow") {
      if (kind === "dep") { const mx = Math.min(sx, tx) - 24; return `M ${sx} ${sy} L ${mx} ${sy} L ${mx} ${ty} L ${tx} ${ty}`; }
      const mx = (sx + tx) / 2; return `M ${sx} ${sy} L ${mx} ${sy} L ${mx} ${ty} L ${tx} ${ty}`;
    }
    if (kind === "dep") { const dx = Math.max(28, Math.abs(tx - sx) * 0.3); return `M ${sx} ${sy} C ${sx - dx} ${sy}, ${tx - dx} ${ty}, ${tx} ${ty}`; }
    const dx = Math.max(36, (tx - sx) * 0.45);
    return `M ${sx} ${sy} C ${sx + dx} ${sy}, ${tx - dx} ${ty}, ${tx} ${ty}`;
  }
  function addPath(d, cls, e, idx) {
    const p = document.createElementNS(SVGNS, "path");
    p.setAttribute("d", d); p.setAttribute("class", cls);
    p.dataset.from = e.from; p.dataset.to = e.to; p.dataset.idx = idx;
    svg.appendChild(p);
  }
  // 預設邊色：依兩端較差的健康上色（讓問題路徑自然浮現）
  function applyHealthEdges() {
    const rank = { online: 0, warn: 1, offline: 2 };
    const cls = ["", "e-warn", "e-err"];
    svg.querySelectorAll("path").forEach((p) => {
      try {
        const ha = healthOf(p.dataset.from), hb = healthOf(p.dataset.to);
        const worst = Math.max(rank[ha] ?? 0, rank[hb] ?? 0);
        if (cls[worst]) p.classList.add(cls[worst]);
      } catch (err) {
        console.warn("applyHealthEdges: skip path", p.dataset, err);
      }
    });
  }
  function healthOf(key) {
    if (!key) return "online";
    const [t, id] = key.split(":");
    if (t === "m") return machineHealth(id);
    if (t === "s") return serviceHealth(services.find((s) => s.id === id));
    return softwareHealth(installById[id]); // installById[id] 可能 undefined → softwareHealth 已防衛
  }

  /* ============================================================
     高亮關聯（hover 暫態 / click pin）
     ============================================================ */
  let pinned = null;
  function connectedSet(key) {
    const seen = new Set([key]); const stack = [key];
    while (stack.length) { const k = stack.pop(); (adj[k] || []).forEach((n) => { if (!seen.has(n)) { seen.add(n); stack.push(n); } }); }
    return seen;
  }
  function highlight(key) {
    const set = connectedSet(key);
    topo.querySelectorAll(".node").forEach((n) => {
      const on = set.has(n.dataset.node);
      n.classList.toggle("dim", !on);
      n.classList.toggle("hl", on && n.dataset.node === key);
    });
    svg.querySelectorAll("path").forEach((p) => {
      const on = set.has(p.dataset.from) && set.has(p.dataset.to);
      p.classList.toggle("dim", !on);
      p.classList.toggle("act", on);
    });
  }
  function clearHighlight() {
    if (pinned) return highlight(pinned);
    topo.querySelectorAll(".node").forEach((n) => n.classList.remove("dim", "hl"));
    svg.querySelectorAll("path").forEach((p) => p.classList.remove("dim", "act"));
  }

  topo.addEventListener("mouseover", (e) => {
    const n = e.target.closest(".node"); if (!n || pinned) return;
    highlight(n.dataset.node);
  });
  topo.addEventListener("mouseout", (e) => { if (e.target.closest(".node") && !pinned) clearHighlight(); });
  topo.addEventListener("click", (e) => {
    // VM 連結按鈕：阻止冒泡，開 dropdown overlay
    const linkBtn = e.target.closest("[data-link-vm]");
    if (linkBtn) {
      e.stopPropagation();
      openLinkOverlay(linkBtn, linkBtn.dataset.linkVm);
      return;
    }
    // 收起/展開按鈕：阻止冒泡，不開詳情
    const colBtn = e.target.closest("[data-collapse-machine]");
    if (colBtn) {
      e.stopPropagation();
      toggleCollapse(colBtn.dataset.collapseMachine);
      return;
    }
    const n = e.target.closest(".node"); if (!n) return;
    pinned = n.dataset.node;
    topo.querySelectorAll(".node").forEach((x) => x.classList.toggle("pinned", x === n));
    highlight(pinned);
    openDetail(pinned);
  });

  /* ============================================================
     詳情抽屜
     ============================================================ */
  function sparkPath(arr, w, h) {
    if (!arr || !arr.length) return "";
    const max = Math.max(...arr, 1), min = Math.min(...arr);
    const rng = Math.max(max - min, 1);
    const step = w / (arr.length - 1);
    const pts = arr.map((v, i) => [i * step, h - ((v - min) / rng) * (h - 4) - 2]);
    return { line: "M " + pts.map((p) => p.join(" ")).join(" L "),
             area: "M " + pts.map((p) => p.join(" ")).join(" L ") + ` L ${w} ${h} L 0 ${h} Z` };
  }

  function openDetail(key) {
    const [t, id] = key.split(":");
    if (t === "m") detailMachine(id);
    else if (t === "s") detailService(services.find((s) => s.id === id));
    else detailSoftware(installById[id]);
    $("#overlay").style.display = "block";
    requestAnimationFrame(() => { $("#overlay").classList.add("show"); $("#drawer").classList.add("open"); });
  }
  function closeDetail() {
    clearTimeout(swStreamTimer);
    $("#drawer").classList.remove("open"); $("#overlay").classList.remove("show");
    setTimeout(() => ($("#overlay").style.display = "none"), 300);
    pinned = null;
    topo.querySelectorAll(".node").forEach((x) => x.classList.remove("pinned"));
    clearHighlight();
  }
  $("#drawer-close").addEventListener("click", closeDetail);
  $("#overlay").addEventListener("click", closeDetail);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && pinned) closeDetail(); });

  function titleBar(dot, name, sub) {
    $("#drawer-title").innerHTML = `<span class="sdot ${dot}"></span>
      <div style="min-width:0;"><div class="font-display" style="font-weight:600; font-size:14px; color:var(--text);">${name}</div>
      ${sub ? `<div style="font-size:11px; color:var(--text-3);">${sub}</div>` : ""}</div>`;
  }

  function detailMachine(id) {
    const m = MACHINE_META[id], h = machineHealth(id);
    titleBar(HCLS[h], m.label, `${id} · ${m.role}`);
    const svc = services.filter((s) => s.machine === id);
    const offline = h === "offline";
    const spk = m.spark ? sparkPath(m.spark, 340, 56) : null;
    const big = offline ? `
      <div style="padding:18px; border:1px solid var(--err-bd); background:var(--err-bg); border-radius:12px; color:var(--err); font-size:13px; display:flex; gap:10px; align-items:flex-start;">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><path d="M12 9v4M12 17h.01"/></svg>
        <div><div style="font-weight:600; margin-bottom:3px;">主機離線</div>
        <div style="color:var(--text-2); line-height:1.5;">agent 最後回報於 ${m.last_seen}。${(m.warnings||[]).join("、")}。相關服務與軟體狀態無法更新。</div></div>
      </div>` : `
      <div style="margin-bottom:4px; font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--text-3);">CPU 近 24 點</div>
      <svg width="100%" height="56" viewBox="0 0 340 56" preserveAspectRatio="none" style="display:block; margin-bottom:14px;">
        <path d="${spk.area}" fill="var(--accent-weak)"/><path d="${spk.line}" fill="none" stroke="var(--accent)" stroke-width="1.8"/>
      </svg>
      <div style="display:grid; grid-template-columns:1fr 1fr; gap:10px; margin-bottom:14px;">
        ${gauge("CPU", m.cpu)}${gauge("記憶體", m.mem)}${gauge("磁碟", m.disk)}${m.gpu!=null?gauge("GPU", m.gpu):kv("負載", m.load.join(" / "))}
      </div>
      <div style="display:flex; gap:8px; flex-wrap:wrap; margin-bottom:16px;">
        ${chip("網路 ↑", m.net.up+" MB/s")}${chip("網路 ↓", m.net.down+" MB/s")}${m.temp!=null?chip("溫度", m.temp+"°C"):""}${chip("運行時間", m.uptime)}${chip("負載", m.load.join("/"))}
      </div>`;
    const agentLine = m.agent_status === "behind"
      ? `<span class="badge b-warn">agent ${m.agent} · 有新版</span>`
      : m.agent_status === "stale" ? `<span class="badge b-err">agent 失聯</span>`
      : `<span class="badge b-ok">agent ${m.agent}</span>`;
    const vmChip = (m.kind === "vm" && m.host_id)
      ? (() => {
          const hostMeta = MACHINE_META[m.host_id];
          const hostLabel = hostMeta ? hostMeta.label : m.host_id;
          return `<span class="badge b-mut" style="font-size:12px;">VM @ ${hostLabel}</span>`;
        })()
      : "";
    $("#drawer-body").innerHTML = `
      <div style="display:flex; gap:8px; align-items:center; margin-bottom:14px; flex-wrap:wrap;">
        <span class="tag">${m.os}</span><span class="tag">${m.arch}</span>${agentLine}${vmChip}
      </div>
      ${big}
      <div style="font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--text-3); margin-bottom:8px;">運行的服務 (${svc.length})</div>
      <div style="display:flex; flex-direction:column; gap:7px;">
        ${svc.map((s) => relRow("s:"+s.id, KIND_ICON[s.kind]||KIND_ICON.service, s.name, s.kind, HCLS[serviceHealth(s)])).join("")}
      </div>`;
  }

  function detailService(s) {
    const h = serviceHealth(s), m = MACHINE_META[s.machine];
    titleBar(HCLS[h], s.name, `${s.kind} · 於 ${m.label}`);
    const sw = s.software.map((id) => installById[id]).filter(Boolean);
    const deps = (s.depends || []).map((d) => services.find((x) => x.id === d)).filter(Boolean);
    const statBadge = s.status === "running" ? `<span class="badge b-ok">running</span>`
      : s.status === "restarting" ? `<span class="badge b-warn">restarting</span>` : `<span class="badge b-err">stopped</span>`;
    $("#drawer-body").innerHTML = `
      <div style="display:flex; gap:8px; align-items:center; margin-bottom:14px; flex-wrap:wrap;">
        ${statBadge}<span class="tag">${s.kind}</span>${s.port?`<span class="tag mono">port :${s.port}</span>`:""}
      </div>
      ${s.cpu!=null?`<div style="display:grid; grid-template-columns:1fr 1fr; gap:10px; margin-bottom:16px;">${gauge("容器 CPU", s.cpu)}${gauge("容器 MEM", s.mem)}</div>`
        :`<div style="padding:12px 14px; background:var(--surface-2); border:1px solid var(--border); border-radius:10px; font-size:12.5px; color:var(--text-2); margin-bottom:16px;">系統層套件群組，無獨立容器資源。</div>`}
      <div style="font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--text-3); margin-bottom:8px;">所在主機</div>
      <div style="margin-bottom:16px;">${relRow("m:"+s.machine, KIND_ICON.bundle, m.label, s.machine, HCLS[machineHealth(s.machine)])}</div>
      ${deps.length?`<div style="font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--text-3); margin-bottom:8px;">依賴服務</div>
        <div style="display:flex; flex-direction:column; gap:7px; margin-bottom:16px;">${deps.map((d)=>relRow("s:"+d.id, KIND_ICON[d.kind]||KIND_ICON.service, d.name, d.kind, HCLS[serviceHealth(d)])).join("")}</div>`:""}
      <div style="font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--text-3); margin-bottom:8px;">軟體組成 (${sw.length})</div>
      <div style="display:flex; flex-direction:column; gap:7px;">
        ${sw.map((w)=>relRow("w:"+w.id, KIND_ICON.runtime, `${w.software} <span class="mono" style="color:var(--text-3); font-size:11px;">${w.current_version}</span>`, w.kind, HCLS[softwareHealth(w)])).join("")}
      </div>`;
  }

  function detailSoftware(w) {
    const h = softwareHealth(w);
    titleBar(HCLS[h], w.software, `${w.kind} · 於 ${w.machine}`);
    const owners = services.filter((s) => s.software.includes(w.id));
    const key = `${w.software}@${w.latest_version}`, cl = VERSIONS[key];
    const [cls, lbl] = WSTATUS[w.status] || WSTATUS.unknown;
    const statusBlock = w.status === "behind"
      ? `<div style="display:flex; align-items:center; gap:10px; padding:12px 14px; background:var(--warn-bg); border:1px solid var(--warn-bd); border-radius:10px; margin-bottom:16px;">
           <span class="mono" style="font-size:13px;">${w.current_version}</span>
           <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--warn)" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14M13 6l6 6-6 6"/></svg>
           <span class="mono" style="font-size:13px; color:var(--warn); font-weight:600;">${w.latest_version}</span>
           <span style="flex:1;"></span><span class="badge b-warn">落後 ${w.behind_count} 版</span>
         </div>`
      : w.status === "error"
      ? `<div style="padding:12px 14px; background:var(--err-bg); border:1px solid var(--err-bd); border-radius:10px; margin-bottom:16px; color:var(--err); font-size:12.5px;"><div style="font-weight:600; margin-bottom:3px;">檢查失敗</div><div class="mono" style="color:var(--text-2); font-size:11.5px; line-height:1.5;">${(w.error||"").replace(/</g,"&lt;")}</div></div>`
      : `<div style="display:flex; align-items:center; gap:8px; padding:12px 14px; background:var(--surface-2); border:1px solid var(--border); border-radius:10px; margin-bottom:16px;"><span class="mono" style="font-size:13px;">${w.current_version}</span><span style="flex:1;"></span><span class="badge ${cls}">${lbl}</span></div>`;
    const changelog = cl ? `
      <div style="font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--text-3); margin-bottom:8px;">${w.latest_version} changelog</div>
      <div class="md" style="font-size:13px; line-height:1.6; color:var(--text-2); margin-bottom:16px;">${mdToHtml(cl.changelog_zh)}</div>` : "";
    $("#drawer-body").innerHTML = `
      <div style="display:flex; gap:8px; align-items:center; margin-bottom:14px; flex-wrap:wrap;">
        <span class="tag">${w.kind}</span><span class="tag">${w.machine}</span>${w.update_kind==="agent"?`<span class="tag" style="color:var(--accent); border-color:var(--accent);">agent 更新</span>`:""}
      </div>
      ${statusBlock}${changelog}
      <div style="font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--text-3); margin-bottom:8px;">被以下服務使用 (${owners.length})</div>
      <div style="display:flex; flex-direction:column; gap:7px; margin-bottom:18px;">
        ${owners.map((s)=>relRow("s:"+s.id, KIND_ICON[s.kind]||KIND_ICON.service, s.name, s.kind+" · "+s.machine, HCLS[serviceHealth(s)])).join("") || `<div style="font-size:12px; color:var(--text-3);">—</div>`}
      </div>
      ${w.status==="behind" ? `
        <button id="sw-update-btn" class="btn" data-update-sw="${w.id}" style="width:100%; justify-content:center; background:var(--accent); border-color:var(--accent); color:var(--accent-ink);">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.64-6.36"/><path d="M21 3v6h-6"/></svg>
          ${w.update_kind==="agent"?"委派 agent 更新":"在此更新"} → ${w.latest_version}
        </button>
        <div id="sw-job"></div>` : ""}`;
  }

  /* ---- 拓樸頁內就地更新（重用 mock-data 的 JOB_SCRIPTS + 共用 store）---- */
  let swStreamTimer = null;
  function colorize(line) {
    const esc = line.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;");
    if (/^✓/.test(line)) return `<span style="color:#4ade80;">${esc}</span>`;
    if (/^✗/.test(line)) return `<span style="color:#f87171;">${esc}</span>`;
    if (/^■/.test(line)) return `<span style="color:#9aa4b2;">${esc}</span>`;
    if (/^▶/.test(line)) return `<span style="color:#7dd3fc;font-weight:600;">${esc}</span>`;
    if (/^→/.test(line)) return `<span style="color:#c4b5fd;">${esc}</span>`;
    return `<span style="opacity:.82;">${esc}</span>`;
  }
  function runUpdate(w) {
    const script = JOB_SCRIPTS[w.software] || JOB_SCRIPTS._command;
    const isAgent = script.kind === "agent";
    const btn = $("#sw-update-btn");
    if (btn) { btn.disabled = true; btn.style.opacity = ".5"; btn.style.cursor = "not-allowed"; }
    const box = $("#sw-job");
    box.innerHTML = `
      <div style="margin-top:12px;">
        ${isAgent?`<div style="font-size:11px; color:var(--text-3); margin-bottom:6px;">runner <span class="mono" style="color:var(--text-2);">${script.runner}</span></div>`:""}
        <div style="display:flex; align-items:center; gap:7px; margin-bottom:8px;">
          <svg class="spin" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--warn)" stroke-width="3" stroke-linecap="round"><path d="M21 12a9 9 0 1 1-6.22-8.56"/></svg>
          <span style="font-size:12px; color:var(--warn);">更新執行中…</span>
        </div>
        <div id="sw-term" style="background:#07090c; color:#cdd3db; font-family:'JetBrains Mono',monospace; font-size:11.5px; line-height:1.6; border-radius:10px; padding:11px 13px; max-height:230px; overflow-y:auto;"></div>
      </div>`;
    const term = $("#sw-term");
    const lines = script.lines; let i = 0;
    const step = () => {
      if (i >= lines.length) return finish();
      const d = document.createElement("div");
      d.style.whiteSpace = "pre-wrap"; d.style.wordBreak = "break-word";
      d.innerHTML = colorize(lines[i].s);
      term.appendChild(d); term.scrollTop = term.scrollHeight;
      i++; swStreamTimer = setTimeout(step, lines[i-1].t);
    };
    function finish() {
      const ok = (script.result || "success") === "success";
      const nv = script.new_version || w.latest_version;
      if (ok) {
        // 更新資料 + 共用 store（清單頁 / 機器頁同步）
        const inst = installById[w.id];
        inst.current_version = nv; inst.status = "up_to_date"; inst.behind_count = 0;
        if (window.CockpitStore) window.CockpitStore.applyUpdate(w.id, { current_version: nv, status: "up_to_date", behind_count: 0 });
      }
      // 重畫拓樸（節點/連線健康），保留目前 pin 並重開詳情
      renderAll(); renderSummary(); drawEdges();
      const keep = "w:" + w.id;
      pinned = keep;
      topo.querySelectorAll(".node").forEach((x) => x.classList.toggle("pinned", x.dataset.node === keep));
      highlight(keep);
      openDetail(keep);   // 重建詳情（此時 status 已是最新）
    }
    swStreamTimer = setTimeout(step, 300);
  }

  /* 詳情裡可點的關聯列 → 跳到該節點 */
  function relRow(key, icon, name, sub, dot) {
    return `<button class="rel-row" data-goto="${key}" style="display:flex; align-items:center; gap:9px; width:100%; text-align:left; padding:8px 10px; border:1px solid var(--border); background:var(--surface); color:var(--text); border-radius:9px; cursor:pointer; transition:.14s;">
      <span class="sdot ${dot}"></span><span style="color:var(--text-3); display:flex;">${icon}</span>
      <span style="flex:1; min-width:0;"><span style="font-size:12.5px; font-weight:500; color:var(--text);">${name}</span><div style="font-size:10.5px; color:var(--text-3);">${sub}</div></span>
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--text-3)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg>
    </button>`;
  }
  $("#drawer-body").addEventListener("mouseover", (e) => { const r = e.target.closest(".rel-row"); if (r) r.style.background = "var(--surface-2)"; });
  $("#drawer-body").addEventListener("mouseout", (e) => { const r = e.target.closest(".rel-row"); if (r) r.style.background = "var(--surface)"; });
  $("#drawer-body").addEventListener("click", (e) => {
    const upd = e.target.closest("[data-update-sw]");
    if (upd) { runUpdate(installById[upd.getAttribute("data-update-sw")]); return; }
    const r = e.target.closest("[data-goto]"); if (!r) return;
    const key = r.getAttribute("data-goto");
    pinned = key;
    topo.querySelectorAll(".node").forEach((x) => x.classList.toggle("pinned", x.dataset.node === key));
    highlight(key); openDetail(key);
    const n = nodeEl(key); if (n) n.scrollIntoView ? n.scrollIntoView({ block: "nearest", behavior: "smooth" }) : null;
  });

  /* small UI helpers */
  function gauge(label, v) {
    return `<div style="border:1px solid var(--border); border-radius:10px; padding:9px 11px; background:var(--surface);">
      <div style="display:flex; justify-content:space-between; align-items:baseline; margin-bottom:6px;">
        <span style="font-size:11px; color:var(--text-3);">${label}</span>
        <span class="mono" style="font-size:13px; font-weight:600; color:${barColor(v)};">${v}%</span></div>
      <span class="bar" style="display:block;"><i style="width:${v}%; background:${barColor(v)};"></i></span></div>`;
  }
  const kv = (k, v) => `<div style="border:1px solid var(--border); border-radius:10px; padding:9px 11px; background:var(--surface);"><div style="font-size:11px; color:var(--text-3); margin-bottom:4px;">${k}</div><div class="mono" style="font-size:13px; font-weight:600; color:var(--text);">${v}</div></div>`;
  const chip = (k, v) => `<span style="display:inline-flex; gap:5px; align-items:center; font-size:11.5px; color:var(--text-2); background:var(--surface-2); border:1px solid var(--border); border-radius:8px; padding:4px 9px;"><span style="color:var(--text-3);">${k}</span><span class="mono" style="color:var(--text-2);">${v}</span></span>`;

  function mdToHtml(src) {
    const esc = (s) => s.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;");
    const inline = (s) => esc(s).replace(/\*\*(.+?)\*\*/g,"<strong>$1</strong>").replace(/`(.+?)`/g,"<code>$1</code>");
    let html="", inList=false;
    src.split("\n").forEach((ln) => {
      const m = ln.match(/^\s*-\s+(.*)$/);
      if (m) { if (!inList){html+="<ul>";inList=true;} html+=`<li>${inline(m[1])}</li>`; }
      else { if (inList){html+="</ul>";inList=false;} if (ln.trim()) html+=`<p>${inline(ln)}</p>`; }
    });
    if (inList) html+="</ul>"; return html;
  }

  /* ============================================================
     摘要 + 只看有問題
     ============================================================ */
  function renderSummary() {
    const onM = MACHINE_ORDER.filter((id) => machineHealth(id) === "online").length;
    const offM = MACHINE_ORDER.filter((id) => machineHealth(id) === "offline").length;
    const runS = services.filter((s) => s.status === "running").length;
    const behindW = INSTALLS.filter((i) => i.status === "behind").length;
    $("#summary").innerHTML = `
      <span style="display:flex; align-items:center; gap:6px;"><span class="sdot s-online"></span>${onM}/${MACHINE_ORDER.length} 機器線上</span>
      <span style="display:flex; align-items:center; gap:6px;">${runS}/${services.length} 服務運行</span>
      <span style="display:flex; align-items:center; gap:6px;"><span class="sdot s-warn"></span>${behindW} 軟體可更新</span>
      ${offM?`<span style="display:flex; align-items:center; gap:6px; color:var(--err);"><span class="sdot s-offline"></span>${offM} 離線</span>`:""}`;
  }

  let issuesOnly = false;
  $("#filter-issues").addEventListener("click", () => {
    issuesOnly = !issuesOnly;
    $("#filter-issues").setAttribute("aria-pressed", issuesOnly);
    $("#filter-issues").style.background = issuesOnly ? "var(--accent)" : "";
    $("#filter-issues").style.color = issuesOnly ? "var(--accent-ink)" : "";
    $("#filter-issues").style.borderColor = issuesOnly ? "var(--accent)" : "";
    topo.querySelectorAll(".node").forEach((n) => {
      const bad = n.dataset.health !== "online";
      n.style.display = issuesOnly && !bad ? "none" : "";
    });
    // 重新套用 collapse 狀態（確保 VM 節點不被 filter 重置影響）
    applyCollapse();
    drawEdges();
    if (issuesOnly) svg.querySelectorAll("path").forEach((p) => {
      if (healthOf(p.dataset.from) === "online" && healthOf(p.dataset.to) === "online") p.style.display = "none";
    });
  });

  /* ============================================================
     Tweaks 面板（佈局探索）
     ============================================================ */
  const TW_UI = [
    { sec: "連線" },
    { type: "seg", key: "connector", label: "連線樣式", opts: [["bezier","曲線"],["elbow","直角"],["straight","直線"]] },
    { type: "toggle", key: "healthColors", label: "依健康狀態上色" },
    { type: "toggle", key: "showDepEdges", label: "顯示服務依賴線" },
    { sec: "佈局" },
    { type: "seg", key: "grouping", label: "排列方式", opts: [["flow","緊湊"],["grouped","機器分群"]] },
    { type: "range", key: "colGap", label: "欄距", min: 48, max: 120, step: 4 },
    { type: "seg", key: "density", label: "節點密度", opts: [["regular","標準"],["compact","緊密"]] },
  ];
  function buildPanelControls() {
    $("#tw-body").innerHTML = TW_UI.map((c) => {
      if (c.sec) return `<div class="tw-sec">${c.sec}</div>`;
      if (c.type === "seg")
        return `<div class="tw-row"><div class="tw-lbl">${c.label}</div><div class="tw-seg">${
          c.opts.map(([v, l]) => `<button data-twset="${c.key}" data-val="${v}" class="${tweaks[c.key]===v?"active":""}">${l}</button>`).join("")
        }</div></div>`;
      if (c.type === "toggle")
        return `<div class="tw-row" style="display:flex; align-items:center; justify-content:space-between;"><div class="tw-lbl" style="margin:0;">${c.label}</div><div class="tw-switch ${tweaks[c.key]?"on":""}" data-twtoggle="${c.key}"><i></i></div></div>`;
      if (c.type === "range")
        return `<div class="tw-row"><div class="tw-lbl">${c.label}<span class="tw-val mono">${tweaks[c.key]}px</span></div><input type="range" class="tw-range" data-twrange="${c.key}" min="${c.min}" max="${c.max}" step="${c.step}" value="${tweaks[c.key]}"></div>`;
      return "";
    }).join("");
  }
  function refreshActive() {
    document.querySelectorAll("[data-twset]").forEach((b) => b.classList.toggle("active", tweaks[b.dataset.twset] === b.dataset.val));
    document.querySelectorAll("[data-twtoggle]").forEach((t) => t.classList.toggle("on", !!tweaks[t.dataset.twtoggle]));
    document.querySelectorAll("[data-twrange]").forEach((r) => { const l = r.closest(".tw-row").querySelector(".tw-val"); if (l) l.textContent = tweaks[r.dataset.twrange] + "px"; });
  }
  function applyColGapDensity() {
    document.querySelector(".cols").style.gap = tweaks.colGap + "px";
    document.body.classList.toggle("density-compact", tweaks.density === "compact");
  }
  function applyTweaks() {
    applyColGapDensity();
    renderAll(); drawEdges();
    if (pinned) { topo.querySelectorAll(".node").forEach((x) => x.classList.toggle("pinned", x.dataset.node === pinned)); highlight(pinned); }
  }
  function setTweak(key, val) {
    tweaks[key] = val;
    window.parent.postMessage({ type: "__edit_mode_set_keys", edits: { [key]: val } }, "*");
    applyTweaks(); refreshActive();
  }
  $("#tw-body").addEventListener("click", (e) => {
    const seg = e.target.closest("[data-twset]"); if (seg) return setTweak(seg.dataset.twset, seg.dataset.val);
    const tg = e.target.closest("[data-twtoggle]"); if (tg) return setTweak(tg.dataset.twtoggle, !tweaks[tg.dataset.twtoggle]);
  });
  $("#tw-body").addEventListener("input", (e) => {
    const r = e.target.closest("[data-twrange]"); if (r) setTweak(r.dataset.twrange, +r.value);
  });
  // host 協定：toolbar 開關 Tweaks
  window.addEventListener("message", (e) => {
    const t = e && e.data && e.data.type;
    if (t === "__activate_edit_mode") $("#tweaks").classList.add("show");
    else if (t === "__deactivate_edit_mode") $("#tweaks").classList.remove("show");
  });
  $("#tw-close").addEventListener("click", () => { $("#tweaks").classList.remove("show"); window.parent.postMessage({ type: "__edit_mode_dismissed" }, "*"); });

  /* ============================================================
     啟動
     ============================================================ */
  initTheme();
  buildPanelControls();
  applyColGapDensity();
  renderAll();
  renderSummary();
  drawEdges();
  window.parent.postMessage({ type: "__edit_mode_available" }, "*");
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(drawEdges);
  let rt;
  const onResize = () => { clearTimeout(rt); rt = setTimeout(drawEdges, 80); };
  new ResizeObserver(onResize).observe(topo);
  window.addEventListener("resize", onResize);
  window.addEventListener("topo:refresh", () => { renderAll(); renderSummary(); drawEdges(); });
})();
