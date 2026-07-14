# Changelog 翻譯狀態 — 生產部署與補翻 runbook

> **約束：** 本文件只描述步驟。部署 binary、改生產 inventory、重啟 serve 由人工在 Mac mini 執行；agent 不直接動生產。

**相關：** design §8、plan Task 7、`inventory.example.yaml`（grok 已示範 `url:`）

**目標主機：** Mac mini（serve @ `127.0.0.1:8787`）  
**路徑慣例：** binary `/usr/local/bin/cockpit`；config `/etc/cockpit/`；db `/var/lib/cockpit/cockpit.db`

---

## 0. 前置檢查

- [ ] 本 feature 分支已合併（或至少本機 checkout 含 T1–T6 的 commit）
- [ ] 生產 LM Studio / translate endpoint 可達；WebUI timeout 建議 **300s**（clamp 30–900）
- [ ] **不**把生產 `inventory.yaml`、`serve.json`、token 寫進 git

---

## 1. Build（開發機或 Mac mini）

Mac mini 為 Apple Silicon；若在該機直接編：

```bash
cd /Users/curtis/Dev/cockpit   # 或部署用 clone 路徑
git pull                       # 取得含 translate status 的程式
CGO_ENABLED=0 go build -ldflags "-s -w" -o dist/cockpit ./cmd/cockpit
./dist/cockpit version         # 確認 binary 可跑
```

若在其他機器交叉編譯：

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o dist/cockpit-darwin-arm64 ./cmd/cockpit
# scp 到 Mac mini，例如：
# scp dist/cockpit-darwin-arm64 curtis@<mac-mini>:/tmp/cockpit
```

（可選）正式 release 仍可用 GoReleaser / `cockpit upgrade`；本 runbook 以本地 build 替換為準。

---

## 2. 安裝 binary（Mac mini）

```bash
# 服務跑著時可先裝好再重啟；binary 需可被 serve 行程覆寫（見 self-upgrade 設計）
sudo install -m755 /tmp/cockpit /usr/local/bin/cockpit
# 若 serve 以 curtis 執行且需保留可寫：
# sudo chown curtis /usr/local/bin/cockpit

/usr/local/bin/cockpit version
```

---

## 3. 修補生產 inventory（grok changelog）

編輯 **`/etc/cockpit/inventory.yaml`**（勿 commit）。為 `grok` 加上（或改成）：

```yaml
  - name: grok
    # ... 其餘欄位維持 ...
    changelog: "url:https://x.ai/cli/changelogs/{version}.external.md"
```

Repo 範本已對齊：`inventory.example.yaml` 同一行。

若 inventory 尚無 grok 條目，可對照 `inventory.example.yaml` 補齊，**不要**複製 example 裡的假 host / 空 token。

---

## 4. 重啟 serve

```bash
# 以服務管理（推薦）
sudo cockpit service stop  -mode serve
sudo cockpit service start -mode serve
sudo cockpit service status -mode serve

# 或 macOS launchd（依實際 label / 網域調整）
# launchctl print gui/$(id -u)/co.sitruc.cockpit   # 先確認 label
# launchctl kickstart -k gui/$(id -u)/co.sitruc.cockpit

# 前景除錯（非生產常態）
# cockpit serve -config /etc/cockpit/serve.json
```

重啟後 Open DB 會跑 migration + `BackfillTranslateStatus`：有 raw 無 zh → `translate_status=failed`。

---

## 5. 補翻與驗收

在 **Mac mini 本機**（或 tunnel 後可達 8787 的環境）執行：

```bash
# 對已知 failed / 空 zh 版本觸發非同步重翻
curl -sS -X POST "http://127.0.0.1:8787/api/changelog/multica/0.4.1/retry"
curl -sS -X POST "http://127.0.0.1:8787/api/changelog/openclaw/2026.7.1/retry"

# 立即檢查：refresh 上游 + 讓 grok 依 url: 抓 raw 並嘗試翻譯
curl -sS -X POST "http://127.0.0.1:8787/api/check"

# 等數分鐘（openclaw raw 大、timeout 可能 300s）後查狀態
curl -sS "http://127.0.0.1:8787/api/changelog/multica/0.4.1" | python3 -m json.tool
curl -sS "http://127.0.0.1:8787/api/changelog/openclaw/2026.7.1" | python3 -m json.tool
```

**Expected**

| 項目 | 條件 |
|------|------|
| multica / openclaw | `translate_status` = `ready`，`changelog_zh` 非空 |
| 進行中 | `pending` / `translating`（modal 會輪詢）；同 key 再 POST retry → **409** |
| 失敗 | `failed` + `translate_error` 可讀；可再 retry |
| grok | 某 latest 版 `changelog_raw` 非空；翻完後 zh + `ready` |
| timeout 設定 | `GET /api/translate/config` 見 `timeout_sec`（建議 300） |

WebUI：開 changelog modal → 可見 翻譯中 / 失敗+重試 / 完成；列表「中文」按鈕無 status badge。

---

## 6. 回滾（必要時）

```bash
# 還原舊 binary 後重啟（inventory 的 url: 行可保留，舊版若未支援會忽略或報錯時再拿掉）
sudo install -m755 /path/to/previous-cockpit /usr/local/bin/cockpit
sudo cockpit service stop -mode serve && sudo cockpit service start -mode serve
```

DB 新欄位（`translate_status` 等）可保留；舊碼不讀即可。

---

## 7. 完成勾選

- [ ] 新 binary 已安裝並 `version` 符合預期
- [ ] 生產 inventory grok `changelog: url:…`
- [ ] serve 已重啟；API 可達
- [ ] multica@0.4.1、openclaw@2026.7.1 補翻 ready
- [ ] grok raw 非空（check 後）
- [ ] 未將 secret / 生產 inventory 提交 git
