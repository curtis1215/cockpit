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

  /* ---- 健康判定 ---- */
  const machineHealth = (id) => MACHINE_META[id].status;        // online|warn|offline
  function serviceHealth(s) {
    if (machineHealth(s.machine) === "offline" || s.status === "stopped") return "offline";
    if (s.status === "restarting") return "warn";
    if (s.software.some((w) => installById[w]?.status === "error")) return "offline";
    if (s.software.some((w) => installById[w]?.status === "behind")) return "warn";
    return "online";
  }
  function softwareHealth(w) {
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
     render 節點
     ============================================================ */
  function machineNode(id) {
    const m = MACHINE_META[id];
    const h = machineHealth(id);
    const svcCount = services.filter((s) => s.machine === id).length;
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
    const foot = h === "pending"
      ? `<div class="m-foot"><span class="badge b-mut">等待連線</span><span>尚未回報</span>${agentBadge}</div>`
      : offline
      ? `<div class="m-foot"><span class="badge b-err">離線</span><span>最後回報 ${m.last_seen}</span>${agentBadge}</div>`
      : `<div class="m-foot">${agentBadge}<span>${m.uptime}</span><span>·</span><span>${svcCount} 服務</span><span>·</span><span>${m.temp}°C</span></div>`;
    return `<div class="node m-node" data-node="m:${id}" data-machine="${id}" data-health="${h}">
      <div class="node-l" style="background:var(--${lcol});"></div>
      <div class="m-top">
        <div style="min-width:0;">
          <div style="display:flex; align-items:center; gap:7px;"><span class="sdot ${HCLS[h]} ${h!=="online"?"pulse":""}"></span><span class="m-name">${m.label}</span></div>
          <div class="m-role">${id} · ${m.role}</div>
        </div>
        <span class="tag" style="margin-top:2px;">${m.arch}</span>
      </div>
      ${metrics}${gpu}${foot}
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
    const [cls, lbl] = WSTATUS[w.status];
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
    $("#col-machines").innerHTML = MACHINE_ORDER.map(machineNode).join("");
    $("#col-services").innerHTML = services.map(serviceNode).join("");
    $("#col-software").innerHTML = currentSoftware().map(softwareNode).join("");
    $("#c-machine").textContent = MACHINE_ORDER.length;
    $("#c-service").textContent = services.length;
    $("#c-software").textContent = software.length;
    applyGrouping();
  }

  // 機器分群：同機器節點間留白成帶狀
  function applyGrouping() {
    const on = tweaks.grouping === "grouped";
    ["#col-machines", "#col-services", "#col-software"].forEach((sel) => {
      let prev = null;
      [...$(sel).children].forEach((n, i) => {
        const m = n.dataset.machine;
        n.style.marginTop = on && i > 0 && m !== prev ? "28px" : "";
        prev = m;
      });
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
    });
    if (tweaks.healthColors) applyHealthEdges();
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
      const ha = healthOf(p.dataset.from), hb = healthOf(p.dataset.to);
      const worst = Math.max(rank[ha] ?? 0, rank[hb] ?? 0);
      if (cls[worst]) p.classList.add(cls[worst]);
    });
  }
  function healthOf(key) {
    const [t, id] = key.split(":");
    if (t === "m") return machineHealth(id);
    if (t === "s") return serviceHealth(services.find((s) => s.id === id));
    return softwareHealth(installById[id]);
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
      <div style="min-width:0;"><div class="font-display" style="font-weight:600; font-size:14px;">${name}</div>
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
        ${chip("網路 ↑", m.net.up+" MB/s")}${chip("網路 ↓", m.net.down+" MB/s")}${chip("溫度", m.temp+"°C")}${chip("運行時間", m.uptime)}${chip("負載", m.load.join("/"))}
      </div>`;
    const agentLine = m.agent_status === "behind"
      ? `<span class="badge b-warn">agent ${m.agent} · 有新版</span>`
      : m.agent_status === "stale" ? `<span class="badge b-err">agent 失聯</span>`
      : `<span class="badge b-ok">agent ${m.agent}</span>`;
    $("#drawer-body").innerHTML = `
      <div style="display:flex; gap:8px; align-items:center; margin-bottom:14px; flex-wrap:wrap;">
        <span class="tag">${m.os}</span><span class="tag">${m.arch}</span>${agentLine}
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
    const [cls, lbl] = WSTATUS[w.status];
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
    return `<button class="rel-row" data-goto="${key}" style="display:flex; align-items:center; gap:9px; width:100%; text-align:left; padding:8px 10px; border:1px solid var(--border); background:var(--surface); border-radius:9px; cursor:pointer; transition:.14s;">
      <span class="sdot ${dot}"></span><span style="color:var(--text-3); display:flex;">${icon}</span>
      <span style="flex:1; min-width:0;"><span style="font-size:12.5px; font-weight:500;">${name}</span><div style="font-size:10.5px; color:var(--text-3);">${sub}</div></span>
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
  const kv = (k, v) => `<div style="border:1px solid var(--border); border-radius:10px; padding:9px 11px; background:var(--surface);"><div style="font-size:11px; color:var(--text-3); margin-bottom:4px;">${k}</div><div class="mono" style="font-size:13px; font-weight:600;">${v}</div></div>`;
  const chip = (k, v) => `<span style="display:inline-flex; gap:5px; align-items:center; font-size:11.5px; color:var(--text-2); background:var(--surface-2); border:1px solid var(--border); border-radius:8px; padding:4px 9px;"><span style="color:var(--text-3);">${k}</span><span class="mono">${v}</span></span>`;

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
})();
