/* =============================================================
   cockpit · api-data.js — 真實 API 轉接層
   -------------------------------------------------------------
   取代 mock-data.js / topo-data.js / store.js / trends-data.js。
   組出與 mock 同形的：
     · window.TOPO  = { MACHINE_META, MACHINE_ORDER, SERVICES }
     · window.MOCK  = { INSTALLS }          ← topo.js / trends.js 直接解構
     · window.TRENDS = { RANGES, series, fmt }
   資料就緒後動態注入 topo.js 或 trends.js，30s 自動重整。

   頁面偵測：依 location.pathname 判斷（含 topology / machine）。
   ============================================================= */

(async () => {
  /* ──────────────────────────────────────────────────────────
     工具函式
  ────────────────────────────────────────────────────────── */
  async function api(path) {
    const r = await fetch(path);
    if (!r.ok) { const e = new Error(`${path} → ${r.status}`); e.status = r.status; throw e; }
    return r.status === 204 ? null : r.json();
  }

  /** uptime 秒 → 人類可讀 */
  function fmtUptime(sec) {
    if (sec == null || sec < 0) return "—";
    if (sec < 90 * 60) return Math.round(sec / 60) + "m";
    if (sec < 48 * 3600) return (sec / 3600).toFixed(1) + "h";
    return (sec / 86400).toFixed(1) + "d";
  }

  /** last_seen UTC "YYYY-MM-DD HH:MM:SS" → 相對中文字串 */
  function fmtLastSeen(s) {
    if (!s) return null;
    try {
      const d = new Date(s.replace(" ", "T") + "Z");
      const diff = (Date.now() - d.getTime()) / 1000;
      if (isNaN(diff)) return s;
      if (diff < 30) return "剛剛";
      if (diff < 3600) return Math.round(diff / 60) + " 分鐘前";
      if (diff < 86400) return (diff / 3600).toFixed(1) + " 小時前";
      return (diff / 86400).toFixed(1) + " 天前";
    } catch (_) { return s; }
  }

  /** 依門檻計算 warnings */
  function computeWarnings(sys) {
    if (sys.status !== "warn") return [];
    const w = [];
    if (sys.mem  != null && sys.mem  >= 85) w.push("mem " + sys.mem + "%");
    if (sys.cpu  != null && sys.cpu  >= 85) w.push("cpu " + sys.cpu + "%");
    if (sys.disk != null && sys.disk >= 90) w.push("disk " + sys.disk + "%");
    if (sys.gpu  != null && sys.gpu  >= 90) w.push("gpu " + sys.gpu + "%");
    if (sys.temp != null && sys.temp >= 75) w.push("temp " + sys.temp + "°C");
    return w;
  }

  /* ──────────────────────────────────────────────────────────
     TRENDS 常數（原 trends-data.js；metricBase 表格複製於此）
  ────────────────────────────────────────────────────────── */
  const RANGES = {
    "1h":  { n: 60,  stepMin: 1,  label: "1 小時" },
    "12h": { n: 72,  stepMin: 10, label: "12 小時" },
    "24h": { n: 96,  stepMin: 15, label: "24 小時" },
    "7d":  { n: 168, stepMin: 60, label: "7 天" },
  };

  function metricBase(machineId, metric) {
    const m = (window.TOPO && window.TOPO.MACHINE_META[machineId]) || {};
    switch (metric) {
      case "cpu":     return { unit: "%",    pct: true,  color: "var(--accent)" };
      case "mem":     return { unit: "%",    pct: true,  color: "#a78bfa" };
      case "disk":    return { unit: "%",    pct: true,  color: "#34d399" };
      case "gpu":     return { unit: "%",    pct: true,  color: "#f472b6" };
      case "netUp":   return { unit: "MB/s", pct: false, color: "#38bdf8" };
      case "netDown": return { unit: "MB/s", pct: false, color: "#22d3ee" };
      case "load":    return { unit: "",     pct: false, color: "#fbbf24" };
      case "temp":    return { unit: "°C",   pct: false, color: "#fb923c" };
      default:        return { unit: "",     pct: true,  color: "var(--accent)" };
    }
  }

  /* ──────────────────────────────────────────────────────────
     Metrics cache（machine.html 用）
     cache[range] = 原始 API 陣列 [{t, cpu, mem, disk, gpu, net_up, net_down, load, temp}, …]
  ────────────────────────────────────────────────────────── */
  let _cacheId = null;  // 目前快取對應的機器 id
  const _cache = {};

  async function prefetchMetrics(machineId) {
    if (_cacheId === machineId && Object.keys(_cache).length === 4) return;
    _cacheId = machineId;
    await Promise.all(Object.keys(RANGES).map(async (range) => {
      try {
        const rows = await api(`/api/systems/${encodeURIComponent(machineId)}/metrics?range=${range}`);
        _cache[range] = Array.isArray(rows) ? rows : [];
      } catch (_) {
        _cache[range] = [];
      }
    }));
  }

  /** series(machineId, metric, range) — 同步（cache 預先填充） */
  function series(machineId, metric, range) {
    const rows = _cache[range];
    // 守門：cache 不存在 / 非陣列 / 空陣列 → null（讓空狀態提示觸發）
    if (!Array.isArray(rows) || rows.length === 0) return null;

    // API metric 欄位名稱映射
    const fieldMap = {
      cpu: "cpu", mem: "mem", disk: "disk", gpu: "gpu",
      netUp: "net_up", netDown: "net_down", load: "load", temp: "temp",
    };
    const field = fieldMap[metric] || metric;

    const points = rows.map((r) => r[field]);
    // 守門：points 長度為 0、或該 metric 全 null → null
    if (points.length === 0 || points.every((v) => v == null)) return null;

    // 以 null → 0 補缺值（圖表 filter(Boolean) 已有保護）
    const pts = points.map((v) => (v == null ? 0 : Number(v)));

    // 時間標籤
    const times = rows.map((r) => {
      const d = new Date(r.t * 1000);
      if (range === "7d") return `${d.getMonth() + 1}/${d.getDate()}`;
      return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
    });

    const cfg = metricBase(machineId, metric);
    const min = Math.min(...pts), max = Math.max(...pts);
    const avg = pts.reduce((a, b) => a + b, 0) / pts.length;
    return {
      metric, points: pts, times, min, max, avg,
      last: pts[pts.length - 1],
      unit: cfg.unit, pct: cfg.pct, color: cfg.color,
    };
  }

  const fmt = (v, unit) => (Math.abs(v) >= 100 ? Math.round(v) : v.toFixed(1)) + unit;

  /* ──────────────────────────────────────────────────────────
     loadAll — 主載入邏輯
  ────────────────────────────────────────────────────────── */
  const IS_TOPOLOGY = /topology/i.test(location.pathname)
    || (!(/machine/i.test(location.pathname)) && location.pathname.indexOf("machine") === -1
        && location.pathname.indexOf("topology") >= 0);
  // 簡單判斷：含 "machine" 字串就走機器頁
  const IS_MACHINE = /machine/i.test(location.pathname);

  
  async function fillVersion() {
    try {
      const v = await api("/api/version");
      const el = document.getElementById("server-ver");
      if (el && v && v.version) el.textContent = "v" + v.version;
    } catch (_) {}
  }
async function loadAll() {
    /* 1. 共用資料 */
    const [systems, services, vms, installs] = await Promise.all([
      api("/api/systems"),
      api("/api/services"),
      api("/api/vms"),
      api("/api/installs"),
    ]);

    /* ── MACHINE_META ── */
    const MACHINE_META = {};
    const MACHINE_ORDER = [];

    // 先依 label 排序 systems
    const sortedSystems = [...systems].sort((a, b) => a.label.localeCompare(b.label));

    sortedSystems.forEach((sys) => {
      const id = sys.id;
      MACHINE_ORDER.push(id);

      const netNull = sys.net_up == null && sys.net_down == null;
      MACHINE_META[id] = {
        label:        sys.label || id,
        role:         sys.role  || "",
        os:           sys.os    || "—",
        arch:         sys.arch  || "—",
        status:       sys.status || "offline",

        cpu:  sys.cpu  != null ? sys.cpu  : null,
        mem:  sys.mem  != null ? sys.mem  : null,
        disk: sys.disk != null ? sys.disk : null,
        gpu:  sys.gpu  != null ? sys.gpu  : null,
        temp: sys.temp != null ? sys.temp : null,

        net:  netNull ? null : { up: sys.net_up ?? null, down: sys.net_down ?? null },
        load: sys.load != null ? [sys.load] : null,

        uptime:       fmtUptime(sys.uptime),
        agent:        sys.agent_version || "—",
        agent_status: sys.agent_status  || "stale",
        last_seen:    fmtLastSeen(sys.last_seen),

        spark:    Array.isArray(sys.spark) && sys.spark.length ? sys.spark : null,
        warnings: computeWarnings(sys),
      };
    });

    /* ── VMs：linked → role 前綴；unlinked → pending 卡 ── */
    if (Array.isArray(vms)) {
      vms.forEach((vm) => {
        // 找 host 機器的 label
        const hostSys = systems.find((s) => s.id === vm.host_id);
        const hostLabel = hostSys ? hostSys.label : (vm.host_id || "未知主機");

        if (vm.linked && MACHINE_META[vm.id]) {
          // linked VM 已在 systems 裡，補充 role
          const existing = MACHINE_META[vm.id];
          const vmRole = existing.role ? `VM @ ${hostLabel}` : `VM @ ${hostLabel}`;
          MACHINE_META[vm.id] = { ...existing, role: vmRole };
        } else if (!vm.linked) {
          // unlinked VM → pending 機器卡
          const pendingId = "vm_" + (vm.uuid || vm.id);
          if (!MACHINE_META[pendingId]) {
            MACHINE_META[pendingId] = {
              label:        vm.name || pendingId,
              role:         "未連線 VM @ " + hostLabel,
              os:           vm.guest_os || "—",
              arch:         "—",
              status:       "pending",
              cpu: null, mem: null, disk: null, gpu: null, temp: null,
              net: null, load: null,
              uptime:       "—",
              agent:        "—",
              agent_status: "pending",
              last_seen:    null,
              spark:        null,
              warnings:     ["等待 agent 連線"],
            };
            MACHINE_ORDER.push(pendingId);
          }
        }
      });
    }

    /* ── INSTALLS（形狀與 mock-data.js 相同）── */
    const INSTALLS = Array.isArray(installs) ? installs.map((r) => ({
      id:              r.id,
      software:        r.software,
      kind:            r.kind,
      machine:         r.machine,
      current_version: r.current_version,
      latest_version:  r.latest_version  ?? null,
      status:          r.status,
      behind_count:    r.behind_count     || 0,
      update_kind:     r.update_kind      || undefined,
      error:           r.error            || undefined,
    })) : [];

    /* ── SERVICES ── */
    // 服務層：API 服務 + 每台機器的 software bundle
    const SERVICES = [];

    // API 服務清單 → 加工成 mock 形狀
    if (Array.isArray(services)) {
      services.forEach((svc) => {
        SERVICES.push({
          id:       "svc_" + svc.system_id + "_" + svc.name.replace(/[^a-z0-9_]/gi, "_"),
          name:     svc.name,
          machine:  svc.system_id,
          kind:     svc.kind  || "service",
          status:   svc.status || "stopped",
          cpu:      svc.cpu   != null ? svc.cpu  : null,
          mem:      svc.mem   != null ? svc.mem  : null,
          port:     svc.port  != null ? svc.port : null,
          software: Array.isArray(svc.software) ? svc.software : [],
          depends:  Array.isArray(svc.depends)  ? svc.depends  : [],
        });
      });
    }

    // 每台機器：若在 installs 有安裝，合成 bundle 服務
    // installs.machine 欄位對應 system label（依 API 契約）
    const systemLabelToId = {};
    Object.entries(MACHINE_META).forEach(([id, m]) => { systemLabelToId[m.label] = id; });

    MACHINE_ORDER.forEach((machineId) => {
      if (machineId.startsWith("vm_")) return; // pending VM 無 installs
      const meta = MACHINE_META[machineId];
      if (!meta) return;

      // installs 依 machine 欄位比對（可能是 label 或 id）
      const machInstalls = INSTALLS.filter((i) =>
        i.machine === machineId ||
        i.machine === meta.label ||
        systemLabelToId[i.machine] === machineId
      );

      if (machInstalls.length > 0) {
        // 確認此機器還沒有 bundle 服務（避免 API services 已帶的 bundle 重複）
        const hasBundleAlready = SERVICES.some(
          (s) => s.machine === machineId && s.kind === "bundle"
        );
        if (!hasBundleAlready) {
          SERVICES.push({
            id:       "bundle_" + machineId,
            name:     "軟體",
            machine:  machineId,
            kind:     "bundle",
            status:   "running",
            cpu:      null,
            mem:      null,
            port:     null,
            software: machInstalls.map((i) => i.id),
            depends:  [],
          });
        }
      }
    });

    /* ── 發佈 window.TOPO / window.MOCK ── */
    window.TOPO = { MACHINE_META, MACHINE_ORDER, SERVICES };
    window.MOCK = {
      INSTALLS,
      // topo.js 也需要 VERSIONS / JOB_SCRIPTS（詳情抽屜用）
      // 真實後端尚無這兩個端點；提供空殼以防止解構失敗
      VERSIONS: {},
      JOB_SCRIPTS: {},
    };

    /* ── machine.html：預先快取 4 個 range 的 metrics ── */
    if (IS_MACHINE) {
      // 決定初始機器（localStorage 或第一台線上）
      const firstOnline = MACHINE_ORDER.find(
        (id) => MACHINE_META[id] && MACHINE_META[id].status !== "offline" && MACHINE_META[id].status !== "pending"
      ) || MACHINE_ORDER[0];
      const savedId = localStorage.getItem("cockpit-machine");
      const initId  = (savedId && MACHINE_META[savedId]) ? savedId : firstOnline;
      if (initId) await prefetchMetrics(initId);
    }

    /* ── 發佈 window.TRENDS ── */
    window.TRENDS = {
      RANGES,
      series,
      fmt,
      // machine.html 切換機器或 range 時，需要重新預取
      prefetchMetrics,
    };
  }

  /* ──────────────────────────────────────────────────────────
     Bootstrap
  ────────────────────────────────────────────────────────── */
  function showLoadError() {
    document.body.insertAdjacentHTML("beforeend",
      `<div id="api-err" style="position:fixed;top:0;left:0;right:0;padding:12px 20px;background:var(--err-bg,#2a1313);color:var(--err,#f87171);border-bottom:1px solid var(--err-bd,#5a2424);font-size:13px;z-index:9999;text-align:center;">
        無法連線後端，請確認服務是否正常運作。
      </div>`
    );
  }

  function inject(scriptName) {
    const s = document.createElement("script");
    s.src = scriptName;
    document.body.appendChild(s);
  }

  try {
    await loadAll();
      fillVersion();
  } catch (err) {
    console.error("[api-data] loadAll failed:", err);
    showLoadError();
    return;
  }

  // 判斷注入哪個渲染腳本
  if (IS_MACHINE) {
    inject("trends.js");
  } else {
    inject("topo.js");
  }

  /* ──────────────────────────────────────────────────────────
     30 秒自動重整
  ────────────────────────────────────────────────────────── */
  setInterval(async () => {
    try {
      await loadAll();
      if (IS_MACHINE) {
        // trends.js 透過 event 重繪
        window.dispatchEvent(new Event("trends:refresh"));
      } else {
        // topo.js 透過 event 重繪
        window.dispatchEvent(new Event("topo:refresh"));
      }
    } catch (err) {
      console.warn("[api-data] refresh failed:", err);
    }
  }, 30000);
})();
