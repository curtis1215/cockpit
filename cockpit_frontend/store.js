/* =============================================================
   cockpit · 共用狀態 (store)
   -------------------------------------------------------------
   讓「更新 / 機器增刪命名 / 軟體管理」在任一頁面執行後，能跨頁、
   跨重新整理保留 —— 以 localStorage 記錄「相對於原始 mock 的差異」，
   各頁載入時重播套用到 window.MOCK / window.TOPO。

   載入順序（重要）：
     · 有拓樸資料的頁：mock-data.js → topo-data.js → store.js → 該頁
     · 清單頁：               mock-data.js → store.js → app.js

   ⚠️ 接後端後整個移除：機器/軟體/版本皆由真實 API 提供。
     · 機器 CRUD   → POST/DELETE/PATCH /api/systems
     · 安裝 token  → POST /api/systems/:id/enroll-token
     · 軟體管理     → POST/PATCH/DELETE /api/installs
   ============================================================= */
(function () {
  const K_VER = "cockpit-version-overrides";   // { installId: {current_version,status,behind_count} }
  const K_MAC = "cockpit-machines";             // { renames:{id:label}, removed:[id], added:[{...}] }
  const K_SW  = "cockpit-software";             // { removed:[id], edits:{id:{...}}, added:[{...}] }
  const read = (k) => { try { return JSON.parse(localStorage.getItem(k)) || {}; } catch (e) { return {}; } };
  const write = (k, v) => { try { localStorage.setItem(k, JSON.stringify(v)); } catch (e) {} };

  const ver = read(K_VER);
  const mac = Object.assign({ renames: {}, removed: [], added: [] }, read(K_MAC));
  const sw  = Object.assign({ removed: [], edits: {}, added: [] }, read(K_SW));

  /* ---------- 套用到 INSTALLS（清單/拓樸/機器頁都需要）---------- */
  if (window.MOCK && Array.isArray(window.MOCK.INSTALLS)) {
    let list = window.MOCK.INSTALLS;
    // 移除：被刪的軟體 + 被刪機器上的軟體
    list = list.filter((i) => !sw.removed.includes(i.id) && !mac.removed.includes(i.machine));
    // 編輯
    list.forEach((i) => { if (sw.edits[i.id]) Object.assign(i, sw.edits[i.id]); });
    // 新增的軟體
    sw.added.forEach((a) => { if (!list.find((i) => i.id === a.id)) list.push(Object.assign({
      status: "unknown", current_version: "—", latest_version: null, behind_count: 0,
    }, a)); });
    // 版本 override（更新成功）
    list.forEach((i) => { if (ver[i.id]) Object.assign(i, ver[i.id]); });
    window.MOCK.INSTALLS.length = 0;
    window.MOCK.INSTALLS.push(...list);
  }

  /* ---------- 套用到 TOPO（機器層）---------- */
  if (window.TOPO) {
    const { MACHINE_META, MACHINE_ORDER, SERVICES } = window.TOPO;
    // 刪除機器
    mac.removed.forEach((id) => {
      delete MACHINE_META[id];
      const oi = MACHINE_ORDER.indexOf(id); if (oi >= 0) MACHINE_ORDER.splice(oi, 1);
      for (let i = SERVICES.length - 1; i >= 0; i--) if (SERVICES[i].machine === id) SERVICES.splice(i, 1);
    });
    if (window.MOCK && window.MOCK.MACHINES) {
      const mlist = window.MOCK.MACHINES.filter((m) => !mac.removed.includes(m));
      window.MOCK.MACHINES.length = 0; window.MOCK.MACHINES.push(...mlist);
    }
    // 重新命名
    Object.entries(mac.renames).forEach(([id, label]) => { if (MACHINE_META[id]) MACHINE_META[id].label = label; });
    // 新增機器（pending：尚未連線、無指標）
    mac.added.forEach((a) => {
      if (MACHINE_META[a.id]) return;
      MACHINE_META[a.id] = {
        label: a.label, role: "等待連線", os: a.os || "—", arch: a.arch || "—",
        status: "pending", cpu: null, mem: null, disk: null, gpu: null, net: null, load: null, temp: null,
        uptime: "—", agent: a.agent || "0.18.7", agent_status: "pending", last_seen: "尚未連線", spark: null,
        token: a.token, created: a.created, warnings: ["等待 agent 連線"],
      };
      MACHINE_ORDER.push(a.id);
      if (window.MOCK && window.MOCK.MACHINES && !window.MOCK.MACHINES.includes(a.id)) window.MOCK.MACHINES.push(a.id);
    });
    // 新增的軟體：盡量掛到該機器的「系統套件」bundle 服務，維持拓樸連線
    if (window.MOCK) sw.added.forEach((a) => {
      const bundle = SERVICES.find((s) => s.machine === a.machine && s.kind === "bundle")
        || SERVICES.find((s) => s.machine === a.machine);
      if (bundle && !bundle.software.includes(a.id)) bundle.software.push(a.id);
    });
  }

  /* ---------- 對外 API（manage.js 使用；同時改記憶體 + 寫 localStorage）---------- */
  function uid(p) { return p + Math.random().toString(36).slice(2, 8); }
  function token() { return "ck_agent_" + Array.from({ length: 32 }, () => "0123456789abcdef"[Math.floor(Math.random() * 16)]).join(""); }

  window.CockpitStore = {
    /* —— 版本更新 —— */
    applyUpdate(id, patch) { ver[id] = Object.assign(ver[id] || {}, patch); write(K_VER, ver);
      const it = window.MOCK?.INSTALLS.find((i) => i.id === id); if (it) Object.assign(it, patch); },

    /* —— 機器 —— */
    renameMachine(id, label) {
      mac.renames[id] = label; write(K_MAC, mac);
      if (window.TOPO?.MACHINE_META[id]) window.TOPO.MACHINE_META[id].label = label;
    },
    removeMachine(id) {
      if (!mac.removed.includes(id)) mac.removed.push(id);
      delete mac.renames[id];
      mac.added = mac.added.filter((a) => a.id !== id);
      write(K_MAC, mac);
      if (window.TOPO) {
        const { MACHINE_META, MACHINE_ORDER, SERVICES } = window.TOPO;
        delete MACHINE_META[id];
        const oi = MACHINE_ORDER.indexOf(id); if (oi >= 0) MACHINE_ORDER.splice(oi, 1);
        for (let i = SERVICES.length - 1; i >= 0; i--) if (SERVICES[i].machine === id) SERVICES.splice(i, 1);
      }
      if (window.MOCK) window.MOCK.INSTALLS = window.MOCK.INSTALLS.filter((i) => i.machine !== id);
    },
    addMachine({ label, os, arch }) {
      const id = uid("m_"); const tk = token(); const created = Date.now();
      const rec = { id, label, os, arch, token: tk, created, agent: "0.18.7" };
      mac.added.push(rec); write(K_MAC, mac);
      if (window.TOPO) {
        window.TOPO.MACHINE_META[id] = {
          label, role: "等待連線", os: os || "—", arch: arch || "—", status: "pending",
          cpu: null, mem: null, disk: null, gpu: null, net: null, load: null, temp: null,
          uptime: "—", agent: "0.18.7", agent_status: "pending", last_seen: "尚未連線", spark: null,
          token: tk, created, warnings: ["等待 agent 連線"],
        };
        window.TOPO.MACHINE_ORDER.push(id);
        if (window.MOCK && !window.MOCK.MACHINES.includes(id)) window.MOCK.MACHINES.push(id);
      }
      return rec;
    },

    /* —— 軟體 —— */
    addSoftware(obj) {
      const id = uid("sw_");
      const rec = Object.assign({ id, status: "unknown", current_version: "—", latest_version: null, behind_count: 0 }, obj);
      sw.added.push(rec); write(K_SW, sw);
      if (window.MOCK) window.MOCK.INSTALLS.push({ ...rec });
      if (window.TOPO) {
        const S = window.TOPO.SERVICES;
        const bundle = S.find((s) => s.machine === obj.machine && s.kind === "bundle") || S.find((s) => s.machine === obj.machine);
        if (bundle && !bundle.software.includes(id)) bundle.software.push(id);
      }
      return rec;
    },
    editSoftware(id, patch) {
      // 既有 mock 軟體 → 記在 edits；session 內新增的 → 直接改 added 紀錄
      const inAdded = sw.added.find((a) => a.id === id);
      if (inAdded) Object.assign(inAdded, patch);
      else sw.edits[id] = Object.assign(sw.edits[id] || {}, patch);
      write(K_SW, sw);
      const it = window.MOCK?.INSTALLS.find((i) => i.id === id); if (it) Object.assign(it, patch);
    },
    removeSoftware(id) {
      sw.added = sw.added.filter((a) => a.id !== id);
      if (!sw.removed.includes(id)) sw.removed.push(id);
      delete sw.edits[id]; write(K_SW, sw);
      if (window.MOCK) window.MOCK.INSTALLS = window.MOCK.INSTALLS.filter((i) => i.id !== id);
      if (window.TOPO) window.TOPO.SERVICES.forEach((s) => { s.software = s.software.filter((x) => x !== id); });
    },

    newToken: token,
    get(id) { return ver[id]; },
    reset() { [K_VER, K_MAC, K_SW].forEach((k) => { try { localStorage.removeItem(k); } catch (e) {} }); },
  };
})();
