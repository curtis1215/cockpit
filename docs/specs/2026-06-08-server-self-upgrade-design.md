# Web UI 觸發 Server 自我升級設計

日期：2026-06-08
狀態：已與使用者確認

## 目標

管理頁一鍵升級 cockpit server 到最新 GitHub release，免 SSH。重用既有 `internal/selfupdate`（agent 自升級與 `cockpit upgrade` 已在用）與服務管理器的自動重啟（launchd `KeepAlive` / systemd `Restart` + `RestartSec=5`）。

## 1. 後端

### `GET /api/version` 擴充

回應由 `{"version": "..."}` 擴充為：

```json
{"version": "0.2.1", "latest": "0.2.2", "update_available": true}
```

- `latest`：server 端呼叫既有 `selfupdate.Latest(hc, githubBase, "curtis1215/cockpit")` 取得（去掉 `v` 前綴）
- **記憶體快取 1 小時**（time-based；首次請求 lazy 查），避免每次頁載打 GitHub、避免瀏覽器端 rate limit
- 查詢失敗（網路/GitHub 掛）→ `latest: ""`、`update_available: false`，HTTP 仍 200，不噴錯
- `update_available` = `latest != "" && latest != version`
- dev build（`version` 為空或 `"0.0.0-dev"`）一律 `update_available: false`、不查 GitHub

### `POST /api/server/upgrade` 新增

流程：

1. **互斥**：以 atomic flag 防併發；升級已在進行 → `409 {"error":"upgrade already in progress"}`
2. 呼叫 `selfupdate.Run(nil, githubBase, "curtis1215/cockpit", s.version, "")`（targetPath 空 = `os.Executable()`；Run 內部是下載→驗證→原子替換，失敗時舊 binary 不動）
3. 結果分支：
   - `(false, nil)`（已最新）→ `200 {"status":"up_to_date"}`，釋放鎖
   - `(false, err)` → `500 {"error": "<err>"}`，釋放鎖；binary 不可寫的錯誤原樣透出（訊息足以提示 chown）
   - `(true, nil)` → `AddEvent("upgrade", "", "server", "self-upgrade to <new>")` → `202 {"status":"restarting"}` → goroutine 延遲 1 秒 `os.Exit(0)` → 服務管理器以新 binary 拉起
4. dev build（`version` 為空或 `"0.0.0-dev"`）直接 `400 {"error":"dev build cannot self-upgrade"}`

### 可測試性 seam

Server struct 注入兩個函式欄位（建構時給預設值）：

- `upgradeFn func() (bool, error)` — 預設包 `selfupdate.Run(...)`
- `exitFn func()` — 預設 `os.Exit(0)`

測試替換為 stub，可完整覆蓋 200/202/400/409/500 分支而不真的升級或退出。

## 2. 前端（管理頁）

- header 的 server 版本（`#server-ver`）旁，`update_available` 時顯示按鈕：「↑ 升級 Server 到 v{latest}」
- 點擊流程：
  1. `confirm("升級會重啟 server（約 10–30 秒），確定？")` 二次確認
  2. `POST /api/server/upgrade`
  3. `202` → toast「升級中，server 重啟約 10–30 秒…」→ 每 3 秒輪詢 `GET /api/version`（fetch 失敗 = 重啟中，靜默重試）
  4. 輪詢到 `version` 改變 → toast「已升級到 v{new}」→ `loadAll()` 重載、按鈕消失
  5. 90 秒仍未恢復 → toast 警告「升級逾時，請手動檢查 server 狀態」
  - `200 up_to_date` → toast「已是最新版」；`409` → toast「升級已在進行」；`500` → toast 顯示錯誤訊息
- 其他頁面不加按鈕（管理頁是唯一管理入口）

## 3. 部署前提

- **前提**：serve 的 binary 必須是 serve 行程可寫。預設安裝（service 以 root 跑）天然滿足；只有**手動降權**過的安裝（如 mac-mini plist `UserName=curtis`）需要一次性 `sudo chown <user> /usr/local/bin/cockpit`
- 不改 `cockpit setup serve`（預設 root 情境 chown 是 no-op，YAGNI）；改以兩道提示兜底：
  1. upgrade 端點執行前 pre-check binary 可寫性，不可寫 → `500 {"error":"binary not writable by server process; run: sudo chown <user> <path>"}`（帶實際 user 與 path）
  2. `cockpit doctor` 的 serve 段加檢查：service user 對 binary 不可寫時印警告與 chown 指令
- mac-mini 一次性手動處理：`sudo chown curtis /usr/local/bin/cockpit`
- Linux systemd：unit 已有 `Restart` + `RestartSec=5`，無需變更

## 4. 邊緣情境

- **升級後新版起不來**：launchd/systemd 反覆重啟失敗 → 超出本功能範圍（selfupdate 原子替換已將風險降到「新版 binary 本身壞」）；前端 90 秒逾時提示兜底
- **同台機器的 agent**：獨立行程/服務，不受 serve 重啟影響
- **CF Access / 反向代理後的輪詢**：輪詢打同源 `/api/version`，與現有頁面行為一致
- **GitHub rate limit**：1 小時快取 + 失敗靜默降級（按鈕不顯示而已）

## 5. 測試

Go（TDD）：

- `GET /api/version`：含 `latest`/`update_available`；快取生效（stub Latest 計數呼叫次數）；查詢失敗降級
- `POST /api/server/upgrade`：202（upgradeFn 成功 → exitFn 被呼叫）、200 up_to_date、500 錯誤透出、409 併發互斥、400 dev build
- setup chown：邏輯單測（或以整合測試確認不回歸）

手動驗收（mac-mini）：

- [ ] chown 後，管理頁出現升級按鈕（需有新版可升時）
- [ ] 點擊 → 確認 → toast → server 重啟 → 輪詢偵測到新版本 → 成功 toast
- [ ] 已最新時按鈕不顯示

## 非目標（YAGNI）

- 自動升級排程
- 升級頻道（beta/stable）選擇
- 降版/回滾 UI
