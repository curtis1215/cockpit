/* =============================================================
   cockpit · 版本追蹤器 — mock 資料
   -------------------------------------------------------------
   ⚠️ 之後接 FastAPI 後端時，這整個檔案會被真實 API 取代：
     · installs  ← GET /api/installs          (主清單)
     · versions  ← GET /api/changelog/:sw/:v  (changelog modal)
     · jobs      ← GET /api/jobs  + SSE        (更新 job 面板)
   前端不應假設這些資料是靜態的；render 函式皆吃陣列/物件。
   ============================================================= */

/* ---- 機器清單（篩選下拉用）---- */
const MACHINES = ["mac", "macmini", "ubuntu_llm", "gcp", "vps"];

/* ---- installs：主清單。一列 = 一個「軟體 × 機器」安裝 ---- */
const INSTALLS = [
  { id: "i01", software: "claude-code", kind: "npm", machine: "mac",
    current_version: "2.1.98", latest_version: "2.1.101", status: "behind", behind_count: 3 },
  { id: "i02", software: "claude-code", kind: "npm", machine: "ubuntu_llm",
    current_version: "2.1.101", latest_version: "2.1.101", status: "up_to_date", behind_count: 0 },
  { id: "i03", software: "super-telegram", kind: "claude-plugin", machine: "mac",
    current_version: "1.3.1", latest_version: "1.4.0", status: "behind", behind_count: 1 },
  { id: "i04", software: "multica", kind: "custom", machine: "macmini",
    current_version: "0.8.2", latest_version: "0.9.0", status: "behind", behind_count: 1,
    update_kind: "agent" },                                  // ← 委派 agent 多步更新
  { id: "i05", software: "ollama", kind: "github", machine: "ubuntu_llm",
    current_version: "0.5.4", latest_version: "0.5.4", status: "up_to_date", behind_count: 0 },
  { id: "i06", software: "vllm", kind: "pypi", machine: "ubuntu_llm",
    current_version: "0.6.3", latest_version: "0.6.6", status: "behind", behind_count: 2,
    update_kind: "agent" },                                  // ← 重 build 鏡像 + 重啟服務
  { id: "i07", software: "caddy", kind: "github", machine: "vps",
    current_version: "2.8.4", latest_version: "2.9.1", status: "behind", behind_count: 5 },
  { id: "i08", software: "uv", kind: "pypi", machine: "mac",
    current_version: "0.5.11", latest_version: "0.5.11", status: "up_to_date", behind_count: 0 },
  { id: "i09", software: "open-webui", kind: "github", machine: "ubuntu_llm",
    current_version: "0.4.8", latest_version: null, status: "unknown", behind_count: 0 },
  { id: "i10", software: "tailscale", kind: "brew", machine: "mac",
    current_version: "1.78.1", latest_version: "1.80.2", status: "behind", behind_count: 4 },
  { id: "i11", software: "comfyui", kind: "custom", machine: "gcp",
    current_version: "0.3.10", latest_version: null, status: "error", behind_count: 0,
    error: "ssh: connect to host gcp-a100 port 22: Operation timed out" },
  { id: "i12", software: "node", kind: "brew", machine: "macmini",
    current_version: "22.11.0", latest_version: "22.13.1", status: "behind", behind_count: 2 },
  { id: "i13", software: "ripgrep", kind: "brew", machine: "mac",
    current_version: "14.1.1", latest_version: "14.1.1", status: "up_to_date", behind_count: 0 },
  { id: "i14", software: "postgres", kind: "brew", machine: "vps",
    current_version: "16.4", latest_version: "17.2", status: "behind", behind_count: 1 },
  { id: "i15", software: "super-telegram", kind: "claude-plugin", machine: "macmini",
    current_version: "1.4.0", latest_version: "1.4.0", status: "up_to_date", behind_count: 0 },
];

/* ---- versions：changelog（modal 用）。key = `${software}@${version}` ---- */
const VERSIONS = {
  "claude-code@2.1.101": {
    software: "claude-code", version: "2.1.101", released_at: "2026-04-10",
    changelog_zh:
`- **新增** \`/team-onboarding\` 指令：一鍵為新成員建立工作區與權限範本
- **新增** 子代理（subagent）可在背景平行執行，主對話不再阻塞
- **修正** 長時間擴展工作階段的記憶體洩漏（>2h session 記憶體成長已收斂）
- **安全性** 修補 \`which\` fallback 的指令注入漏洞（CVE-2026-2871）
- **效能** 大型 repo 的檔案索引快取命中率提升約 40%`,
    changelog_raw:
`## 2.1.101
### Added
- Added /team-onboarding command to scaffold workspaces & permission presets
- Subagents can now run in the background in parallel
### Fixed
- Fixed memory leak in long-running extended sessions
- Security: patched command injection in \`which\` fallback (CVE-2026-2871)
### Performance
- ~40% better cache hit rate for large repo file indexing`,
  },
  "super-telegram@1.4.0": {
    software: "super-telegram", version: "1.4.0", released_at: "2026-03-28",
    changelog_zh:
`- **新增** 內聯指令選單，於聊天視窗直接觸發 plugin
- **新增** 支援轉發訊息批次摘要
- **變更** 設定檔格式由 \`.json\` 改為 \`.toml\`（首次啟動會自動遷移）
- **修正** webhook 在 IPv6-only 主機上無法綁定的問題`,
    changelog_raw:
`## 1.4.0
- Added inline command menu
- Added batch summarization for forwarded messages
- BREAKING: config moved from .json to .toml (auto-migrated on first run)
- Fixed webhook bind failure on IPv6-only hosts`,
  },
  "multica@0.9.0": {
    software: "multica", version: "0.9.0", released_at: "2026-05-30",
    changelog_zh:
`- **新增** 多租戶隔離：每個 workspace 獨立 sqlite + 卷
- **變更** 基底鏡像升級到 \`node:22-slim\`，鏡像體積 −180MB
- **變更** 啟動參數 \`--legacy-router\` 已移除
- **修正** 在 ARM 主機上 sharp 編譯失敗`,
    changelog_raw:
`## 0.9.0
- Added multi-tenant isolation (per-workspace sqlite + volume)
- Base image -> node:22-slim (-180MB)
- BREAKING: removed --legacy-router flag
- Fixed sharp build failure on ARM hosts`,
  },
  "vllm@0.6.6": {
    software: "vllm", version: "0.6.6", released_at: "2026-05-22",
    changelog_zh:
`- **新增** 支援 FP8 KV cache，長 context 顯存佔用下降
- **新增** \`--enable-chunked-prefill\` 預設開啟
- **修正** 多卡 tensor-parallel 偶發 NCCL 逾時
- **相容性** 需重新編譯 CUDA kernel（建議重 build 鏡像）`,
    changelog_raw:
`## 0.6.6
- Added FP8 KV cache support
- --enable-chunked-prefill now on by default
- Fixed sporadic NCCL timeout under tensor parallel
- Note: requires CUDA kernel rebuild`,
  },
  "caddy@2.9.1": {
    software: "caddy", version: "2.9.1", released_at: "2026-04-02",
    changelog_zh:
`- **新增** 內建 ACME 支援 DNS-01 多 provider 並行
- **修正** HTTP/3 在高併發下的連線重置
- **效能** reverse_proxy 連線池重用率提升`,
    changelog_raw:
`## 2.9.1
- Added concurrent multi-provider DNS-01 ACME
- Fixed HTTP/3 connection resets under high concurrency
- Improved reverse_proxy connection pool reuse`,
  },
  "tailscale@1.80.2": {
    software: "tailscale", version: "1.80.2", released_at: "2026-05-12",
    changelog_zh:
`- **新增** Tailnet lock 支援離線金鑰簽署
- **修正** macOS 上 exit node 切換後 DNS 未更新
- **修正** 部分 NAT 環境下 DERP 回退過於積極`,
    changelog_raw:
`## 1.80.2
- Added offline key signing for Tailnet lock
- Fixed DNS not updating after exit node switch on macOS
- Fixed overly aggressive DERP fallback on some NATs`,
  },
  "node@22.13.1": {
    software: "node", version: "22.13.1", released_at: "2026-05-18",
    changelog_zh:
`- **安全性** 修補 \`node:http\` 標頭走私（HIGH）
- **變更** \`--experimental-strip-types\` 升為穩定
- **修正** Windows ARM64 的 fs.watch 事件遺漏`,
    changelog_raw:
`## 22.13.1 (LTS)
- Security: fix header smuggling in node:http (HIGH)
- --experimental-strip-types is now stable
- Fixed missed fs.watch events on Windows ARM64`,
  },
  "postgres@17.2": {
    software: "postgres", version: "17.2", released_at: "2026-02-14",
    changelog_zh:
`- **新增** 增量備份（incremental \`pg_basebackup\`）
- **效能** 平行 \`VACUUM\` 與更佳的 query plan 記憶體管理
- **注意** major 升級（16→17）需 \`pg_upgrade\`，請先快照`,
    changelog_raw:
`## 17.2
- Added incremental pg_basebackup
- Parallel VACUUM and better query plan memory mgmt
- NOTE: major upgrade (16->17) requires pg_upgrade`,
  },
};

/* ---- jobs：更新工作（job 面板用）。最新的在前 ---- */
/* status: queued | running | success | failed                       */
/* kind:   command（單一指令）| agent（委派 AI agent 多步任務）        */
const JOBS = [
  {
    id: "job_03", software: "claude-code", machine: "mac", kind: "command",
    status: "success", new_version: "2.1.101",
    started_at: "2026-06-03T09:41:00", finished_at: "2026-06-03T09:41:22",
    log: [
      "▶ npm i -g @anthropic-ai/claude-code@latest",
      "npm warn deprecated har-validator@5.1.5",
      "added 1 package in 11s",
      "✓ claude-code 2.1.98 → 2.1.101",
    ],
  },
  {
    id: "job_02", software: "caddy", machine: "vps", kind: "command",
    status: "failed",
    started_at: "2026-06-03T08:12:00", finished_at: "2026-06-03T08:12:09",
    log: [
      "▶ sudo systemctl stop caddy",
      "▶ curl -fsSL https://github.com/caddyserver/caddy/releases/download/v2.9.1/caddy_linux_amd64 -o /usr/local/bin/caddy",
      "  % Total    % Received  Time    Speed",
      "  100  48.2M  100  48.2M   0:00:07  6.1M",
      "▶ caddy validate --config /etc/caddy/Caddyfile",
      "✗ Error: adapting config: /etc/caddy/Caddyfile:14 — unknown directive 'handle_path'",
      "✗ 更新中止，已還原舊版二進位",
    ],
  },
];

/* ---- 預先寫好的 streaming 腳本（prototype 用計時器逐行 append）----
   真實後端：開 EventSource(`/api/jobs/${id}/stream`)，onmessage 直接 append。
   下方腳本依 software 提供「執行中會吐出的 log 序列」與最終結果。          */
const JOB_SCRIPTS = {
  multica: {
    kind: "agent", runner: "codex exec", new_version: "0.9.0",
    prompt:
`multica 上游有新版 0.9.0（目前 0.8.2）。請依序：
1. 同步上游：git fetch upstream && git merge upstream/main
2. 解決可能的 lockfile 衝突，跑 pnpm install
3. 依新版 Dockerfile 重 build 鏡像 multica:0.9.0
4. 以 docker compose 滾動重啟，healthcheck 通過才算成功
5. 失敗則自動 rollback 到 0.8.2 並回報原因`,
    lines: [
      { t: 400, s: "▶ 啟動 codex exec @ /srv/multica" },
      { t: 700, s: "  model=gpt-5-codex  sandbox=workspace-write  approval=never" },
      { t: 900, s: "→ git fetch upstream && git merge upstream/main" },
      { t: 1200, s: "  Updating 4f2a1c9..b7e90a2, Fast-forward" },
      { t: 900, s: "  14 files changed, 612 insertions(+), 88 deletions(-)" },
      { t: 1100, s: "→ pnpm install" },
      { t: 1400, s: "  Lockfile up to date, resolving 1 new dependency…" },
      { t: 1000, s: "  + sharp 0.34.1 (linux-arm64)" },
      { t: 1200, s: "→ docker build -t multica:0.9.0 ." },
      { t: 1300, s: "  Step 5/14 : COPY package.json pnpm-lock.yaml ./" },
      { t: 1500, s: "  Step 7/14 : RUN pnpm install --prod --frozen-lockfile" },
      { t: 1600, s: "  Step 11/14 : RUN node ./scripts/build.mjs" },
      { t: 1300, s: "  Step 14/14 : CMD [\"node\",\"server.js\"]" },
      { t: 900, s: "  → exported image sha256:9c1a…e4  (412MB)" },
      { t: 1100, s: "→ docker compose up -d --no-deps multica" },
      { t: 1200, s: "  Recreating multica … done" },
      { t: 1400, s: "→ 等待 healthcheck (GET /healthz)…" },
      { t: 1500, s: "  healthz 503 … retry 1/5" },
      { t: 1400, s: "  healthz 200 ✓  (uptime 6s)" },
      { t: 800, s: "✓ multica 0.8.2 → 0.9.0 部署完成，舊容器已清除" },
    ],
    result: "success",
  },
  vllm: {
    kind: "agent", runner: "claude -p", new_version: "0.6.6",
    prompt:
`vllm 0.6.6 需要重新編譯 CUDA kernel。請：
1. 進到 ubuntu_llm，停掉 vllm 服務
2. pip install vllm==0.6.6，重 build 自訂鏡像
3. 用 1 張 A6000 跑 smoke test（載入 8B 模型 + 一次推論）
4. 通過才滾動重啟，否則 rollback`,
    lines: [
      { t: 400, s: "▶ 啟動 claude -p @ ubuntu_llm:/opt/vllm" },
      { t: 800, s: "→ systemctl stop vllm.service" },
      { t: 1000, s: "→ pip install vllm==0.6.6" },
      { t: 1500, s: "  Building wheels for vllm (CUDA 12.4)… this can take a while" },
      { t: 1800, s: "  [1/3] nvcc -gencode arch=compute_89,code=sm_89 …" },
      { t: 1700, s: "  [2/3] compiling fp8 kv-cache kernels …" },
      { t: 1600, s: "  [3/3] linking _C.cpython-312-x86_64-linux-gnu.so" },
      { t: 1200, s: "→ smoke test: load meta-llama/Llama-3.1-8B" },
      { t: 1500, s: "  KV cache: FP8 enabled, 38.4 GiB free" },
      { t: 1300, s: "  prompt='ping' → 'pong' (142 tok/s) ✓" },
      { t: 1000, s: "→ systemctl start vllm.service" },
      { t: 1200, s: "✓ vllm 0.6.3 → 0.6.6，服務已恢復" },
    ],
    result: "success",
  },
  /* 通用單一指令型（npm / brew / github binary）---------------- */
  _command: {
    kind: "command",
    lines: [
      { t: 500, s: "▶ 連線到目標機器…" },
      { t: 900, s: "→ 執行更新指令" },
      { t: 1400, s: "  下載中… ████████░░ 80%" },
      { t: 1100, s: "  套用變更…" },
      { t: 900, s: "✓ 完成" },
    ],
    result: "success",
  },
};

window.MOCK = { MACHINES, INSTALLS, VERSIONS, JOBS, JOB_SCRIPTS };
