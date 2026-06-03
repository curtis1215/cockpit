# cockpit

自架於 mac mini、經 Cloudflare Tunnel 私有存取的 homelab 控制台。

兩個子系統：

1. **設備監控** — 拓樸圖 + 設備狀態 + 服務狀態（Beszel + 自建拓樸圖）。
2. **軟體版本追蹤** — 跨機器追蹤版本、翻譯 changelog（繁中）、Telegram 確認後遠端更新。

## 文件

- 設計規格：[`docs/specs/`](docs/specs/)
- 實作計畫：[`docs/plans/`](docs/plans/)

目前進度：子系統 2（版本追蹤器）設計已核可，待產出實作計畫。

> ⚠️ 本 repo 含 homelab 基礎設施細節（機器 IP、Tailscale、Cloudflare 設定），維持 **private**。
