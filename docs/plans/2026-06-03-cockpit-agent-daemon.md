# Cockpit Agent Daemon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把子系統 2 的執行層從「中央 SSH over Tailscale 拉取」改為「每機 cockpit-agent（Go daemon）經 Cloudflare Tunnel 主動回報與執行」，中央 server 只編排。

**Architecture:** Server（Python/FastAPI）保留上游抓取/翻譯/比對/DB/前端 API/瀏覽器 SSE，並新增 `/api/agent/*` 佇列協定；移除 SSH/本機執行。各機 `cockpit-agent`（Go static binary）督管 beszel-agent、long-poll 取得 server **已渲染好的 shell 指令** 在本機執行、串 log 與回報。指令渲染與引號處理留在 server（單一來源、已測）。

**Tech Stack:** Python 3.11 / FastAPI / SQLite / APScheduler（server）；Go 1.22+（agent，標準庫 net/http + os/exec，無第三方依賴）。

設計依據：`docs/specs/2026-06-03-cockpit-agent-daemon-design.md`。

---

## 慣例與型別約定（跨任務一致，請勿改名）

- **狀態字串**：install status ∈ `up_to_date|behind|unknown|error`；job status ∈ `queued|running|success|failed|aborted`；update type ∈ `command|agent`；runner ∈ `codex_exec|claude_p|custom`。
- **既有可重用**（保持不變）：`cockpit/models.py`、`cockpit/inventory.py`（將加 `agent_token`）、`cockpit/version_parse.py`（`parse_version`/`compare`）、`cockpit/sources/`、`cockpit/translate.py`、`cockpit/db.py`（將加欄位/函式）、`cockpit/jobs.py`（保留 `build_update`/`start_job`/`ActiveJobExists`，改執行模型）、`cockpit/web/app.py`（已有前端 API + 瀏覽器 SSE + FE-A 的 machines/jobs/enriched installs）。
- **agent 協定**：所有 `/api/agent/*` 需 `Authorization: Bearer <agent_token>`；server 由 token 解析 `machine`。
- **Go module path**：`cockpit-agent`（`agent/go.mod` module 名 `cockpit-agent`）。

## 檔案結構

```
cockpit/
  cockpit/
    db.py            # +欄位、+claim/record/abort/check 函式
    inventory.py     # +agent_token 解析、+token→machine map、+load_inventory_text
    jobs.py          # build_update 保留；run_job→claim_next_job/record_result/abort
    collector.py     # 拆成 refresh_upstream + apply_version_report
    schema.sql       # jobs 加欄位、+machine_state
    web/
      app.py         # 既有；wire 入 agent router、改 /api/check、改瀏覽器 abort
      agent.py       # 新：/api/agent/* 路由 + token 認證 dependency
    config.py        # 既有
    scheduler.py     # 既有（排程改跑 refresh_upstream，在 main 接線）
    main.py          # build() 改：scheduler 跑 refresh_upstream、create_app 帶 inventory_path/token map
  tests/
    test_*.py
agent/               # 新 Go 專案（cockpit-agent）
  go.mod
  main.go
  internal/
    config/config.go
    httpclient/client.go
    executor/executor.go
    reporter/reporter.go
    jobrunner/jobrunner.go
    supervisor/supervisor.go
  deploy/
    cockpit-agent.service      # systemd 範本
    cockpit-agent.plist        # launchd 範本
    config.example.json
```

---

# Part A — Server（Python）

> 每個 server 任務結束都跑全套件確認無回歸：`cd /Users/curtis/Dev/cockpit && . .venv/bin/activate && python -m pytest -q`。當前基線 59 測試綠（含 FE-A）。

### Task 1: DB schema delta 與資料層

**Files:**
- Modify: `cockpit/cockpit/schema.sql`
- Modify: `cockpit/cockpit/db.py`
- Test: `cockpit/tests/test_db_agent.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_db_agent.py`:

```python
from cockpit import db


def _conn(tmp_path):
    c = db.connect(tmp_path / "c.db")
    db.init_db(c)
    return c


def test_job_agent_columns_default(tmp_path):
    c = _conn(tmp_path)
    jid = db.create_job(c, "cc", "mac", "command")
    job = db.get_job(c, jid)
    assert job["cmd"] is None
    assert job["abort_requested"] == 0


def test_set_job_dispatch_and_running(tmp_path):
    c = _conn(tmp_path)
    jid = db.create_job(c, "cc", "mac", "command")
    db.set_job_dispatch(c, jid, cmd="npm i -g cc@latest", cwd=None,
                        current_cmd="cc --version", version_regex=None)
    db.set_job_running(c, jid)
    job = db.get_job(c, jid)
    assert job["cmd"] == "npm i -g cc@latest"
    assert job["current_cmd"] == "cc --version"
    assert job["status"] == "running"


def test_claim_oldest_queued_marks_running(tmp_path):
    c = _conn(tmp_path)
    a = db.create_job(c, "a", "mac", "command")
    b = db.create_job(c, "b", "mac", "command")
    db.create_job(c, "c", "box", "command")
    assert db.claim_oldest_queued(c, "mac")["id"] == a   # 最舊先出、限定該機、原子標 running
    assert db.get_job(c, a)["status"] == "running"
    assert db.claim_oldest_queued(c, "mac")["id"] == b
    assert db.claim_oldest_queued(c, "mac") is None       # 該機已無 queued


def test_abort_flag(tmp_path):
    c = _conn(tmp_path)
    jid = db.create_job(c, "cc", "mac", "command")
    assert db.abort_requested(c, jid) is False
    db.request_abort(c, jid)
    assert db.abort_requested(c, jid) is True


def test_check_flag_roundtrip(tmp_path):
    c = _conn(tmp_path)
    db.set_check_requested(c, "mac")
    db.set_check_requested(c, "box")
    assert db.take_check_requested(c, "mac") is True     # 取後清除
    assert db.take_check_requested(c, "mac") is False
    assert db.take_check_requested(c, "box") is True
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit && . .venv/bin/activate && python -m pytest tests/test_db_agent.py -v`
Expected: FAIL（no such column: cmd / AttributeError）。

- [ ] **Step 3: 更新 schema.sql**

Replace the `jobs` table block in `cockpit/schema.sql` with（新增 5 欄）：

```sql
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  software TEXT NOT NULL,
  machine TEXT NOT NULL,
  kind TEXT NOT NULL,
  runner TEXT,
  status TEXT NOT NULL DEFAULT 'queued',
  started_at TEXT,
  finished_at TEXT,
  exit_code INTEGER,
  new_version TEXT,
  log TEXT NOT NULL DEFAULT '',
  cmd TEXT,
  cwd TEXT,
  current_cmd TEXT,
  version_regex TEXT,
  abort_requested INTEGER NOT NULL DEFAULT 0
);
```

Append a new table at the end of `cockpit/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS machine_state (
  machine TEXT PRIMARY KEY,
  check_requested INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT DEFAULT (datetime('now'))
);
```

> 注意：`CREATE TABLE IF NOT EXISTS` 不會替既有 DB 加欄位。本專案尚未部署、homelab 可重建 DB；測試皆用新建 DB。部署文件會註明首次升級需刪舊 `cockpit.db`。

- [ ] **Step 4: 在 db.py 新增函式**

Append to `cockpit/db.py`（沿用既有 `@_synchronized` 裝飾器）：

```python
@_synchronized
def set_job_dispatch(conn, job_id, cmd, cwd, current_cmd, version_regex):
    conn.execute(
        "UPDATE jobs SET cmd=?, cwd=?, current_cmd=?, version_regex=? WHERE id=?",
        (cmd, cwd, current_cmd, version_regex, job_id),
    )
    conn.commit()


@_synchronized
def claim_oldest_queued(conn, machine):
    """原子取該機最舊 queued job 並標 running，回該 row（無則 None）。"""
    row = conn.execute(
        "SELECT * FROM jobs WHERE machine=? AND status='queued' ORDER BY id LIMIT 1",
        (machine,),
    ).fetchone()
    if row is None:
        return None
    conn.execute(
        "UPDATE jobs SET status='running', started_at=datetime('now') WHERE id=?",
        (row["id"],),
    )
    conn.commit()
    return row


@_synchronized
def request_abort(conn, job_id):
    conn.execute("UPDATE jobs SET abort_requested=1 WHERE id=?", (job_id,))
    conn.commit()


@_synchronized
def abort_requested(conn, job_id):
    row = conn.execute("SELECT abort_requested FROM jobs WHERE id=?", (job_id,)).fetchone()
    return bool(row and row["abort_requested"])


@_synchronized
def set_check_requested(conn, machine):
    conn.execute(
        """INSERT INTO machine_state (machine, check_requested, updated_at)
           VALUES (?, 1, datetime('now'))
           ON CONFLICT(machine) DO UPDATE SET check_requested=1, updated_at=datetime('now')""",
        (machine,),
    )
    conn.commit()


@_synchronized
def take_check_requested(conn, machine):
    row = conn.execute(
        "SELECT check_requested FROM machine_state WHERE machine=?", (machine,)
    ).fetchone()
    requested = bool(row and row["check_requested"])
    if requested:
        conn.execute(
            "UPDATE machine_state SET check_requested=0, updated_at=datetime('now') WHERE machine=?",
            (machine,),
        )
        conn.commit()
    return requested
```

- [ ] **Step 5: 跑測試確認通過**

Run: `python -m pytest tests/test_db_agent.py -v`
Expected: PASS（5 passed）。

- [ ] **Step 6: 全套件 + Commit**

```bash
cd /Users/curtis/Dev/cockpit && . .venv/bin/activate && python -m pytest -q
git add cockpit/schema.sql cockpit/db.py tests/test_db_agent.py && git commit -m "feat: db schema and helpers for agent queue model"
```
Expected: 64 passed（59 + 5）。

---

### Task 2: inventory 加 agent_token 與 token→machine map

**Files:**
- Modify: `cockpit/cockpit/inventory.py`
- Modify: `cockpit/cockpit/models.py`
- Test: `cockpit/tests/test_inventory_agent.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_inventory_agent.py`:

```python
from cockpit.inventory import load_inventory_text, machine_for_token, InventoryError

INV = """
machines:
  mac:  { host: 1.2.3.4, ssh_user: curtis, local: true, agent_token: tok-mac }
  box:  { host: 5.6.7.8, ssh_user: root, agent_token: tok-box }
software:
  - name: cc
    kind: npm
    latest_source: "npm:cc"
    changelog: null
    installs:
      - machine: mac
        current_cmd: "cc --version"
        update: { type: command, cmd: "npm i -g cc@latest" }
"""


def test_load_text_and_tokens():
    inv = load_inventory_text(INV)
    assert inv.machines["mac"].agent_token == "tok-mac"
    assert machine_for_token(inv, "tok-box") == "box"
    assert machine_for_token(inv, "nope") is None


def test_bad_text_raises():
    import pytest
    with pytest.raises(InventoryError):
        load_inventory_text("not: [a, mapping")
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_inventory_agent.py -v`
Expected: FAIL（ImportError: load_inventory_text）。

- [ ] **Step 3: models.py 加欄位**

In `cockpit/models.py`, add `agent_token` to `Machine`（放最後、預設 None，不影響既有呼叫）：

```python
@dataclass
class Machine:
    name: str
    host: str
    ssh_user: str
    local: bool = False
    agent_token: str | None = None
```

- [ ] **Step 4: inventory.py 重構出 load_inventory_text + token helper**

In `cockpit/inventory.py`, replace `load_inventory` so it delegates to a new text parser, parse `agent_token`, and add `machine_for_token`:

```python
def load_inventory_text(text: str) -> Inventory:
    try:
        data = yaml.safe_load(text)
    except yaml.YAMLError as e:
        raise InventoryError(f"YAML 解析失敗: {e}")
    if not isinstance(data, dict):
        raise InventoryError("inventory 根節點必須是 mapping")

    machines: dict[str, Machine] = {}
    for name, m in (data.get("machines") or {}).items():
        if not isinstance(m, dict):
            raise InventoryError(f"machine {name}: 定義必須是 mapping")
        if "host" not in m or "ssh_user" not in m:
            raise InventoryError(f"machine {name}: 需要 host 與 ssh_user")
        machines[name] = Machine(name=name, host=m["host"], ssh_user=m["ssh_user"],
                                 local=bool(m.get("local", False)),
                                 agent_token=m.get("agent_token"))

    software: list[Software] = []
    for sw in (data.get("software") or []):
        name = sw.get("name")
        if not name:
            raise InventoryError("software 條目缺少 name")
        if not sw.get("latest_source"):
            raise InventoryError(f"software {name}: 需要 latest_source")
        installs: list[Install] = []
        for i, inst in enumerate(sw.get("installs") or []):
            ctx = f"software {name} install[{i}]"
            mach = inst.get("machine")
            if mach not in machines:
                raise InventoryError(f"{ctx}: 參照未知 machine {mach!r}")
            if not inst.get("current_cmd"):
                raise InventoryError(f"{ctx}: 需要 current_cmd")
            update = _parse_update(inst.get("update"), ctx)
            installs.append(Install(machine=mach, current_cmd=inst["current_cmd"],
                                    update=update, version_regex=inst.get("version_regex")))
        software.append(Software(name=name, kind=sw.get("kind", "custom"),
                                 latest_source=sw["latest_source"],
                                 changelog=sw.get("changelog"), installs=installs))
    return Inventory(machines=machines, software=software)


def load_inventory(path: str | Path) -> Inventory:
    return load_inventory_text(Path(path).read_text())


def machine_for_token(inv: Inventory, token: str) -> str | None:
    if not token:
        return None
    for name, m in inv.machines.items():
        if m.agent_token and m.agent_token == token:
            return name
    return None
```

(刪除舊的 `load_inventory` 內文與 `_parse_update` 重複；保留 `_parse_update` 與 `InventoryError` 定義不變。`from pathlib import Path` 已存在。)

- [ ] **Step 5: 跑測試確認通過 + 既有 inventory 測試不回歸**

Run: `python -m pytest tests/test_inventory_agent.py tests/test_inventory.py -v`
Expected: PASS（既有 inventory 測試仍綠 + 2 new）。

- [ ] **Step 6: 全套件 + Commit**

```bash
python -m pytest -q
git add cockpit/models.py cockpit/inventory.py tests/test_inventory_agent.py && git commit -m "feat: inventory agent_token and token->machine resolution"
```
Expected: 66 passed。

---

### Task 3: jobs 佇列模型（claim / record / abort）

**Files:**
- Modify: `cockpit/cockpit/jobs.py`
- Test: `cockpit/tests/test_jobs_queue.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_jobs_queue.py`:

```python
import pytest
from cockpit import db, jobs
from cockpit.models import Machine, Update, Install, Software, Inventory


def _inv_command():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _seed(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    db.add_version(c, "cc", "2.1.101", None, "raw", "中文")
    db.upsert_install(c, "cc", "mac", "2.1.98", "behind", "t")
    return c


def test_claim_renders_and_marks_running(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")          # queued
    claimed = jobs.claim_next_job(c, inv, "mac")
    assert claimed["id"] == jid
    assert claimed["shell_cmd"] == "npm i -g cc@latest"
    assert claimed["current_cmd"] == "cc --version"
    assert db.get_job(c, jid)["status"] == "running"
    # 取完即無下一件
    assert jobs.claim_next_job(c, inv, "mac") is None


def test_record_result_success_updates_install(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")
    jobs.record_result(c, inv, jid, "success", 0, "2.1.101")
    job = db.get_job(c, jid)
    assert job["status"] == "success"
    assert job["new_version"] == "2.1.101"
    inst = db.get_install(c, "cc", "mac")
    assert inst["current_version"] == "2.1.101"
    assert inst["status"] == "up_to_date"


def test_record_result_failed_keeps_behind(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")
    jobs.record_result(c, inv, jid, "failed", 1, None)
    assert db.get_job(c, jid)["status"] == "failed"
    assert db.get_install(c, "cc", "mac")["status"] == "behind"


def test_request_abort_running_sets_flag(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")                 # running
    job = jobs.request_abort(c, jid)
    assert job["status"] == "running"                  # 仍在跑，等 agent 回 aborted
    assert db.abort_requested(c, jid) is True


def test_request_abort_queued_marks_aborted(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")           # queued, 未 claim
    job = jobs.request_abort(c, jid)
    assert job["status"] == "aborted"


def test_record_result_aborted(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")
    jobs.record_result(c, inv, jid, "aborted", -1, None)
    assert db.get_job(c, jid)["status"] == "aborted"
    assert db.get_install(c, "cc", "mac")["status"] == "behind"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_jobs_queue.py -v`
Expected: FAIL（AttributeError: claim_next_job）。

- [ ] **Step 3: 重寫 jobs.py 執行模型**

In `cockpit/jobs.py`：保留 `_find`、`_render`、`build_update`、`start_job`、`ActiveJobExists`、`_now`。**刪除 `run_job`**（本機 execute 已移到 agent），改新增下列函式。完整檔案如下：

```python
from __future__ import annotations
import shlex

from cockpit import db
from cockpit.models import Inventory, Software, Install, Machine


class ActiveJobExists(Exception):
    pass


def _find(inv: Inventory, software: str, machine: str) -> tuple[Software, Install]:
    for sw in inv.software:
        if sw.name == software:
            for inst in sw.installs:
                if inst.machine == machine:
                    return sw, inst
    raise KeyError(f"找不到 install: {software}@{machine}")


def _render(template: str, **vars) -> str:
    out = template
    for k, v in vars.items():
        out = out.replace("{" + k + "}", "" if v is None else str(v))
    return out


def build_update(inv: Inventory, sw: Software, inst: Install, *, latest_version,
                 current_version, changelog_zh) -> tuple[str, Machine]:
    upd = inst.update
    target_name = upd.machine or inst.machine
    machine = inv.machines[target_name]
    if upd.type == "command":
        return upd.cmd, machine

    prompt = _render(upd.prompt, name=sw.name, machine=target_name,
                     current_version=current_version, latest_version=latest_version,
                     changelog_zh=changelog_zh, cwd=upd.cwd)
    if upd.runner == "codex_exec":
        cd = f"--cd {shlex.quote(upd.cwd)} " if upd.cwd else ""
        cmd = f"codex exec {cd}{shlex.quote(prompt)}"
    elif upd.runner == "claude_p":
        cd = f"cd {shlex.quote(upd.cwd)} && " if upd.cwd else ""
        cmd = f"{cd}claude -p {shlex.quote(prompt)}"
    elif upd.runner == "custom":
        cmd = _render(upd.invoke, prompt=shlex.quote(prompt),
                      cwd=shlex.quote(upd.cwd) if upd.cwd else "")
    else:
        raise ValueError(f"未知 runner: {upd.runner}")
    return cmd, machine


def start_job(conn, inv: Inventory, software: str, machine: str) -> int:
    sw, inst = _find(inv, software, machine)
    jid = db.create_job_unique(conn, software, machine, inst.update.type,
                               runner=inst.update.runner)
    if jid is None:
        raise ActiveJobExists(f"active job already exists for {software}@{machine}")
    return jid


def claim_next_job(conn, inv: Inventory, machine: str) -> dict | None:
    """原子取該機最舊 queued job（標 running）、渲染指令、寫入 dispatch 欄位，回 dict。"""
    row = db.claim_oldest_queued(conn, machine)   # 原子 queued→running
    if row is None:
        return None
    job_id = row["id"]
    sw, inst = _find(inv, row["software"], row["machine"])
    latest = db.get_latest_version(conn, sw.name)
    latest_version = latest["version"] if latest else None
    changelog_zh = latest["changelog_zh"] if latest else None
    cur = db.get_install(conn, sw.name, inst.machine)
    current_version = cur["current_version"] if cur else None
    cmd, _machine = build_update(inv, sw, inst, latest_version=latest_version,
                                 current_version=current_version, changelog_zh=changelog_zh)
    cwd = inst.update.cwd if inst.update.type == "agent" else None
    db.set_job_dispatch(conn, job_id, cmd=cmd, cwd=cwd,
                        current_cmd=inst.current_cmd, version_regex=inst.version_regex)
    return {
        "id": job_id, "software": sw.name, "machine": inst.machine,
        "shell_cmd": cmd, "cwd": cwd,
        "current_cmd": inst.current_cmd, "version_regex": inst.version_regex,
    }


def record_result(conn, inv: Inventory, job_id: int, status: str,
                  exit_code: int, new_version: str | None) -> None:
    job = db.get_job(conn, job_id)
    if job is None:
        return
    if status == "success" and new_version:
        db.upsert_install(conn, job["software"], job["machine"], new_version,
                          "up_to_date", _now())
    db.finish_job(conn, job_id, status, exit_code, new_version=new_version)
    db.add_event(conn, "update", job["software"], job["machine"],
                 f"job {job_id} {status} exit={exit_code} new={new_version}")


def request_abort(conn, job_id: int) -> dict | None:
    job = db.get_job(conn, job_id)
    if job is None:
        return None
    if job["status"] == "queued":
        db.finish_job(conn, job_id, "aborted", -1)
        db.add_event(conn, "update", job["software"], job["machine"], f"job {job_id} aborted (queued)")
        return dict(db.get_job(conn, job_id))
    if job["status"] == "running":
        db.request_abort(conn, job_id)
    return dict(db.get_job(conn, job_id))


def _now() -> str:
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).isoformat()
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_jobs_queue.py -v`
Expected: PASS（6 passed）。

- [ ] **Step 5: 移除過時的 run_job 測試**

`tests/test_jobs.py` 內測 `run_job` 的測試（`test_run_update_job_success`、`test_run_job_build_error_not_stuck`、`test_run_job_verify_failure_still_finishes`）已不適用（執行移到 agent）。刪除這三個測試函式；保留 `build_update`/`start_job` 的測試（`test_build_command_cmd`、`test_build_agent_codex_exec_renders_prompt`、`test_build_agent_claude_p_renders_prompt`、`test_build_agent_custom_invoke_template`、`test_build_unknown_runner_raises`、`test_start_job_blocks_when_active`）。同時刪除該檔頂部不再需要的 `from cockpit.runner import ExecResult` import（若刪測試後沒人用）。

Run: `python -m pytest tests/test_jobs.py -v`
Expected: PASS（剩 6 passed）。

- [ ] **Step 6: 全套件 + Commit**

```bash
python -m pytest -q
git add cockpit/jobs.py tests/test_jobs_queue.py tests/test_jobs.py && git commit -m "feat: job queue model (claim/record/abort) replacing in-process run_job"
```
Expected: 全綠（66 − 3 + 6 = 69 passed）。

---

### Task 4: collector 拆分（refresh_upstream + apply_version_report）

**Files:**
- Modify: `cockpit/cockpit/collector.py`
- Test: `cockpit/tests/test_collector_split.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_collector_split.py`:

```python
from cockpit import db
from cockpit.collector import refresh_upstream, apply_version_report
from cockpit.models import Machine, Update, Install, Software, Inventory
from cockpit.sources import SourceResult


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog="github:o/cc",
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def test_refresh_upstream_stores_and_translates(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv()

    def fake_fetch(software, client=None):
        return SourceResult(version="2.1.101", changelog_raw="## notes")

    def fake_translate(raw, timeout=120):
        return "中文摘要"

    refresh_upstream(c, inv, fetch=fake_fetch, translate=fake_translate)
    v = db.get_version(c, "cc", "2.1.101")
    assert v["changelog_zh"] == "中文摘要"


def test_apply_version_report_marks_behind(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv()
    db.add_version(c, "cc", "2.1.101", None, "raw", "中文")     # 上游已知
    n = apply_version_report(c, inv, "mac", [{"software": "cc", "current_version": "2.1.98"}])
    assert n == 1
    inst = db.get_install(c, "cc", "mac")
    assert inst["current_version"] == "2.1.98"
    assert inst["status"] == "behind"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_collector_split.py -v`
Expected: FAIL（ImportError: refresh_upstream）。

- [ ] **Step 3: 重寫 collector.py**

Replace `cockpit/collector.py` 全檔：

```python
from __future__ import annotations
from datetime import datetime, timezone

from cockpit import db
from cockpit.models import Inventory
from cockpit.sources import fetch_latest as _default_fetch
from cockpit.translate import translate_changelog as _default_translate
from cockpit.version_parse import compare


def refresh_upstream(conn, inv: Inventory, *, fetch=_default_fetch,
                     translate=_default_translate) -> None:
    """server 端：抓每個 software 的上游最新版 + 翻譯 changelog，存入 versions。"""
    for sw in inv.software:
        try:
            latest = fetch(sw)
        except Exception as e:
            db.add_event(conn, "error", sw.name, None, f"fetch failed: {e}")
            continue
        existing = db.get_version(conn, sw.name, latest.version)
        zh = existing["changelog_zh"] if existing else None
        if zh is None and latest.changelog_raw:
            zh = translate(latest.changelog_raw)
        db.add_version(conn, sw.name, latest.version, None, latest.changelog_raw, zh)


def apply_version_report(conn, inv: Inventory, machine: str, reports) -> int:
    """agent 回報目前版：對每筆比對已知上游最新版、寫 installs。回寫入筆數。"""
    now = datetime.now(timezone.utc).isoformat()
    applied = 0
    for r in reports:
        software = r.get("software")
        current = r.get("current_version")
        if not software:
            continue
        latest = db.get_latest_version(conn, software)
        latest_version = latest["version"] if latest else None
        status, _ = compare(current, latest_version)
        db.upsert_install(conn, software, machine, current, status, now)
        db.add_event(conn, "check", software, machine,
                     f"current={current} latest={latest_version} status={status}")
        applied += 1
    return applied
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_collector_split.py -v`
Expected: PASS（2 passed）。

- [ ] **Step 5: 移除過時 collector 測試**

`tests/test_collector.py`（測舊 `run_collection`，已不存在）整檔刪除：`git rm tests/test_collector.py`。

- [ ] **Step 6: 全套件 + Commit**

```bash
python -m pytest -q
git add cockpit/collector.py tests/test_collector_split.py && git rm tests/test_collector.py && git commit -m "feat: split collector into refresh_upstream + apply_version_report"
```
Expected: 全綠（69 − 1[del test_collector] + 2 = 70 passed）。

---

### Task 5: agent 端點 + token 認證

**Files:**
- Create: `cockpit/cockpit/web/agent.py`
- Modify: `cockpit/cockpit/web/app.py`
- Test: `cockpit/tests/test_agent_api.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_agent_api.py`:

```python
from fastapi.testclient import TestClient
from cockpit import db, jobs
from cockpit.web.app import create_app
from cockpit.models import Machine, Update, Install, Software, Inventory


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True, agent_token="tok-mac")
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _app(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    db.add_version(c, "cc", "2.1.101", None, "raw", "中文")
    db.upsert_install(c, "cc", "mac", "2.1.98", "behind", "t")
    return create_app(c, _inv()), c


def _auth(tok="tok-mac"):
    return {"Authorization": f"Bearer {tok}"}


def test_agent_auth_required(tmp_path):
    app, _ = _app(tmp_path)
    cl = TestClient(app)
    assert cl.get("/api/agent/installs").status_code == 401
    assert cl.get("/api/agent/installs", headers=_auth("bad")).status_code == 401


def test_agent_installs(tmp_path):
    app, _ = _app(tmp_path)
    r = TestClient(app).get("/api/agent/installs", headers=_auth())
    assert r.status_code == 200
    assert r.json() == [{"software": "cc", "current_cmd": "cc --version", "version_regex": None}]


def test_agent_report_versions(tmp_path):
    app, c = _app(tmp_path)
    r = TestClient(app).post("/api/agent/report-versions", headers=_auth(),
                             json=[{"software": "cc", "current_version": "2.1.98"}])
    assert r.status_code == 200 and r.json()["applied"] == 1
    assert db.get_install(c, "cc", "mac")["status"] == "behind"


def test_agent_poll_returns_job_then_log_result(tmp_path):
    app, c = _app(tmp_path)
    cl = TestClient(app)
    jid = jobs.start_job(c, _inv(), "cc", "mac")        # 直接建 queued job
    poll = cl.get("/api/agent/poll", headers=_auth(), params={"wait": 0}).json()
    assert poll["type"] == "job"
    assert poll["job"]["shell_cmd"] == "npm i -g cc@latest"
    cl.post(f"/api/agent/jobs/{jid}/log", headers=_auth(), json={"lines": ["added 1 package"]})
    assert "added 1 package" in db.get_job(c, jid)["log"]
    cl.post(f"/api/agent/jobs/{jid}/result", headers=_auth(),
            json={"status": "success", "exit_code": 0, "new_version": "2.1.101"})
    assert db.get_job(c, jid)["status"] == "success"


def test_agent_poll_check_signal(tmp_path):
    app, c = _app(tmp_path)
    db.set_check_requested(c, "mac")
    poll = TestClient(app).get("/api/agent/poll", headers=_auth(), params={"wait": 0}).json()
    assert poll["type"] == "check"


def test_agent_poll_timeout_204(tmp_path):
    app, _ = _app(tmp_path)
    r = TestClient(app).get("/api/agent/poll", headers=_auth(), params={"wait": 0})
    assert r.status_code == 204


def test_agent_control_abort(tmp_path):
    app, c = _app(tmp_path)
    jid = jobs.start_job(c, _inv(), "cc", "mac")
    db.request_abort(c, jid)
    r = TestClient(app).get(f"/api/agent/jobs/{jid}/control", headers=_auth())
    assert r.json() == {"abort": True}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_agent_api.py -v`
Expected: FAIL（404，尚無 /api/agent 路由）。

- [ ] **Step 3: 實作 agent.py**

Create `cockpit/web/agent.py`:

```python
from __future__ import annotations
import asyncio

from fastapi import APIRouter, Header, HTTPException, Request, Response

from cockpit import db, jobs
from cockpit.collector import apply_version_report
from cockpit.inventory import machine_for_token

POLL_MAX_WAIT = 25.0    # long-poll 上限秒
POLL_TICK = 0.5


def _machine(inv, authorization: str | None) -> str:
    if not authorization or not authorization.startswith("Bearer "):
        raise HTTPException(401, "missing agent token")
    token = authorization.split(" ", 1)[1].strip()
    machine = machine_for_token(inv, token)
    if machine is None:
        raise HTTPException(401, "invalid agent token")
    return machine


def build_agent_router(conn, inv) -> APIRouter:
    r = APIRouter(prefix="/api/agent")

    @r.get("/installs")
    def agent_installs(authorization: str | None = Header(default=None)):
        machine = _machine(inv, authorization)
        out = []
        for sw in inv.software:
            for inst in sw.installs:
                if inst.machine == machine:
                    out.append({"software": sw.name, "current_cmd": inst.current_cmd,
                                "version_regex": inst.version_regex})
        return out

    @r.post("/report-versions")
    async def agent_report(request: Request, authorization: str | None = Header(default=None)):
        machine = _machine(inv, authorization)
        reports = await request.json()
        applied = apply_version_report(conn, inv, machine, reports)
        return {"applied": applied}

    @r.get("/poll")
    async def agent_poll(wait: float = POLL_MAX_WAIT,
                         authorization: str | None = Header(default=None)):
        machine = _machine(inv, authorization)
        deadline = min(wait, POLL_MAX_WAIT)
        waited = 0.0
        while True:
            claimed = jobs.claim_next_job(conn, inv, machine)
            if claimed is not None:
                return {"type": "job", "job": claimed}
            if db.take_check_requested(conn, machine):
                return {"type": "check"}
            if waited >= deadline:
                return Response(status_code=204)
            await asyncio.sleep(POLL_TICK)
            waited += POLL_TICK

    @r.post("/jobs/{job_id}/log")
    async def agent_log(job_id: int, request: Request,
                        authorization: str | None = Header(default=None)):
        _machine(inv, authorization)
        body = await request.json()
        for line in body.get("lines", []):
            db.append_job_log(conn, job_id, line)
        return Response(status_code=204)

    @r.post("/jobs/{job_id}/result")
    async def agent_result(job_id: int, request: Request,
                           authorization: str | None = Header(default=None)):
        _machine(inv, authorization)
        body = await request.json()
        jobs.record_result(conn, inv, job_id, body.get("status", "failed"),
                           body.get("exit_code", -1), body.get("new_version"))
        return dict(db.get_job(conn, job_id))

    @r.get("/jobs/{job_id}/control")
    def agent_control(job_id: int, authorization: str | None = Header(default=None)):
        _machine(inv, authorization)
        return {"abort": db.abort_requested(conn, job_id)}

    return r
```

- [ ] **Step 4: 在 app.py 掛入 agent router**

In `cockpit/web/app.py` 的 `create_app`，於 `return app` 與 static mount **之前**加入：

```python
    from cockpit.web.agent import build_agent_router
    app.include_router(build_agent_router(conn, inv))
```
(放在所有 `/api` 路由之後、`if STATIC_DIR.exists():` static mount 之前，確保 static catch-all 仍在最後。)

- [ ] **Step 5: 跑測試確認通過**

Run: `python -m pytest tests/test_agent_api.py -v`
Expected: PASS（7 passed）。

- [ ] **Step 6: 全套件 + Commit**

```bash
python -m pytest -q
git add cockpit/web/agent.py cockpit/web/app.py tests/test_agent_api.py && git commit -m "feat: agent-facing API (poll/report/log/result/control) with token auth"
```
Expected: 全綠（77 passed）。

---

### Task 6: app.py 切到佇列模型（trigger 不再 spawn、abort 走 request_abort、check 改 refresh+flag、SSE 收 aborted）

**Files:**
- Modify: `cockpit/cockpit/web/app.py`
- Modify: `cockpit/tests/test_web.py`
- Test: `cockpit/tests/test_web_abort_check.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_web_abort_check.py`:

```python
from fastapi.testclient import TestClient
from cockpit import db, jobs
from cockpit.web.app import create_app
from cockpit.models import Machine, Update, Install, Software, Inventory


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True, agent_token="tok-mac")
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="x"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _app(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    db.add_version(c, "cc", "2.1.101", None, "raw", "中文")
    db.upsert_install(c, "cc", "mac", "2.1.98", "behind", "t")
    return create_app(c, _inv()), c


def test_browser_abort_queued(tmp_path):
    app, c = _app(tmp_path)
    jid = jobs.start_job(c, _inv(), "cc", "mac")
    r = TestClient(app).post(f"/api/jobs/{jid}/abort")
    assert r.status_code == 200 and r.json()["status"] == "aborted"


def test_browser_abort_running_sets_flag(tmp_path):
    app, c = _app(tmp_path)
    jid = jobs.start_job(c, _inv(), "cc", "mac")
    jobs.claim_next_job(c, _inv(), "mac")          # running
    r = TestClient(app).post(f"/api/jobs/{jid}/abort")
    assert r.status_code == 200 and r.json()["status"] == "running"
    assert db.abort_requested(c, jid) is True


def test_check_sets_machine_flags(tmp_path):
    app, c = _app(tmp_path)
    r = TestClient(app).post("/api/check")
    assert r.status_code == 200 and r.json()["started"] is True
    # mac 被設旗標（agent 下次 poll 會拿到 check）
    assert db.take_check_requested(c, "mac") is True


def test_trigger_update_enqueues_only(tmp_path):
    app, c = _app(tmp_path)
    r = TestClient(app).post("/api/installs/cc/mac/update")
    assert r.status_code == 200
    jid = r.json()["job_id"]
    job = db.get_job(c, jid)
    assert job["software"] == "cc"
    assert job["status"] == "queued"        # 不再就地執行；等 agent claim
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_web_abort_check.py -v`
Expected: FAIL（abort 走舊邏輯 / check 未設旗標）。

- [ ] **Step 3: 改 app.py 切到佇列模型（4 處）**

In `cockpit/web/app.py`：

(a) `trigger_update` 不再就地執行——只建 queued job 等 agent claim。**刪除 `trigger_update` 內的 `_spawn_job(conn, inv, jid)` 呼叫**，並**刪除模組層的 `_spawn_job` 函式定義**（連同其 `import threading` 若僅此處用）。改後：

```python
    @app.post("/api/installs/{software}/{machine}/update")
    def trigger_update(software: str, machine: str):
        try:
            jid = jobs.start_job(conn, inv, software, machine)
        except KeyError:
            raise HTTPException(404, "install not found")
        except jobs.ActiveJobExists:
            raise HTTPException(409, "update already in progress")
        return {"job_id": jid}
```

(b) `abort_update`（若無則新增；FE-A 後尚無 abort 路由）改呼叫 `jobs.request_abort`：

```python
    @app.post("/api/jobs/{job_id}/abort")
    def abort_update(job_id: int):
        job = jobs.request_abort(conn, job_id)
        if job is None:
            raise HTTPException(404, "job not found")
        return job
```

(c) `POST /api/check` 改為：背景跑一次上游刷新 + 對每台機器設 check 旗標（移除對已不存在的 `run_collection`/`_spawn_job` 的引用）：

```python
    @app.post("/api/check")
    def check():
        import threading
        from cockpit.collector import refresh_upstream
        threading.Thread(target=refresh_upstream, args=(conn, inv), daemon=True).start()
        for name in inv.machines:
            db.set_check_requested(conn, name)
        return {"started": True}
```

(d) SSE `stream_log` 終止狀態加入 `aborted`：把 `if job["status"] in ("success", "failed"):` 改為 `if job["status"] in ("success", "failed", "aborted"):`。

- [ ] **Step 4: 更新 test_web.py（移除對已刪 `_spawn_job` 的 monkeypatch）**

In `cockpit/tests/test_web.py`，`test_trigger_update_creates_job` 與 `test_trigger_update_conflict_returns_409` 內的 `monkeypatch.setattr(webapp, "_spawn_job", ...)` 行已無對象——刪除該行（job 改為留 queued，斷言不變仍成立）。對應地，這兩個測試的函式簽名可移除 `monkeypatch` 參數與 `import cockpit.web.app as webapp` 行（若不再使用）。

Run: `python -m pytest tests/test_web.py tests/test_web_abort_check.py -v`
Expected: PASS（test_web.py 既有測試仍綠 + 4 new in test_web_abort_check）。

- [ ] **Step 5: 全套件 + Commit**

```bash
python -m pytest -q
git add cockpit/web/app.py tests/test_web.py tests/test_web_abort_check.py && git commit -m "feat: app.py queue model — trigger enqueues only, abort via request_abort, check flags machines, SSE ends on aborted"
```
Expected: 全綠（無回歸；數量隨新增測試增加）。

---

### Task 7: main.py 接線（排程跑 refresh_upstream、移除 SSH 依賴）

**Files:**
- Modify: `cockpit/cockpit/main.py`
- Modify: `cockpit/pyproject.toml`
- Delete: `cockpit/cockpit/runner.py` + `cockpit/tests/test_runner.py`
- Test: `cockpit/tests/test_main_smoke.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_main_smoke.py`:

```python
def test_build_uses_refresh_upstream(monkeypatch, tmp_path):
    import cockpit.main as m
    from cockpit.collector import refresh_upstream
    # build() 應以 refresh_upstream 當排程目標（檢查 import 存在且可呼叫）
    assert callable(refresh_upstream)
    # main 模組可在提供 inventory 時 import（避免硬連 SSH）
    assert hasattr(m, "build")
```

- [ ] **Step 2: 跑測試確認失敗 / 或確認 import 問題**

Run: `python -m pytest tests/test_main_smoke.py -v`
Expected: FAIL or ERROR（main 仍 import 已刪 collector.run_collection / runner）。

- [ ] **Step 3: 改 main.py**

Replace `cockpit/main.py`：

```python
from __future__ import annotations
import uvicorn

from cockpit import db
from cockpit.config import Settings
from cockpit.inventory import load_inventory
from cockpit.collector import refresh_upstream
from cockpit.scheduler import build_scheduler
from cockpit.web.app import create_app


def build():
    settings = Settings.from_env()
    conn = db.connect(settings.db_path)
    db.init_db(conn)
    inv = load_inventory(settings.inventory_path)
    app = create_app(conn, inv)

    sch = build_scheduler(lambda: refresh_upstream(conn, inv), hours=settings.check_hours)
    sch.start()
    return app, settings


app, _settings = build()


def main():
    uvicorn.run(app, host="127.0.0.1", port=8787)


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: 刪除 runner（server 不再執行）**

```bash
cd /Users/curtis/Dev/cockpit && git rm cockpit/runner.py tests/test_runner.py
```
從 `pyproject.toml` 的 `dependencies` 移除 `"paramiko>=3.4",`（server 不再 SSH）。確認其餘檔案無 `import paramiko` 或 `from cockpit.runner` 殘留：
```bash
grep -rn "paramiko\|cockpit.runner\|from cockpit import runner" cockpit/ tests/ || echo "clean"
```
若有殘留則一併移除。

- [ ] **Step 5: 跑測試確認通過 + 全套件**

Run: `python -m pytest -q`
Expected: 全綠（移除整個 `test_runner.py` 後、加 1 個 smoke；無其他回歸）。

- [ ] **Step 6: Commit**

```bash
git add cockpit/main.py cockpit/pyproject.toml tests/test_main_smoke.py && git rm cockpit/runner.py tests/test_runner.py && git commit -m "feat: scheduler runs refresh_upstream; remove SSH runner from server"
```

---

# Part B — Agent（Go static binary）

> 前置：在 `/Users/curtis/Dev/cockpit/agent/` 建立 Go 專案。需 Go 1.22+（`go version`）。每個 Go 任務以 `cd /Users/curtis/Dev/cockpit/agent && go test ./...` 驗證。

### Task 8: Go 專案骨架與 config

**Files:**
- Create: `agent/go.mod`
- Create: `agent/internal/config/config.go`
- Test: `agent/internal/config/config_test.go`

- [ ] **Step 1: 建 go.mod**

```bash
cd /Users/curtis/Dev/cockpit && mkdir -p agent/internal/config && cd agent && cat > go.mod <<'EOF'
module cockpit-agent

go 1.22
EOF
```

- [ ] **Step 2: 寫失敗測試**

Create `agent/internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`{"server_url":"https://cockpit.example","agent_token":"tok"}`), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "https://cockpit.example" || c.AgentToken != "tok" {
		t.Fatalf("bad parse: %+v", c)
	}
	if c.PollTimeoutSec != 25 || c.ReportIntervalSec != 3600 || c.ControlIntervalSec != 2 {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`{"server_url":""}`), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for missing fields")
	}
}
```

- [ ] **Step 3: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit/agent && go test ./internal/config/`
Expected: FAIL（package has no Load）。

- [ ] **Step 4: 實作 config.go**

Create `agent/internal/config/config.go`:

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	ServerURL          string   `json:"server_url"`
	AgentToken         string   `json:"agent_token"`
	BeszelCmd          string   `json:"beszel_cmd"`
	BeszelArgs         []string `json:"beszel_args"`
	PollTimeoutSec     int      `json:"poll_timeout_sec"`
	ReportIntervalSec  int      `json:"report_interval_sec"`
	ControlIntervalSec int      `json:"control_interval_sec"`
	ExecTimeoutSec     int      `json:"exec_timeout_sec"`
}

func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.ServerURL == "" || c.AgentToken == "" {
		return c, fmt.Errorf("config: server_url and agent_token are required")
	}
	if c.PollTimeoutSec == 0 {
		c.PollTimeoutSec = 25
	}
	if c.ReportIntervalSec == 0 {
		c.ReportIntervalSec = 3600
	}
	if c.ControlIntervalSec == 0 {
		c.ControlIntervalSec = 2
	}
	if c.ExecTimeoutSec == 0 {
		c.ExecTimeoutSec = 900
	}
	return c, nil
}
```

- [ ] **Step 5: 跑測試確認通過 + Commit**

```bash
cd /Users/curtis/Dev/cockpit/agent && go test ./internal/config/ && go vet ./...
cd /Users/curtis/Dev/cockpit && git add agent/go.mod agent/internal/config/ && git commit -m "feat(agent): go scaffold and config"
```
Expected: ok。

---

### Task 9: HTTP client（auth + long-poll + 退避）

**Files:**
- Create: `agent/internal/httpclient/client.go`
- Test: `agent/internal/httpclient/client_test.go`

- [ ] **Step 1: 寫失敗測試**

Create `agent/internal/httpclient/client_test.go`:

```go
package httpclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetJSONSendsAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", 5*time.Second)
	var out map[string]string
	status, err := c.GetJSON("/x", &out)
	if err != nil || status != 200 || out["hello"] != "world" {
		t.Fatalf("status=%d err=%v out=%v", status, err, out)
	}
}

func TestGet204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", 5*time.Second)
	var out map[string]string
	status, err := c.GetJSON("/x", &out)
	if err != nil || status != 204 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestPostJSON(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", 5*time.Second)
	status, err := c.PostJSON("/y", map[string]string{"a": "b"}, nil)
	if err != nil || status != 204 || got["a"] != "b" {
		t.Fatalf("status=%d err=%v got=%v", status, err, got)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit/agent && go test ./internal/httpclient/`
Expected: FAIL（undefined: New）。

- [ ] **Step 3: 實作 client.go**

Create `agent/internal/httpclient/client.go`:

```go
package httpclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(base, token string, timeout time.Duration) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: timeout},
	}
}

func (c *Client) do(req *http.Request, out any) (int, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return 204, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return resp.StatusCode, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (c *Client) GetJSON(path string, out any) (int, error) {
	req, err := http.NewRequest("GET", c.base+path, nil)
	if err != nil {
		return 0, err
	}
	return c.do(req, out)
}

func (c *Client) PostJSON(path string, body any, out any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", c.base+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}
```

- [ ] **Step 4: 跑測試確認通過 + Commit**

```bash
cd /Users/curtis/Dev/cockpit/agent && go test ./internal/httpclient/ && go vet ./...
cd /Users/curtis/Dev/cockpit && git add agent/internal/httpclient/ && git commit -m "feat(agent): http client with bearer auth"
```

---

### Task 10: executor（bash -lc 串流 + timeout + cancel）

**Files:**
- Create: `agent/internal/executor/executor.go`
- Test: `agent/internal/executor/executor_test.go`

- [ ] **Step 1: 寫失敗測試**

Create `agent/internal/executor/executor_test.go`:

```go
package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunStreamsAndExit(t *testing.T) {
	var lines []string
	res := Run(context.Background(), "echo hello && echo world", "", 10*time.Second,
		func(l string) { lines = append(lines, l) })
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if strings.Join(lines, ",") != "hello,world" {
		t.Fatalf("lines=%v", lines)
	}
}

func TestRunNonzero(t *testing.T) {
	res := Run(context.Background(), "exit 3", "", 10*time.Second, nil)
	if res.ExitCode != 3 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
}

func TestRunCancelKills(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	res := Run(ctx, "sleep 5", "", 10*time.Second, nil)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("cancel did not kill promptly")
	}
	if res.ExitCode == 0 {
		t.Fatalf("expected nonzero on cancel")
	}
}

func TestRunTimeout(t *testing.T) {
	start := time.Now()
	res := Run(context.Background(), "sleep 5", "", 1*time.Second, nil)
	if time.Since(start) > 3*time.Second || res.ExitCode == 0 {
		t.Fatalf("timeout not enforced: dur=%v exit=%d", time.Since(start), res.ExitCode)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit/agent && go test ./internal/executor/`
Expected: FAIL（undefined: Run）。

- [ ] **Step 3: 實作 executor.go**

Create `agent/internal/executor/executor.go`:

```go
package executor

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Result struct {
	ExitCode int
}

// Run executes `bash -lc cmd` in its own process group, streaming stdout/stderr
// lines to onLine. The process is killed when ctx is cancelled or timeout elapses.
func Run(ctx context.Context, cmd, cwd string, timeout time.Duration, onLine func(string)) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.Command("bash", "-lc", cmd)
	if cwd != "" {
		c.Dir = cwd
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group → kill children too
	stdout, err := c.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1}
	}
	c.Stderr = c.Stdout // merge stderr into stdout

	if err := c.Start(); err != nil {
		return Result{ExitCode: -1}
	}

	// kill the whole process group when ctx ends
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if c.Process != nil {
				syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if onLine != nil {
			onLine(line)
		}
	}
	err = c.Wait()
	close(done)

	return Result{ExitCode: exitCode(err)}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
	}
	return -1
}
```

- [ ] **Step 4: 跑測試確認通過 + Commit**

```bash
cd /Users/curtis/Dev/cockpit/agent && go test ./internal/executor/ && go vet ./...
cd /Users/curtis/Dev/cockpit && git add agent/internal/executor/ && git commit -m "feat(agent): streaming command executor with timeout and group-kill"
```

---

### Task 11: version reporter

**Files:**
- Create: `agent/internal/reporter/reporter.go`
- Test: `agent/internal/reporter/reporter_test.go`

- [ ] **Step 1: 寫失敗測試**

Create `agent/internal/reporter/reporter_test.go`:

```go
package reporter

import "testing"

func TestParseVersionSemver(t *testing.T) {
	if v := ParseVersion("claude 2.1.98 (Claude Code)", ""); v != "2.1.98" {
		t.Fatalf("got %q", v)
	}
	if v := ParseVersion("v0.9.0", ""); v != "0.9.0" {
		t.Fatalf("got %q", v)
	}
}

func TestParseVersionCustomRegex(t *testing.T) {
	if v := ParseVersion("image: multica:0.8.2", `multica:([0-9.]+)`); v != "0.8.2" {
		t.Fatalf("got %q", v)
	}
	// 無 capture group → 回整段 match
	if v := ParseVersion("app:1.2.3", `app:[0-9.]+`); v != "app:1.2.3" {
		t.Fatalf("got %q", v)
	}
	// 非法 regex → 空字串
	if v := ParseVersion("whatever", `([0-9`); v != "" {
		t.Fatalf("got %q", v)
	}
}

func TestParseNone(t *testing.T) {
	if v := ParseVersion("no version here", ""); v != "" {
		t.Fatalf("got %q", v)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit/agent && go test ./internal/reporter/`
Expected: FAIL（undefined: ParseVersion）。

- [ ] **Step 3: 實作 reporter.go**

Create `agent/internal/reporter/reporter.go`:

```go
package reporter

import "regexp"

var semver = regexp.MustCompile(`(\d+(?:\.\d+){1,3})`)

// ParseVersion mirrors the Python server's version_parse.parse_version:
// uses the custom regex when given (whole match if it has no capture group),
// else the default semver pattern. Returns "" when nothing matches or regex invalid.
func ParseVersion(text, customRegex string) string {
	re := semver
	group := 1
	if customRegex != "" {
		r, err := regexp.Compile(customRegex)
		if err != nil {
			return ""
		}
		re = r
		if re.NumSubexp() == 0 {
			group = 0
		}
	}
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[group]
}
```

- [ ] **Step 4: 跑測試確認通過 + Commit**

```bash
cd /Users/curtis/Dev/cockpit/agent && go test ./internal/reporter/ && go vet ./...
cd /Users/curtis/Dev/cockpit && git add agent/internal/reporter/ && git commit -m "feat(agent): version parsing mirroring server semantics"
```

---

### Task 12: job runner（poll→exec→log→control/abort→verify→result）

**Files:**
- Create: `agent/internal/jobrunner/jobrunner.go`
- Test: `agent/internal/jobrunner/jobrunner_test.go`

- [ ] **Step 1: 寫失敗測試**（對 httptest mock server 跑一個 command 型 job 全流程）

Create `agent/internal/jobrunner/jobrunner_test.go`:

```go
package jobrunner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cockpit-agent/internal/httpclient"
)

func TestRunJobCommandFlow(t *testing.T) {
	var mu sync.Mutex
	logs := []string{}
	var result map[string]any
	served := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			var body struct{ Lines []string `json:"lines"` }
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock(); logs = append(logs, body.Lines...); mu.Unlock()
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/result"):
			json.NewDecoder(r.Body).Decode(&result)
			w.WriteHeader(200); w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/control"):
			json.NewEncoder(w).Encode(map[string]bool{"abort": false})
		}
		served = true
	}))
	defer srv.Close()

	c := httpclient.New(srv.URL, "tok", 5*time.Second)
	job := Job{ID: 7, ShellCmd: "echo added 1 package", CurrentCmd: "echo cc 2.1.101", VersionRegex: ""}
	RunJob(c, job, 2*time.Second, 10*time.Second)

	mu.Lock(); defer mu.Unlock()
	if !served || len(logs) == 0 || logs[0] != "added 1 package" {
		t.Fatalf("logs=%v", logs)
	}
	if result["status"] != "success" || result["new_version"] != "2.1.101" {
		t.Fatalf("result=%v", result)
	}
}

func TestRunJobAbort(t *testing.T) {
	var result map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/result"):
			json.NewDecoder(r.Body).Decode(&result)
			w.WriteHeader(200); w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/control"):
			json.NewEncoder(w).Encode(map[string]bool{"abort": true}) // 立即要求中止
		}
	}))
	defer srv.Close()

	c := httpclient.New(srv.URL, "tok", 5*time.Second)
	job := Job{ID: 8, ShellCmd: "sleep 5", CurrentCmd: "echo x", VersionRegex: ""}
	start := time.Now()
	RunJob(c, job, 200*time.Millisecond, 10*time.Second)
	if time.Since(start) > 4*time.Second {
		t.Fatalf("abort too slow")
	}
	if result["status"] != "aborted" {
		t.Fatalf("result=%v", result)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit/agent && go test ./internal/jobrunner/`
Expected: FAIL（undefined: Job/RunJob）。

- [ ] **Step 3: 實作 jobrunner.go**

Create `agent/internal/jobrunner/jobrunner.go`:

```go
package jobrunner

import (
	"context"
	"fmt"
	"time"

	"cockpit-agent/internal/executor"
	"cockpit-agent/internal/httpclient"
	"cockpit-agent/internal/reporter"
)

type Job struct {
	ID           int    `json:"id"`
	Software     string `json:"software"`
	Machine      string `json:"machine"`
	ShellCmd     string `json:"shell_cmd"`
	Cwd          string `json:"cwd"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}

// RunJob executes the server-rendered command, streams log lines, polls the
// control endpoint for abort, verifies the new version on success, and reports
// the result. Single job at a time (caller guarantees).
func RunJob(c *httpclient.Client, job Job, controlInterval, execTimeout time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aborted := make(chan struct{}) // closed by control poller on abort (synchronizes the signal)

	// control poller → cancels exec on abort
	go func() {
		t := time.NewTicker(controlInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				var ctrl struct{ Abort bool `json:"abort"` }
				if _, err := c.GetJSON(fmt.Sprintf("/api/agent/jobs/%d/control", job.ID), &ctrl); err == nil && ctrl.Abort {
					close(aborted)
					cancel()
					return
				}
			}
		}
	}()

	postLine := func(line string) {
		c.PostJSON(fmt.Sprintf("/api/agent/jobs/%d/log", job.ID),
			map[string]any{"lines": []string{line}}, nil)
	}

	res := executor.Run(ctx, job.ShellCmd, job.Cwd, execTimeout, postLine)

	select {
	case <-aborted: // abort signalled (channel close happens-before this read)
		postLine("■ 已由使用者中止")
		report(c, job.ID, "aborted", res.ExitCode, "")
		return
	default:
	}

	if res.ExitCode != 0 {
		report(c, job.ID, "failed", res.ExitCode, "")
		return
	}

	// verify new version
	newVersion := ""
	vres := executor.Run(context.Background(), job.CurrentCmd, job.Cwd, execTimeout, func(l string) {
		if v := reporter.ParseVersion(l, job.VersionRegex); v != "" && newVersion == "" {
			newVersion = v
		}
	})
	_ = vres
	report(c, job.ID, "success", res.ExitCode, newVersion)
}

func report(c *httpclient.Client, jobID int, status string, exit int, newVersion string) {
	c.PostJSON(fmt.Sprintf("/api/agent/jobs/%d/result", jobID),
		map[string]any{"status": status, "exit_code": exit, "new_version": newVersion}, nil)
}
```

- [ ] **Step 4: 跑測試確認通過 + Commit**

```bash
cd /Users/curtis/Dev/cockpit/agent && go test ./internal/jobrunner/ && go vet ./...
cd /Users/curtis/Dev/cockpit && git add agent/internal/jobrunner/ && git commit -m "feat(agent): job runner with log streaming, abort, and verify"
```

---

### Task 13: beszel supervisor

**Files:**
- Create: `agent/internal/supervisor/supervisor.go`
- Test: `agent/internal/supervisor/supervisor_test.go`

- [ ] **Step 1: 寫失敗測試**

Create `agent/internal/supervisor/supervisor_test.go`:

```go
package supervisor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSuperviseRestarts(t *testing.T) {
	var starts int32
	ctx, cancel := context.WithCancel(context.Background())
	// 用一個會立刻結束的指令；supervisor 應重啟它幾次
	s := New("bash", []string{"-lc", "true"}, 50*time.Millisecond)
	s.onStart = func() { atomic.AddInt32(&starts, 1) }
	go s.Run(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()
	if atomic.LoadInt32(&starts) < 2 {
		t.Fatalf("expected multiple restarts, got %d", starts)
	}
}

func TestSuperviseNoCmdIsNoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New("", nil, 50*time.Millisecond)
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("empty supervisor should return immediately")
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit/agent && go test ./internal/supervisor/`
Expected: FAIL（undefined: New）。

- [ ] **Step 3: 實作 supervisor.go**

Create `agent/internal/supervisor/supervisor.go`:

```go
package supervisor

import (
	"context"
	"os/exec"
	"time"
)

type Supervisor struct {
	cmd     string
	args    []string
	backoff time.Duration
	onStart func() // test hook
}

func New(cmd string, args []string, backoff time.Duration) *Supervisor {
	return &Supervisor{cmd: cmd, args: args, backoff: backoff}
}

// Run keeps the child process alive, restarting it after backoff when it exits,
// until ctx is cancelled. If cmd is empty it returns immediately (Beszel optional).
func (s *Supervisor) Run(ctx context.Context) {
	if s.cmd == "" {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if s.onStart != nil {
			s.onStart()
		}
		c := exec.CommandContext(ctx, s.cmd, s.args...)
		_ = c.Start()
		_ = c.Wait()
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.backoff):
		}
	}
}
```

- [ ] **Step 4: 跑測試確認通過 + Commit**

```bash
cd /Users/curtis/Dev/cockpit/agent && go test ./internal/supervisor/ && go vet ./...
cd /Users/curtis/Dev/cockpit && git add agent/internal/supervisor/ && git commit -m "feat(agent): beszel process supervisor"
```

---

### Task 14: main 主迴圈接線 + 整合建置

**Files:**
- Create: `agent/main.go`
- Test: `agent/main_test.go`

- [ ] **Step 1: 寫失敗測試**（整合：對 httptest server 跑一輪 poll→check→report）

Create `agent/main_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cockpit-agent/internal/httpclient"
)

func TestPollOnceHandlesCheck(t *testing.T) {
	var reported int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/poll":
			json.NewEncoder(w).Encode(map[string]any{"type": "check"})
		case "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{
				{"software": "cc", "current_cmd": "echo cc 2.1.98", "version_regex": nil},
			})
		case "/api/agent/report-versions":
			atomic.AddInt32(&reported, 1)
			json.NewEncoder(w).Encode(map[string]int{"applied": 1})
		}
	}))
	defer srv.Close()

	c := httpclient.New(srv.URL, "tok", 5*time.Second)
	// pollOnce 應在收到 check 時跑 installs + report-versions
	pollOnce(c, 5*time.Second)
	if atomic.LoadInt32(&reported) != 1 {
		t.Fatalf("expected one report, got %d", reported)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit/agent && go test .`
Expected: FAIL（undefined: pollOnce）。

- [ ] **Step 3: 實作 main.go**

Create `agent/main.go`:

```go
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"cockpit-agent/internal/config"
	"cockpit-agent/internal/executor"
	"cockpit-agent/internal/httpclient"
	"cockpit-agent/internal/jobrunner"
	"cockpit-agent/internal/reporter"
	"cockpit-agent/internal/supervisor"
)

func main() {
	cfgPath := flag.String("config", "/etc/cockpit-agent/config.json", "path to config json")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	c := httpclient.New(cfg.ServerURL, cfg.AgentToken,
		time.Duration(cfg.PollTimeoutSec+10)*time.Second)

	ctx := context.Background()

	// beszel supervisor (optional)
	sup := supervisor.New(cfg.BeszelCmd, cfg.BeszelArgs, 5*time.Second)
	go sup.Run(ctx)

	// periodic version report
	go func() {
		for {
			reportVersions(c, time.Duration(cfg.ExecTimeoutSec)*time.Second)
			time.Sleep(time.Duration(cfg.ReportIntervalSec) * time.Second)
		}
	}()

	// main long-poll loop
	execTimeout := time.Duration(cfg.ExecTimeoutSec) * time.Second
	backoff := time.Second
	for {
		if err := pollOnce(c, execTimeout); err != nil {
			log.Printf("poll error: %v (backoff %v)", err, backoff)
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

// pollOnce does one long-poll; on job it runs it (bounded by execTimeout);
// on check it reports versions. The long-poll wait (25s) is bounded by the
// http client timeout configured in main (PollTimeoutSec + 10).
func pollOnce(c *httpclient.Client, execTimeout time.Duration) error {
	var resp struct {
		Type string        `json:"type"`
		Job  jobrunner.Job `json:"job"`
	}
	status, err := c.GetJSON("/api/agent/poll?wait=25", &resp)
	if err != nil {
		return err
	}
	if status == 204 {
		return nil
	}
	switch resp.Type {
	case "job":
		jobrunner.RunJob(c, resp.Job, 2*time.Second, execTimeout)
	case "check":
		reportVersions(c, execTimeout)
	}
	return nil
}

type installDef struct {
	Software     string `json:"software"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}

func reportVersions(c *httpclient.Client, execTimeout time.Duration) {
	var defs []installDef
	if _, err := c.GetJSON("/api/agent/installs", &defs); err != nil {
		log.Printf("installs error: %v", err)
		return
	}
	var reports []map[string]string
	for _, d := range defs {
		cur := ""
		executor.Run(context.Background(), d.CurrentCmd, "", execTimeout, func(l string) {
			if v := reporter.ParseVersion(l, d.VersionRegex); v != "" && cur == "" {
				cur = v
			}
		})
		reports = append(reports, map[string]string{"software": d.Software, "current_version": cur})
	}
	if len(reports) > 0 {
		c.PostJSON("/api/agent/report-versions", reports, nil)
	}
}
```

- [ ] **Step 4: 跑測試確認通過 + build**

```bash
cd /Users/curtis/Dev/cockpit/agent && go test ./... && go vet ./... && go build -o /tmp/cockpit-agent .
```
Expected: 全 package PASS、build 成功。

- [ ] **Step 5: Commit**

```bash
cd /Users/curtis/Dev/cockpit && git add agent/main.go agent/main_test.go && git commit -m "feat(agent): main loop wiring (poll/report) + integration test"
```

---

### Task 15: 部署範本與文件

**Files:**
- Create: `agent/deploy/cockpit-agent.service`
- Create: `agent/deploy/cockpit-agent.plist`
- Create: `agent/deploy/config.example.json`
- Modify: `cockpit/inventory.example.yaml`
- Modify: `cockpit/docs/deploy.md`

- [ ] **Step 1: agent config 範例**

Create `agent/deploy/config.example.json`:

```json
{
  "server_url": "https://cockpit.example.co",
  "agent_token": "REPLACE_WITH_PER_MACHINE_TOKEN",
  "beszel_cmd": "/usr/local/bin/beszel-agent",
  "beszel_args": [],
  "poll_timeout_sec": 25,
  "report_interval_sec": 3600,
  "control_interval_sec": 2,
  "exec_timeout_sec": 900
}
```

- [ ] **Step 2: systemd 範本**

Create `agent/deploy/cockpit-agent.service`:

```ini
[Unit]
Description=cockpit-agent (version tracker + beszel supervisor)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/cockpit-agent -config /etc/cockpit-agent/config.json
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 3: launchd 範本**

Create `agent/deploy/cockpit-agent.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>co.sitruc.cockpit-agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/cockpit-agent</string>
    <string>-config</string>
    <string>/usr/local/etc/cockpit-agent/config.json</string>
  </array>
  <key>KeepAlive</key><true/>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
```

- [ ] **Step 4: inventory.example.yaml 加 agent_token 註解**

In `cockpit/inventory.example.yaml`，於每台 machine 加註解示範（不放真值）：

```yaml
machines:
  mac:        { host: 192.168.1.10, ssh_user: curtis, local: true }   # agent_token: <每機唯一 token，僅放真實 inventory.yaml>
  ubuntu_llm: { host: 100.0.0.0, ssh_user: curtis }                     # agent_token: <...>
  macmini:    { host: 192.168.1.10, ssh_user: curtis }                # agent_token: <...>
```

- [ ] **Step 5: 更新 docs/deploy.md**

Append an "Agent daemon" section to `cockpit/docs/deploy.md`:

````markdown
## 6. cockpit-agent（每台機器）

每台機器跑一隻 `cockpit-agent`（取代獨立 beszel service，改由 agent 督管 beszel-agent）。

```bash
# 在開發機 build（或交叉編譯）
cd agent && go build -o cockpit-agent .
# 部署到目標機
scp cockpit-agent target:/usr/local/bin/cockpit-agent
# 設定（每機唯一 agent_token，需同時寫入 server 的真實 inventory.yaml 該機 agent_token）
sudo mkdir -p /etc/cockpit-agent && sudo cp deploy/config.example.json /etc/cockpit-agent/config.json && sudo vi /etc/cockpit-agent/config.json
# Linux: systemd
sudo cp deploy/cockpit-agent.service /etc/systemd/system/ && sudo systemctl enable --now cockpit-agent
# macOS: launchd
sudo cp deploy/cockpit-agent.plist /Library/LaunchDaemons/co.sitruc.cockpit-agent.plist && sudo launchctl load /Library/LaunchDaemons/co.sitruc.cockpit-agent.plist
```

- Cloudflare：`/api/agent/*` 設 Access **Bypass**（agent 以 app 層 Bearer token 把關），其餘路徑維持 Bypass(信任IP)/Allow(登入)。
- 首次升級需刪舊 `cockpit.db`（schema 加欄位）。
````

- [ ] **Step 6: Commit**

```bash
cd /Users/curtis/Dev/cockpit && git add agent/deploy/ cockpit/inventory.example.yaml cockpit/docs/deploy.md && git commit -m "docs: cockpit-agent deploy templates and guide"
```

---

## 完成後

全部 server 任務後：`python -m pytest -q` 應全綠（約 78）。全部 agent 任務後：`cd agent && go test ./... && go build` 成功。

接著回到前端整合（FE-C inventory 編輯端點、FE-D 接線、FE-E Chrome 驗證）——前端與本 plan 解耦，可獨立進行。
