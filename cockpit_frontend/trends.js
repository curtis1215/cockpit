/* =============================================================
   cockpit · 機器頁 — 數據走勢 (vanilla JS, beszel 風格)
   單機多指標時序圖：CPU / 記憶體 / 磁碟 / GPU / 網路 / 負載 / 溫度
   含 hover 十字準星 + tooltip、區間切換、即時統計卡、容器清單。
   ============================================================= */
(() => {
  const { MACHINE_META, MACHINE_ORDER, SERVICES } = window.TOPO;
  const { INSTALLS } = window.MOCK;
  const { RANGES, series, fmt } = window.TRENDS;
  const $ = (s, r = document) => r.querySelector(s);
  const installById = Object.fromEntries(INSTALLS.map((i) => [i.id, i]));

  const firstOnline = MACHINE_ORDER.find((id) => MACHINE_META[id].status !== "offline") || MACHINE_ORDER[0];
  const state = {
    machine: localStorage.getItem("cockpit-machine") || firstOnline,
    range: localStorage.getItem("cockpit-range") || "24h",
  };
  if (!MACHINE_META[state.machine]) state.machine = firstOnline;

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
    localStorage.setItem("cockpit-theme", dark ? "dark" : "light");
    syncIcon(); renderCharts();
  });

  const HCLS = { online: "s-online", warn: "s-warn", offline: "s-offline", pending: "s-pending" };
  const barColor = (v) => (v >= 85 ? "var(--err)" : v >= 60 ? "var(--warn)" : "var(--ok)");

  /* ============================================================
     機器切換器（可搜尋，適合機器數量多時）+ 區間
     ============================================================ */
  function renderSwitcher() {
    const m = MACHINE_META[state.machine];
    $("#sw-dot").className = "sdot " + (HCLS[m.status] || "s-offline") + (m.status !== "online" ? " pulse" : "");
    $("#sw-name").textContent = m.label;
    $("#sw-id").textContent = state.machine;
    $("#m-count").textContent = MACHINE_ORDER.length + " 台";
    const idx = MACHINE_ORDER.indexOf(state.machine);
    $("#m-prev").disabled = idx <= 0;
    $("#m-next").disabled = idx >= MACHINE_ORDER.length - 1;
  }
  function renderPopoverList(filter) {
    const f = (filter || "").trim().toLowerCase();
    const ids = MACHINE_ORDER.filter((id) => !f || id.toLowerCase().includes(f) || MACHINE_META[id].label.toLowerCase().includes(f));
    $("#mp-list").innerHTML = ids.map((id) => {
      const m = MACHINE_META[id], sel = id === state.machine;
      return `<div class="mp-item ${sel ? "sel" : ""}" data-pick="${id}" role="option" aria-selected="${sel}">
        <span class="sdot ${HCLS[m.status] || "s-offline"} ${m.status !== "online" ? "pulse" : ""}"></span>
        <span style="flex:1;min-width:0;"><div style="font-size:13px;font-weight:550;">${m.label}</div><div style="font-size:10.5px;color:var(--text-3);">${id} · ${m.role}</div></span>
        ${sel ? `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>` : ""}
      </div>`;
    }).join("") || `<div style="padding:18px;text-align:center;color:var(--text-3);font-size:12.5px;">找不到機器</div>`;
  }
  function openPopover() {
    $("#m-popover").classList.add("show"); $("#m-switcher").setAttribute("aria-expanded", "true");
    $("#mp-search").value = ""; renderPopoverList(""); setTimeout(() => $("#mp-search").focus(), 30);
  }
  function closePopover() { $("#m-popover").classList.remove("show"); $("#m-switcher").setAttribute("aria-expanded", "false"); }
  function selectMachine(id) { state.machine = id; localStorage.setItem("cockpit-machine", id); closePopover(); render(); }

  $("#m-switcher").addEventListener("click", (e) => { e.stopPropagation(); $("#m-popover").classList.contains("show") ? closePopover() : openPopover(); });
  $("#mp-search").addEventListener("input", (e) => renderPopoverList(e.target.value));
  $("#mp-list").addEventListener("click", (e) => { const it = e.target.closest("[data-pick]"); if (it) selectMachine(it.getAttribute("data-pick")); });
  $("#m-prev").addEventListener("click", () => { const i = MACHINE_ORDER.indexOf(state.machine); if (i > 0) selectMachine(MACHINE_ORDER[i - 1]); });
  $("#m-next").addEventListener("click", () => { const i = MACHINE_ORDER.indexOf(state.machine); if (i < MACHINE_ORDER.length - 1) selectMachine(MACHINE_ORDER[i + 1]); });
  document.addEventListener("click", (e) => { if (!e.target.closest("#m-popover") && !e.target.closest("#m-switcher")) closePopover(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closePopover(); });

  function renderRangeTabs() {
    $("#range-tabs").innerHTML = Object.entries(RANGES).map(([k, r]) =>
      `<button class="range-btn ${k === state.range ? "active" : ""}" data-range="${k}">${r.label}</button>`).join("");
  }

  $("#range-tabs").addEventListener("click", (e) => {
    const b = e.target.closest("[data-range]"); if (!b) return;
    state.range = b.getAttribute("data-range");
    localStorage.setItem("cockpit-range", state.range);
    renderRangeTabs(); renderCharts();
  });

  /* ============================================================
     機器標題 + 即時統計
     ============================================================ */
  function renderHead() {
    const m = MACHINE_META[state.machine], h = m.status;
    const warn = (m.warnings || []).length
      ? `<div style="display:flex;gap:7px;flex-wrap:wrap;margin-top:8px;">${m.warnings.map((w) =>
          `<span class="badge ${h==="offline"?"b-err":"b-warn"}">${w}</span>`).join("")}</div>` : "";
    const agentBadge = m.agent_status === "behind" ? `<span class="badge b-warn">agent ${m.agent} · 有新版</span>`
      : m.agent_status === "stale" ? `<span class="badge b-err">agent 失聯</span>`
      : `<span class="tag">agent ${m.agent}</span>`;
    $("#machine-head").innerHTML = `
      <div style="display:flex;align-items:center;gap:10px;">
        <span class="sdot ${HCLS[h]} ${h!=="online"?"pulse":""}" style="width:11px;height:11px;"></span>
        <h1 class="font-display" style="font-size:24px;font-weight:700;margin:0;letter-spacing:-.01em;">${m.label}</h1>
        <span class="tag">${state.machine}</span>
      </div>
      <div style="display:flex;align-items:center;gap:8px;margin-top:7px;flex-wrap:wrap;font-size:12.5px;color:var(--text-2);">
        <span>${m.role}</span><span style="color:var(--text-3);">·</span>
        <span class="tag">${m.os}</span><span class="tag">${m.arch}</span>
        ${h==="online"||h==="warn"?`<span style="color:var(--text-3);">·</span><span>運行 ${m.uptime}</span><span style="color:var(--text-3);">·</span><span>${m.temp}°C</span>`:h==="pending"?`<span style="color:var(--accent);">· 等待 agent 連線</span>`:`<span style="color:var(--err);">· 離線 · 最後回報 ${m.last_seen}</span>`}
        ${agentBadge}
      </div>${warn}`;
  }

  function statCard(label, value, sub, accent) {
    return `<div class="card stat" style="padding:13px 15px;">
      <div style="font-size:11px;color:var(--text-3);text-transform:uppercase;letter-spacing:.05em;margin-bottom:8px;">${label}</div>
      <div class="v" style="color:${accent||"var(--text)"};">${value}</div>
      ${sub?`<div style="font-size:11px;color:var(--text-3);margin-top:5px;">${sub}</div>`:""}
    </div>`;
  }
  function renderStats() {
    const m = MACHINE_META[state.machine];
    const cards = [
      statCard("CPU", m.cpu + "%", `負載 ${m.load.join(" / ")}`, barColor(m.cpu)),
      statCard("記憶體", m.mem + "%", null, barColor(m.mem)),
      statCard("磁碟", m.disk + "%", null, barColor(m.disk)),
    ];
    if (m.gpu != null) cards.push(statCard("GPU", m.gpu + "%", null, barColor(m.gpu)));
    cards.push(statCard("網路", `↑${m.net.up}`, `↓${m.net.down} MB/s`, "var(--text)"));
    cards.push(statCard("溫度", m.temp + "°C", null, m.temp >= 70 ? "var(--err)" : m.temp >= 55 ? "var(--warn)" : "var(--ok)"));
    $("#stat-cards").innerHTML = cards.join("");
  }

  /* ============================================================
     圖表（SVG area + line，含 hover 準星）
     ============================================================ */
  const CHART_DEFS = [
    { metrics: ["cpu"],            title: "CPU 使用率" },
    { metrics: ["mem"],            title: "記憶體使用率" },
    { metrics: ["gpu"],            title: "GPU 使用率" },
    { metrics: ["netDown","netUp"],title: "網路吞吐", legend: ["下載","上傳"] },
    { metrics: ["disk"],           title: "磁碟使用率" },
    { metrics: ["load"],           title: "系統負載" },
    { metrics: ["temp"],           title: "溫度" },
  ];

  function renderCharts() {
    const m = MACHINE_META[state.machine];
    const grid = $("#charts");
    if (m.status === "offline") { grid.innerHTML = ""; return; }
    const defs = CHART_DEFS.filter((d) => series(state.machine, d.metrics[0], state.range));
    grid.innerHTML = defs.map((d, idx) => {
      const primary = series(state.machine, d.metrics[0], state.range);
      const legend = d.legend
        ? `<div style="display:flex;gap:12px;">${d.metrics.map((mt, i) => {
            const s = series(state.machine, mt, state.range);
            return `<span style="display:flex;align-items:center;gap:5px;font-size:11px;color:var(--text-3);"><span style="width:9px;height:3px;border-radius:2px;background:${s.color};"></span>${d.legend[i]}</span>`;
          }).join("")}</div>`
        : "";
      return `<div class="card chart-card" style="padding:14px 15px 10px;" data-chart="${idx}">
        <div style="display:flex;align-items:flex-start;justify-content:space-between;gap:10px;margin-bottom:10px;">
          <div>
            <div style="font-size:12.5px;font-weight:600;">${d.title}</div>
            <div style="display:flex;gap:10px;margin-top:4px;font-size:10.5px;color:var(--text-3);">
              <span>當前 <span class="mono" style="color:var(--text-2);">${fmt(primary.last, primary.unit)}</span></span>
              <span>平均 <span class="mono">${fmt(primary.avg, primary.unit)}</span></span>
              <span>峰值 <span class="mono">${fmt(primary.max, primary.unit)}</span></span>
            </div>
          </div>${legend}
        </div>
        <div class="chart-wrap" style="height:150px;"><svg style="display:block;width:100%;height:150px;"></svg><div class="crosshair"></div><div class="tip"></div></div>
      </div>`;
    }).join("");
    // 繪製 + wire（下一拍，確保 clientWidth 可量）
    requestAnimationFrame(() => defs.forEach((d, idx) => drawChart($(`[data-chart="${idx}"]`), d)));
    setTimeout(() => defs.forEach((d, idx) => { const c = $(`[data-chart="${idx}"]`); if (c && !c.__drawn) drawChart(c, d); }), 60);
  }

  function drawChart(card, def) {
    if (!card) return;
    const wrap = card.querySelector(".chart-wrap");
    const svg = wrap.querySelector("svg");
    const W = wrap.clientWidth || 360, H = 150, padB = 22, plotH = H - padB;
    const seriesList = def.metrics.map((mt) => series(state.machine, mt, state.range)).filter(Boolean);
    if (!seriesList.length) return;
    const n = seriesList[0].points.length;
    // 自動縮放
    let lo = Math.min(...seriesList.flatMap((s) => s.points));
    let hi = Math.max(...seriesList.flatMap((s) => s.points));
    const pad = (hi - lo) * 0.25 || hi * 0.15 || 1;
    lo = Math.max(0, lo - pad); hi = hi + pad;
    if (seriesList[0].pct) hi = Math.min(100, hi);
    const X = (i) => (i / (n - 1)) * W;
    const Y = (v) => plotH - ((v - lo) / (hi - lo || 1)) * plotH;

    const NS = "http://www.w3.org/2000/svg";
    let svgInner = "";
    // 水平格線
    for (let g = 0; g <= 3; g++) {
      const y = (plotH / 3) * g;
      svgInner += `<line x1="0" y1="${y}" x2="${W}" y2="${y}" stroke="var(--chart-grid)" stroke-width="1"/>`;
    }
    // x 軸時間標籤（~4 個）
    const labelCount = Math.min(4, n);
    for (let l = 0; l < labelCount; l++) {
      const i = Math.round((l / (labelCount - 1)) * (n - 1));
      const x = X(i);
      const anchor = l === 0 ? "start" : l === labelCount - 1 ? "end" : "middle";
      svgInner += `<text x="${x}" y="${H - 6}" fill="var(--text-3)" font-size="10" font-family="JetBrains Mono,monospace" text-anchor="${anchor}">${seriesList[0].times[i]}</text>`;
    }
    // 每條 series：area + line
    seriesList.forEach((s, si) => {
      const line = s.points.map((v, i) => `${i === 0 ? "M" : "L"} ${X(i).toFixed(1)} ${Y(v).toFixed(1)}`).join(" ");
      if (si === 0) {
        const area = line + ` L ${W} ${plotH} L 0 ${plotH} Z`;
        const gid = "g" + def.metrics.join("") + Math.random().toString(36).slice(2, 6);
        svgInner += `<defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="${s.color}" stop-opacity="0.22"/><stop offset="100%" stop-color="${s.color}" stop-opacity="0"/></linearGradient></defs>`;
        svgInner += `<path d="${area}" fill="url(#${gid})"/>`;
      }
      svgInner += `<path d="${line}" fill="none" stroke="${s.color}" stroke-width="1.8" stroke-linejoin="round"/>`;
      svgInner += `<circle class="hdot hdot-${si}" r="3.5" fill="${s.color}" stroke="var(--surface)" stroke-width="1.5" style="opacity:0"/>`;
    });
    svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
    svg.setAttribute("preserveAspectRatio", "none");
    svg.innerHTML = svgInner;
    card.__drawn = true;

    // hover
    const cross = wrap.querySelector(".crosshair");
    const tip = wrap.querySelector(".tip");
    const dots = [...svg.querySelectorAll(".hdot")];
    const move = (e) => {
      const r = wrap.getBoundingClientRect();
      const px = Math.max(0, Math.min(r.width, e.clientX - r.left));
      const i = Math.round((px / r.width) * (n - 1));
      const xPx = (i / (n - 1)) * r.width;
      cross.style.left = xPx + "px"; cross.style.opacity = "1";
      dots.forEach((dot, si) => {
        const v = seriesList[si].points[i];
        // viewBox 寬 W 映射到實際 r.width，但 circle 用 viewBox 座標
        dot.setAttribute("cx", X(i)); dot.setAttribute("cy", Y(v)); dot.style.opacity = "1";
      });
      const rows = seriesList.map((s, si) =>
        `<div style="display:flex;align-items:center;gap:6px;"><span style="width:8px;height:8px;border-radius:2px;background:${s.color};"></span><span class="tv" style="color:${s.color};">${fmt(s.points[i], s.unit)}</span>${def.legend?`<span class="tt">${def.legend[si]}</span>`:""}</div>`).join("");
      tip.innerHTML = `<div class="tt" style="margin-bottom:3px;">${seriesList[0].times[i]}</div>${rows}`;
      tip.style.opacity = "1";
      const tw = tip.offsetWidth || 90;
      tip.style.left = Math.max(0, Math.min(r.width - tw, xPx - tw / 2)) + "px";
      tip.style.top = "2px";
    };
    const leave = () => { cross.style.opacity = "0"; tip.style.opacity = "0"; dots.forEach((d) => (d.style.opacity = "0")); };
    wrap.onmousemove = move;
    wrap.onmouseleave = leave;
  }

  /* ============================================================
     容器 / 服務
     ============================================================ */
  function renderContainers() {
    const m = MACHINE_META[state.machine];
    const wrap = $("#containers");
    if (m.status === "offline") { wrap.innerHTML = ""; return; }
    const svc = SERVICES.filter((s) => s.machine === state.machine);
    const SBADGE = { running: ["b-ok", "running"], restarting: ["b-warn", "restarting"], stopped: ["b-err", "stopped"] };
    wrap.innerHTML = `
      <div style="font-size:11px;text-transform:uppercase;letter-spacing:.06em;color:var(--text-3);margin-bottom:10px;">服務與容器 (${svc.length})</div>
      <div class="card" style="overflow:hidden;">
        ${svc.map((s, i) => {
          const [cls, lbl] = SBADGE[s.status] || SBADGE.running;
          const sw = s.software.map((id) => installById[id]).filter(Boolean);
          const behind = sw.filter((w) => w.status === "behind").length;
          const cpu = s.cpu != null
            ? `<div style="display:flex;align-items:center;gap:7px;width:150px;"><span style="font-size:10.5px;color:var(--text-3);width:26px;">CPU</span><span style="flex:1;height:5px;border-radius:999px;background:var(--surface-3);overflow:hidden;"><i style="display:block;height:100%;width:${Math.min(100,s.cpu)}%;background:${barColor(s.cpu)};border-radius:999px;"></i></span><span class="mono" style="font-size:10.5px;color:var(--text-2);width:34px;text-align:right;">${s.cpu}%</span></div>`
            : `<span style="font-size:11px;color:var(--text-3);">系統層</span>`;
          return `<div style="display:flex;align-items:center;gap:12px;padding:12px 15px;${i?"border-top:1px solid var(--border);":""}">
            <span class="sdot ${s.status==="running"?"s-online":s.status==="restarting"?"s-warn":"s-offline"}"></span>
            <div style="min-width:0;flex:1;">
              <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;"><span style="font-size:13px;font-weight:550;">${s.name}</span><span class="tag">${s.kind}</span>${s.port?`<span class="tag mono">:${s.port}</span>`:""}${behind?`<span class="badge b-warn">${behind} 軟體可更新</span>`:""}</div>
              <div style="font-size:10.5px;color:var(--text-3);margin-top:3px;">${sw.map((w)=>`${w.software} ${w.current_version}`).join(" · ")}</div>
            </div>
            ${cpu}
            <span class="badge ${cls}">${lbl}</span>
          </div>`;
        }).join("")}
      </div>`;
  }

  /* ============================================================
     離線狀態
     ============================================================ */
  function renderOffline() {
    const m = MACHINE_META[state.machine];
    const pending = m.status === "pending";
    const off = $("#offline-state");
    $("#stat-cards").innerHTML = ""; $("#charts").innerHTML = ""; $("#containers").innerHTML = "";
    off.style.display = "flex";
    off.innerHTML = pending
      ? `<div style="display:grid;place-items:center;width:60px;height:60px;border-radius:16px;background:var(--accent-weak);border:1px solid var(--accent);color:var(--accent);">
          <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2v4M12 18v4M4.9 4.9l2.8 2.8M16.3 16.3l2.8 2.8M2 12h4M18 12h4M4.9 19.1l2.8-2.8M16.3 7.7l2.8-2.8"/></svg>
        </div>
        <div class="font-display" style="font-size:18px;font-weight:600;">${m.label} · 等待 agent 連線</div>
        <div style="font-size:13px;color:var(--text-2);max-width:460px;line-height:1.6;">這台機器已新增，但尚未收到 agent 回報。在目標機器執行安裝指令後，狀態會自動轉為線上並開始顯示數據。</div>
        <a href="manage.html" class="btn btn-xs" style="margin-top:4px;">前往管理頁取得安裝指令 →</a>`
      : `<div style="display:grid;place-items:center;width:60px;height:60px;border-radius:16px;background:var(--err-bg);border:1px solid var(--err-bd);color:var(--err);">
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><path d="M12 9v4M12 17h.01"/></svg>
        </div>
        <div class="font-display" style="font-size:18px;font-weight:600;">${m.label} 目前離線</div>
        <div style="font-size:13px;color:var(--text-2);max-width:420px;line-height:1.6;">agent 最後回報於 ${m.last_seen}。${(m.warnings||[]).join("、")}。無法取得即時與歷史數據；恢復連線後將自動補回。</div>`;
  }

  /* ============================================================
     render 主流程
     ============================================================ */
  function render() {
    renderSwitcher(); renderRangeTabs(); renderHead();
    const st = MACHINE_META[state.machine].status;
    const noData = st === "offline" || st === "pending";
    $("#offline-state").style.display = noData ? "flex" : "none";
    if (noData) { renderOffline(); return; }
    renderStats(); renderCharts(); renderContainers();
  }

  let rt;
  window.addEventListener("resize", () => { clearTimeout(rt); rt = setTimeout(renderCharts, 120); });

  initTheme();
  render();
  window.addEventListener("trends:refresh", () => render());
})();
