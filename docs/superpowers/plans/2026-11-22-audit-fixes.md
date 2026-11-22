# 盤點修正計畫（2026-11-22）

來源：程式碼盤點報告的 9 項發現。每項是獨立 task，依序實作，每個 task 只做一個 commit。

通用規則：
- 測試指令：`go vet ./... && go test ./...`
- 只 stage 自己改的檔案（`git add <paths>`）。工作目錄裡 `.gitignore`、`internal/translate/http.go`、`internal/translate/http_test.go` 有使用者尚未提交的變更，**絕對不要 stage 或修改這三個檔案**。
- commit message 用繁體中文 conventional commits，例如 `fix(store): ...`，結尾加上
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`
- 不新增相依套件、不加設定項，只做最小改動。

---

## Task 1：SQLite PRAGMA 套用到每一條連線

檔案：`internal/store/store.go`（`Open`）、`internal/store/store_test.go` 或新測試檔

問題：`db.Exec("PRAGMA busy_timeout=5000")` 只會套用到連線池的其中一條連線。其他連線的 busy_timeout 仍是 0，遇到鎖會直接回傳 `database is locked`。

做法：
- 改成 `sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")`（modernc.org/sqlite 支援 `_pragma` DSN 參數）。
- 刪除原本兩段 `db.Exec("PRAGMA journal_mode=WAL")`、`db.Exec("PRAGMA busy_timeout=5000")` 和相關註解；在 DSN 那行留一行註解說明原因（PRAGMA 是 per-connection）。

測試（先寫，必須在舊程式上失敗）：開啟 store 後，用 `s.db.Conn(ctx)` 同時取得 3 條不同連線（全部持有、先不要 Close），每條都執行 `PRAGMA busy_timeout`，斷言結果都是 5000。

## Task 2：agentSystem 讀 inventory 要加鎖

檔案：`internal/server/monitor_api.go:42`

做法：`inventory.MachineForToken(s.inv, tok)` 改成 `inventory.MachineForToken(s.getInv(), tok)`。這是一行修正，不需要新測試；跑 `go test -race ./internal/server/` 確認沒有 race（如果環境沒有 cgo、無法跑 -race，就在 summary 註明）。

## Task 3：agent 的 job log 改成批次送出

檔案：`internal/agent/agent.go`（`RunJob`）、`internal/agent/vt_test.go`

問題：`post` 每一行同步送一次 HTTP，送出期間沒人讀 stdout，大量輸出時會拖慢指令本身。

做法（server 端不改，API 本來就收 `{"lines": [...]}`）：
- 在 agent 套件新增一個小型 log batcher（同一個檔案即可）：
  - `add(line)`：加進緩衝；緩衝達 50 行時立刻 flush。
  - 背景 ticker 每 1 秒 flush 一次，job 結束時停止。
  - `flush()`：用 mutex 序列化，確保批次順序不會亂。一次把整個緩衝 POST 到 `/api/agent/jobs/{id}/log`。POST 失敗時，把這批放回緩衝最前面，下次再送。緩衝上限 5000 行，超過就丟掉最舊的（加 `shortcut:` 註解說明這個上限）。
- `RunJob` 把 `executor.Run` 的 onLine 換成 `batcher.add`。中止訊息「■ 已由使用者中止」也走 `add`。**每次 `reportResult` 之前都要先 `flush()`**，確保 log 早於結果送達。

測試：用 httptest server 記錄收到的 `/log` 請求。執行一個輸出 120 行的指令（`for i in $(seq 1 120); do echo line$i; done`），斷言：
- 收到的行數總共 120 行、順序正確；
- `/log` 請求數 ≤ 5；
- 所有 `/log` 請求都早於 `/result` 請求。

既有的 `TestRunJobOnce`、`TestRunJobFailure` 要繼續通過。

## Task 4：`/api/jobs` 列表只回傳每個 job 最後 200 行 log

檔案：`internal/server/version_api.go`（`handleJobs`）、對應測試

問題：列表會回傳 50 個 job 的完整 log，log 很長時首頁會很慢。

做法：只在 `handleJobs`（列表）裡，把每筆的 `log` 截成最後 200 行（保留結尾換行的格式）。`/api/jobs/{id}` 單筆查詢和 SSE 串流都不改。新增一個小函式 `tailLines(s string, n int) string`。

測試：建立一個有 300 行 log 的 job，GET `/api/jobs`，斷言 log 剛好 200 行，第一行是 `line101`，最後一行是 `line300`。另外測一個少於 200 行的 job，log 要完全不變。

## Task 5：`TakeCheckRequested` / `TakeUpgradeRequested` 改成原子操作

檔案：`internal/store/store.go:730-753`

問題：先 SELECT 再 UPDATE。如果在兩步之間有人設了新請求，這個請求會被吃掉。

做法：兩個函式都改成一條 SQL：`UPDATE machine_state SET check_requested=0, updated_at=datetime('now') WHERE machine=? AND check_requested=1`，再用 `RowsAffected() == 1` 判斷是否取到請求；upgrade 版本照做。

測試：Set → Take 回傳 true → 再 Take 回傳 false；沒有任何紀錄的機器 Take 回傳 false。兩個函式都要測。

## Task 6：降採樣不重算已被部分 prune 的舊桶

檔案：`internal/store/monitor.go`（`Downsample` 和上面的註解）、`internal/store/monitor_test.go`

問題：條件只有 `ts < now`，每 5 分鐘就重算全部歷史。來源資料被 prune 掉一部分以後，最舊的那個桶會只用剩下的資料重算平均，覆蓋原本正確的值。

做法：
- 每個層級算出來源資料的保留期 `srcRetain`：來源是 `"1m"` 時用 `retain1m`，否則用 `dsLevels` 中 `Dst == Src` 那一層的 `RetainSec`。
- 在 SQL 加上下限 `AND ts >= ?`，參數為 `lb = ((now - srcRetain)/BucketSec + 1) * BucketSec`；這個時間點之前的桶可能已不完整，不再重算。
- 保留 `ts < now`（最新的部分桶仍會持續更新，趨勢圖需要），並把註解改成符合實際行為。

測試：對同一個 15m 桶寫入完整的 1m 資料，執行一次 Downsample，記下 15m 的平均值。接著刪除該桶前半段的 1m 列（模擬 prune），把 `now` 推進到讓該桶落在 `lb` 之前，再執行一次 Downsample，斷言 15m 的值沒有改變。必須在舊程式上失敗。

## Task 7：`CreateJobUnique` 用單一條 SQL 檢查並插入

檔案：`internal/store/store.go:590-604`

問題：先 SELECT 再 INSERT，兩個請求同時進來時會建立兩個 job。

做法：改成單一條語句（SQLite 單一語句是原子的）：
```sql
INSERT INTO jobs (software,machine,kind,runner)
SELECT ?,?,?,? WHERE NOT EXISTS (
  SELECT 1 FROM jobs WHERE software=? AND machine=? AND status IN ('queued','running'))
```
`RowsAffected() == 0` 時回傳 `(0, nil)`，否則回傳 `LastInsertId()`。不改 schema。

測試：並發 10 個 goroutine 同時呼叫 `CreateJobUnique` 建立同一個 (software, machine)，斷言剛好 1 個回傳非 0 的 id，DB 中 queued 的 job 只有 1 筆。既有測試要繼續通過。

## Task 8：單行超過 1MB 時，executor 不再卡到 timeout

檔案：`internal/executor/executor.go`、`internal/executor/executor_test.go`

問題：Scanner 遇到超長的行就停止讀取，子程序寫滿 pipe 後會一直卡住，直到 timeout。

做法：scan 迴圈結束後，如果 `sc.Err() != nil`，先呼叫 `onLine("■ 輸出單行超過 1MB，其餘輸出略過")`（onLine 不為 nil 時），再 `io.Copy(io.Discard, stdout)` 把剩下的輸出讀完，然後才 `c.Wait()`。

測試：執行 `head -c 2000000 /dev/zero | tr '\0' a; echo; for i in $(seq 1 2000); do echo xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx; done`，timeout 設 10 秒，斷言 3 秒內結束且 ExitCode 為 0。必須在舊程式上失敗或逾時。如果 Windows 平台不適用，用 build tag 或 `runtime.GOOS` 跳過。

## Task 9：精簡

檔案：`internal/store/store.go`、`cmd/cockpit/serve.go`、`go.mod`、`go.sum`

- 刪除 `store.go` 自己寫的 `contains`，所有呼叫點改用 `strings.Contains`（先 grep 整個 repo 找出全部呼叫點，含測試）。
- `cmd/cockpit/serve.go` 拿掉 `if true { // 排程恆啟動... }` 這層包裝，內容維持不變；註解保留為一般註解。
- 執行 `go mod tidy`，讓直接 import 的 `github.com/kardianos/service` 不再標成 `// indirect`。
- 這個 task 不需要新測試，既有測試全過即可。
