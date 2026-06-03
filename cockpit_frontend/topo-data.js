/* =============================================================
   cockpit · 拓樸頁 — 監控 + 拓樸 mock 資料
   -------------------------------------------------------------
   靈感來自 beszel（henrygd/beszel）的主機監控：每台機器回報
   CPU / 記憶體 / 磁碟 / 網路 / 負載 / 溫度 / GPU + 容器狀態。
   這裡把「監控」與「版本追蹤」整合成三層拓樸：
       機器 (machine)  →  服務 (service)  →  軟體 (software)
   軟體層直接重用 mock-data.js 的 INSTALLS / VERSIONS。

   ⚠️ 接後端時對應（延續 api-contract.md）：
     · MACHINE_META ← GET /api/machines/metrics   (beszel agent 回報)
     · SERVICES     ← GET /api/services           (docker/podman + 程序)
     · 軟體層        ← GET /api/installs            (已定義)
   ============================================================= */

/* ---- 每台機器的監控資料（beszel 風格）----
   status: online | warn | offline
   cpu/mem/disk/gpu = 0~100 (%)；offline 時為 null
   net = {up,down} MB/s；spark = 近 24 點 CPU 歷史（畫 sparkline 用） */
const MACHINE_META = {
  mac: {
    label: "Mac Studio", role: "開發主機", os: "macOS 15.4", arch: "arm64",
    status: "online", cpu: 18, mem: 42, disk: 61, gpu: null,
    net: { up: 2.1, down: 8.4 }, load: [1.8, 2.1, 1.6], temp: 48,
    uptime: "12 天 4 時", agent: "0.18.7", agent_status: "ok",
    spark: [12,15,14,22,18,17,30,24,19,16,21,18,14,12,17,25,20,18,16,14,19,18,15,18],
  },
  macmini: {
    label: "Mac mini", role: "家用伺服器", os: "macOS 15.3", arch: "arm64",
    status: "online", cpu: 34, mem: 58, disk: 73, gpu: null,
    net: { up: 0.8, down: 3.2 }, load: [2.4, 2.2, 2.0], temp: 55,
    uptime: "47 天 9 時", agent: "0.18.7", agent_status: "ok",
    spark: [28,30,34,31,29,36,40,38,33,30,35,42,39,34,31,33,36,34,30,32,37,35,33,34],
  },
  ubuntu_llm: {
    label: "LLM 推論機", role: "GPU 推論 · A6000", os: "Ubuntu 24.04", arch: "amd64",
    status: "warn", cpu: 71, mem: 86, disk: 44, gpu: 92,
    net: { up: 14.3, down: 22.7 }, load: [6.1, 5.8, 4.9], temp: 74,
    uptime: "23 天 1 時", agent: "0.18.7", agent_status: "ok",
    spark: [55,60,68,72,70,75,82,88,84,79,71,69,73,80,86,90,88,83,77,71,74,79,85,71],
    warnings: ["記憶體使用 86%", "GPU 92% · 74°C"],
  },
  gcp: {
    label: "GCP A100", role: "雲端訓練 · 隨需", os: "Ubuntu 22.04", arch: "amd64",
    status: "offline", cpu: null, mem: null, disk: null, gpu: null,
    net: null, load: null, temp: null,
    uptime: "—", agent: "0.18.7", agent_status: "stale",
    last_seen: "3 小時前", spark: null,
    warnings: ["agent 失聯", "ssh 連線逾時"],
  },
  vps: {
    label: "Edge VPS", role: "邊緣 · 反向代理", os: "Debian 12", arch: "amd64",
    status: "online", cpu: 9, mem: 31, disk: 52, gpu: null,
    net: { up: 1.4, down: 1.1 }, load: [0.4, 0.5, 0.6], temp: 39,
    uptime: "88 天 12 時", agent: "0.18.5", agent_status: "behind",  // agent 自己也落後
    spark: [8,10,9,7,11,9,8,12,10,9,7,8,10,9,11,8,7,9,10,8,9,7,8,9],
  },
};

/* ---- 服務層：機器上跑的「執行單位」----
   kind:   docker | service | daemon | proxy | db | plugin | runtime | bundle
   status: running | restarting | stopped
   software: 對應 INSTALLS 的 id 陣列（一個服務可由多個軟體組成）
   depends: 依賴的其他服務 id（畫服務↔服務的次要連線）
   cpu/mem: 容器資源（%）；無容器則 null                                */
const SERVICES = [
  /* —— mac —— */
  { id: "svc_tg_mac",  name: "telegram-bot", machine: "mac", kind: "plugin",
    status: "running", cpu: 1.2, mem: 3, port: null, software: ["i03"], depends: ["svc_sys_mac"] },
  { id: "svc_tailscale_mac", name: "tailscaled", machine: "mac", kind: "daemon",
    status: "running", cpu: 0.4, mem: 1, port: null, software: ["i10"] },
  { id: "svc_sys_mac", name: "系統套件", machine: "mac", kind: "bundle",
    status: "running", cpu: null, mem: null, port: null, software: ["i01", "i08", "i13"] },

  /* —— macmini —— */
  { id: "svc_multica", name: "multica", machine: "macmini", kind: "docker",
    status: "running", cpu: 6.8, mem: 14, port: 8080, software: ["i04"], depends: ["svc_sys_mini"] },
  { id: "svc_tg_mini", name: "telegram-bot", machine: "macmini", kind: "plugin",
    status: "running", cpu: 0.9, mem: 2, port: null, software: ["i15"] },
  { id: "svc_sys_mini", name: "系統套件", machine: "macmini", kind: "bundle",
    status: "running", cpu: null, mem: null, port: null, software: ["i12"] },

  /* —— ubuntu_llm —— */
  { id: "svc_vllm", name: "vllm", machine: "ubuntu_llm", kind: "docker",
    status: "running", cpu: 64, mem: 71, port: 8000, software: ["i06"] },
  { id: "svc_ollama", name: "ollama", machine: "ubuntu_llm", kind: "service",
    status: "running", cpu: 22, mem: 18, port: 11434, software: ["i05"] },
  { id: "svc_webui", name: "open-webui", machine: "ubuntu_llm", kind: "docker",
    status: "running", cpu: 3.1, mem: 9, port: 3000, software: ["i09"], depends: ["svc_ollama"] },
  { id: "svc_sys_ubuntu", name: "系統套件", machine: "ubuntu_llm", kind: "bundle",
    status: "running", cpu: null, mem: null, port: null, software: ["i02"] },

  /* —— gcp（離線）—— */
  { id: "svc_comfy", name: "comfyui", machine: "gcp", kind: "docker",
    status: "stopped", cpu: null, mem: null, port: 8188, software: ["i11"] },

  /* —— vps —— */
  { id: "svc_caddy", name: "caddy", machine: "vps", kind: "proxy",
    status: "running", cpu: 0.6, mem: 2, port: 443, software: ["i07"], depends: ["svc_multica", "svc_webui"] },
  { id: "svc_postgres", name: "postgres", machine: "vps", kind: "db",
    status: "running", cpu: 1.8, mem: 11, port: 5432, software: ["i14"] },
];

/* ---- 機器固定排序（拓樸由上而下）---- */
const MACHINE_ORDER = ["mac", "macmini", "ubuntu_llm", "gcp", "vps"];

window.TOPO = { MACHINE_META, SERVICES, MACHINE_ORDER };
