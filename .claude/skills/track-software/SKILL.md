---
name: track-software
description: 為 cockpit 新增軟體/服務版本追蹤。當使用者要求「追蹤某軟體」「加入版本監控」「在某台機器上追蹤 X」時使用。涵蓋安裝形態偵測、current_cmd/update 指令配方、乾淨環境驗證、API 建立與驗收的完整流程。
---

# 為 cockpit 新增軟體版本追蹤

把任意軟體/服務接進 cockpit 的版本追蹤：Web UI 顯示「目前版本 / 最新版本 / behind N 版」、繁中 changelog、一鍵更新。

## 前置條件

1. 目標機器已註冊且 agent online：

```sh
curl -s $COCKPIT_API/api/systems | python3 -m json.tool
```

`COCKPIT_API` 是管理 API 位址。若 server 有 Cloudflare Access 之類的保護，從跑 serve 的那台機器以 localhost 呼叫（例：`http://127.0.0.1:8787`）。

2. 確認 agent 的執行身份（影響所有指令配方）：
   - Linux systemd / macOS 預設安裝 → **root**
   - macOS plist 有 `UserName` → 該使用者
   - 不確定就在目標機器跑 `ps -axo user,args | grep "cockpit agent"`

## 第 1 步：偵測安裝形態

在目標機器上執行（看 symlink 指向哪裡就知道形態）：

```sh
which <軟體>; ls -l $(which <軟體>)
```

| symlink / 路徑特徵 | 形態 |
|---|---|
| `<prefix>/lib/node_modules/...` | npm global |
| `~/Library/pnpm/...` | pnpm global |
| `/opt/homebrew/Cellar/...` | brew formula |
| `/opt/homebrew/Caskroom/...` | brew cask（native binary） |
| `~/.local/share/uv/tools/...` | uv tool |
| pipx venv（`pipx list` 可見） | pipx |
| `~/.local/share/<軟體>/versions/...` | native 安裝器（自帶 self-update） |
| 不在 PATH、是 docker 容器 | docker 服務（見下方專節） |

## 第 2 步：組 current_cmd 並做乾淨環境驗證

**鐵則：current_cmd 由 agent 在無登入 shell 的 daemon 環境執行，必須用絕對路徑 + 顯式 PATH，並先驗證。**

驗證指令（模擬 daemon 環境；agent 是 root 就加 sudo）：

```sh
env -i HOME=$HOME [sudo -S] [PATH=...] <current_cmd>
```

通過標準：**exit code 0 且輸出含版本號**。stderr 有 warning 沒關係，版本擷取會自動抓第一個版本樣式字串。

配方表：

| 形態 | current_cmd 範例 |
|---|---|
| native | `/home/u/.local/bin/claude --version`（純絕對路徑） |
| npm launcher | `PATH=/opt/homebrew/bin:$PATH /opt/homebrew/bin/<軟體> --version`（shebang 是 `env node`，PATH 要有 node） |
| pnpm | 同 npm，另需 `PNPM_HOME=~/Library/pnpm PATH=$PNPM_HOME:...` |
| brew cask / 系統 npm（如 `/usr/bin/x`） | 絕對路徑直接跑 |
| pipx / uv tool | venv launcher 絕對路徑直接跑（如 `/home/u/.local/bin/headroom --version`） |
| 啟動會載 plugin 的工具 | 把 plugin 依賴（如 `gh`）所在目錄補進 PATH；只要 exit 0 即可 |

## 第 3 步：選 latest_source 與 changelog

| 來源 | 寫法 | 適用 |
|---|---|---|
| npm | `npm:<package>` | npm/pnpm 裝的（cask 版號若與 npm 同步也可共用） |
| PyPI | `pypi:<package>` | pipx / uv tool |
| brew | `brew:<formula>` | brew formula |
| GitHub releases | `github:<owner>/<repo>` | 有打 release 的任何專案（含 docker 服務） |
| claude plugin | `claude-plugin:<owner>/<repo>` | Claude Code plugin |
| custom | `custom:<bash 指令>` | 以上皆非，指令輸出最新版本字串 |

changelog 欄位填 `github:<owner>/<repo>` 即自動抓 release notes 並翻繁中。tag 命名不規則（如 `rust-vX.Y.Z`）也能 fallback 比對。

## 第 4 步：組 update 指令

**鐵則：agent 是 root、而軟體裝在使用者層（homebrew、家目錄）時，必須降權執行，否則會把檔案弄成 root-owned。**

降權方式（macOS 千萬不要用 `su - user -c`，無 TTY 時 PAM 直接拒絕、log 只見 `su: Sorry`）：

- macOS：`sudo -u <user> -H bash -lc '<指令>'`
- Linux：`runuser -l <user> -c '<指令>'`

配方表：

| 形態 | update cmd 範例 |
|---|---|
| npm（使用者層） | `sudo -u curtis -H bash -lc 'PATH=/opt/homebrew/bin:$PATH npm install -g <pkg>@latest'` |
| npm（系統層 root 裝） | `npm install -g <pkg>@latest`（agent 是 root 直接跑） |
| brew cask | `sudo -u curtis -H bash -lc '/opt/homebrew/bin/brew upgrade --cask <名>'` |
| pipx | `runuser -l curtis -c 'pipx upgrade <pkg>'` |
| uv tool | `PATH=/opt/homebrew/bin:$PATH /Users/u/.local/bin/uv tool upgrade <pkg>`（agent 已是該使用者時免降權） |
| native | `runuser -l curtis -c '<絕對路徑> update'` |

**更新後要重啟服務的，用 `&&` 串在後面：**

- macOS LaunchAgent：`launchctl kickstart -k gui/<uid>/<label>`（先 `launchctl print gui/<uid>/<label>` 確認網域存在）
- Linux user service（root 重啟）：`systemctl --machine <user>@.host --user restart <unit>`
- Linux system service：`systemctl restart <unit>`

## docker 服務專節

容器跑的服務（image tag 是 `:latest` 時版本看不出來）：

- **current_cmd**：讀 image label →
  `docker inspect <容器名> -f '{{index .Config.Labels "org.opencontainers.image.version"}}'`
  （label 沒有的話 fallback：app 的 version endpoint、或 image tag 本身帶版本時解析 tag）
- **latest_source**：`github:<owner>/<repo>`（上游 repo 的 releases）
- **update**：`cd <compose目錄> && docker compose -f <files...> pull && docker compose -f <files...> up -d`
  - compose 檔案與目錄用 `docker inspect <容器> -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}} {{index .Config.Labels "com.docker.compose.project.config_files"}}'` 查
- agent 是 root 而 docker 是 rootful 時免降權

## 第 5 步：建立與驗收

POST（新軟體帶全欄位；既有軟體加機器時只帶 name/machine/current_cmd/update，沿用軟體層設定）：

```sh
curl -s -X POST $COCKPIT_API/api/software -H "Content-Type: application/json" -d '{
  "name": "<軟體>",
  "kind": "<npm|pypi|brew|docker|...>",
  "latest_source": "<來源>",
  "changelog": "github:<owner>/<repo>",
  "machine": "<機器label>",
  "current_cmd": "<已驗證的指令>",
  "update": {"type": "command", "cmd": "<更新指令>"}
}'
```

驗收三連：

```sh
curl -s -X POST $COCKPIT_API/api/check          # 觸發全面檢查
sleep 20
curl -s $COCKPIT_API/api/installs               # 確認 current/latest/status，error 須為 null
curl -s $COCKPIT_API/api/changelog/<軟體>/<最新版>  # 確認 changelog 已抓到
```

改既有設定用 PATCH：`curl -X PATCH $COCKPIT_API/api/software/<軟體>/<機器> -d '{"update":{...}}'`

更新失敗時看 job log 找根因：`curl -s $COCKPIT_API/api/jobs`（常見：`su: Sorry`＝降權方式錯、`not found`＝PATH 缺、`EACCES`＝該降權沒降權）。

## 常見坑速查

| 症狀 | 根因 → 解法 |
|---|---|
| `su: Sorry` | macOS daemon 無 TTY，PAM 拒絕 `su` → 改 `sudo -u <user> -H bash -lc` |
| `env: node: No such file or directory` | npm launcher 找不到 node → PATH 補 node 所在目錄 |
| `ERR_PNPM_NO_GLOBAL_BIN_DIR` | 缺 `PNPM_HOME` → 環境前綴補上 |
| 指令在終端 OK、agent 跑失敗 | 沒做 `env -i` 乾淨環境驗證 → 回第 2 步 |
| 更新成功但 UI 版本沒變 | 服務沒重啟（舊行程還在）→ update cmd 補重啟段 |
| changelog 空白 | tag 命名特殊 → 確認 `github:` 來源；翻譯是非同步的，稍等再查 |
