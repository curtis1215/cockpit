/* =============================================================
   cockpit · 機器頁 — 時序資料產生器（beszel 風格走勢）
   -------------------------------------------------------------
   prototype 用：以決定性 PRNG 依 (機器, 指標, 區間) 產生穩定的
   隨機漫步 + 日週期波形，最後一點對齊 MACHINE_META 的當前值。

   ⚠️ 接後端時整個移除，改為：
      GET /api/systems/:id/metrics?range=24h
      回傳 [{ t, cpu, mem, disk, gpu, net_up, net_down, load, temp }, …]
      （beszel agent 每 ~15s 上報，hub 做時間聚合）
   ============================================================= */
(function () {
  const META = window.TOPO.MACHINE_META;

  const RANGES = {
    "1h":  { n: 60,  stepMin: 1,  label: "1 小時" },
    "12h": { n: 72,  stepMin: 10, label: "12 小時" },
    "24h": { n: 96,  stepMin: 15, label: "24 小時" },
    "7d":  { n: 168, stepMin: 60, label: "7 天" },
  };

  // 每個指標的設定：基準取自 META、波動度、是否百分比
  function metricBase(machineId, metric) {
    const m = META[machineId];
    switch (metric) {
      case "cpu":    return { base: m.cpu,    vol: 14, unit: "%",    pct: true,  color: "var(--accent)" };
      case "mem":    return { base: m.mem,    vol: 6,  unit: "%",    pct: true,  color: "#a78bfa" };
      case "disk":   return { base: m.disk,   vol: 1.2,unit: "%",    pct: true,  color: "#34d399" };
      case "gpu":    return { base: m.gpu,    vol: 16, unit: "%",    pct: true,  color: "#f472b6" };
      case "netUp":  return { base: m.net?.up,   vol: m.net?.up*0.7,   unit: "MB/s", pct: false, color: "#38bdf8" };
      case "netDown":return { base: m.net?.down, vol: m.net?.down*0.7, unit: "MB/s", pct: false, color: "#22d3ee" };
      case "load":   return { base: m.load?.[0], vol: (m.load?.[0]||1)*0.5, unit: "",  pct: false, color: "#fbbf24" };
      case "temp":   return { base: m.temp,   vol: 4,  unit: "°C",   pct: false, color: "#fb923c" };
      default:       return { base: 0, vol: 1, unit: "", pct: true, color: "var(--accent)" };
    }
  }

  // 簡單字串雜湊 → 種子
  function hash(str) { let h = 2166136261; for (let i = 0; i < str.length; i++) { h ^= str.charCodeAt(i); h = Math.imul(h, 16777619); } return h >>> 0; }
  function mulberry32(a) { return function () { a |= 0; a = (a + 0x6D2B79F5) | 0; let t = Math.imul(a ^ (a >>> 15), 1 | a); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; }; }

  function series(machineId, metric, range) {
    const m = META[machineId];
    if (!m || m.status === "offline") return null;
    const cfg = metricBase(machineId, metric);
    if (cfg.base == null) return null;
    const { n, stepMin } = RANGES[range] || RANGES["24h"];
    const rnd = mulberry32(hash(machineId + ":" + metric + ":" + range));
    const pts = [];
    let v = cfg.base;
    // 先往前回推一個漫步，再反轉，使「最後一點 = 當前值」
    for (let i = 0; i < n; i++) {
      const daily = Math.sin((i / n) * Math.PI * (range === "7d" ? 7 : 2)) * cfg.vol * 0.5;
      const noise = (rnd() - 0.5) * cfg.vol;
      v = v - noise * 0.45 - daily * 0.04;
      if (cfg.pct) v = Math.max(2, Math.min(99, v));
      else v = Math.max(0, v);
      pts.push(v);
    }
    pts.reverse();
    pts[pts.length - 1] = cfg.base;     // 對齊當前值

    // 時間標籤
    const now = new Date("2026-06-03T10:00:00");
    const times = pts.map((_, i) => {
      const d = new Date(now.getTime() - (n - 1 - i) * stepMin * 60000);
      if (range === "7d") return `${d.getMonth() + 1}/${d.getDate()}`;
      return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
    });

    const min = Math.min(...pts), max = Math.max(...pts);
    const avg = pts.reduce((a, b) => a + b, 0) / pts.length;
    return { metric, points: pts, times, min, max, avg, last: pts[pts.length - 1], unit: cfg.unit, pct: cfg.pct, color: cfg.color };
  }

  window.TRENDS = { RANGES, series, fmt: (v, unit) => (Math.abs(v) >= 100 ? Math.round(v) : v.toFixed(1)) + unit };
})();
