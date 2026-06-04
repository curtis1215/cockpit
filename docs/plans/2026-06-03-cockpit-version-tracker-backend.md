# Cockpit 版本追蹤器（後端）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 實作 cockpit 子系統 2 的後端：從 YAML 清單跨機器讀軟體版本、比對上游最新版、翻譯 changelog 成繁中、並在 Web UI 觸發更新（command 或 agent 型）以 job 形式執行並即時串流 log。

**Architecture:** Python + FastAPI 單進程服務。YAML inventory 為定義的單一事實來源（載入記憶體）；SQLite 只存「狀態」（installs 目前版、versions 歷史、jobs、events）。APScheduler 週期跑 collector；更新以 job 背景執行，log 增量寫入 DB，前端用 SSE 拉取。所有遠端執行統一走「shell 字串 + 本地 subprocess 或 SSH over Tailscale」。

**Tech Stack:** Python 3.11+、FastAPI、uvicorn、APScheduler、httpx（含內建 MockTransport 供測試）、paramiko（SSH）、PyYAML、sse-starlette、pytest。前端（靜態 prototype）由 claude design 另外產出，本計畫只負責後端 + API + SSE，並預留 `web/static/` 放前端產物。

---

## 慣例與型別約定（跨任務一致，請勿改名）

- **狀態字串**：install status ∈ `up_to_date|behind|unknown|error`；job status ∈ `queued|running|success|failed`；update type ∈ `command|agent`；runner ∈ `codex_exec|claude_p|custom`；provider ∈ `npm|github|pypi|brew|claude-plugin|custom`。
- **模型欄位**（`cockpit/models.py`）：
  - `Machine(name, host, ssh_user, local=False)`
  - `Update(type, cmd=None, runner=None, prompt=None, machine=None, cwd=None, invoke=None)`
  - `Install(machine, current_cmd, update, version_regex=None)`
  - `Software(name, kind, latest_source, changelog, installs)`
  - `Inventory(machines: dict[str, Machine], software: list[Software])`
- **DB 函式**（`cockpit/db.py`，全部第一參數 `conn`）：`connect(path)`、`init_db(conn)`、`upsert_install(conn, software, machine, current_version, status, last_checked)`、`list_installs(conn)`、`add_version(conn, software, version, released_at, changelog_raw, changelog_zh)`、`get_version(conn, software, version)`、`create_job(conn, software, machine, kind, runner=None)`、`set_job_running(conn, job_id)`、`append_job_log(conn, job_id, line)`、`finish_job(conn, job_id, status, exit_code, new_version=None)`、`get_job(conn, job_id)`、`add_event(conn, type, software, machine, detail)`。
- **執行器**（`cockpit/runner.py`）：`ExecResult(exit_code, output)`；`execute(machine, shell_cmd, cwd=None, on_line=None, timeout=900) -> ExecResult`。
- **版本來源**（`cockpit/sources/`）：`SourceResult(version, changelog_raw)`；`fetch_latest(software, client=None) -> SourceResult`。
- **版本解析**：`parse_version(text, regex=None) -> str | None`；`compare(current, latest) -> tuple[str, int]`（回 `(status, behind_count)`）。

> **與 spec 的差異**：spec 4.2 列了 `software` 表；本實作把「軟體定義」留在 YAML（記憶體），DB 僅持久化狀態（installs/versions/jobs/events）。理由：定義已版控於 YAML，無需在 DB 重複，避免雙寫不一致。

---

## 檔案結構

```
cockpit/
  pyproject.toml
  inventory.example.yaml          # 範例清單（真實清單 inventory.yaml 由 .gitignore 排除）
  cockpit/
    __init__.py
    config.py                     # 環境設定（DB 路徑、inventory 路徑、GitHub token、排程）
    models.py                     # dataclasses + 型別
    inventory.py                  # 解析/驗證 inventory.yaml -> Inventory
    db.py                         # sqlite 連線 + schema 初始化 + CRUD
    schema.sql                    # DDL
    version_parse.py              # 從文字抽 semver、比較版本
    runner.py                     # 本地/SSH 執行 shell 指令（可串流）
    translate.py                  # 用 `claude -p` 翻譯 changelog
    sources/
      __init__.py                 # provider 註冊表 + fetch_latest 分派
      npm.py / github.py / pypi.py / brew.py / claude_plugin.py / custom.py
    collector.py                  # 排程採集：讀現版 + 抓最新 + 比對 + 翻譯 + 落庫
    jobs.py                       # job 引擎：建立/執行 command 與 agent 更新、串流 log、收尾
    scheduler.py                  # APScheduler 接線
    web/
      app.py                      # FastAPI app + API 路由 + 靜態前端
      static/                     # 前端 prototype 產物（claude design 交付）
    main.py                       # 進入點（uvicorn）
  tests/
    conftest.py
    test_*.py
```

---

### Task 1: 專案骨架與測試工具鏈

**Files:**
- Create: `cockpit/pyproject.toml`
- Create: `cockpit/cockpit/__init__.py`
- Create: `cockpit/tests/conftest.py`
- Test: `cockpit/tests/test_smoke.py`

- [ ] **Step 1: 建立 pyproject.toml**

Create `pyproject.toml`:

```toml
[project]
name = "cockpit"
version = "0.1.0"
description = "Homelab software version tracker (subsystem 2)"
requires-python = ">=3.11"
dependencies = [
    "fastapi>=0.110",
    "uvicorn[standard]>=0.29",
    "apscheduler>=3.10",
    "httpx>=0.27",
    "paramiko>=3.4",
    "pyyaml>=6.0",
    "sse-starlette>=2.1",
]

[project.optional-dependencies]
dev = ["pytest>=8.0", "anyio>=4.0"]

[tool.pytest.ini_options]
testpaths = ["tests"]
addopts = "-q"
```

- [ ] **Step 2: 建套件與 smoke 測試**

Create `cockpit/__init__.py`:

```python
__version__ = "0.1.0"
```

Create `tests/conftest.py`:

```python
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
```

Create `tests/test_smoke.py`:

```python
import cockpit


def test_version_present():
    assert cockpit.__version__ == "0.1.0"
```

- [ ] **Step 3: 建立 venv 並安裝**

Run:
```bash
cd /Users/curtis/Dev/cockpit && python3 -m venv .venv && . .venv/bin/activate && pip install -e ".[dev]"
```
Expected: 安裝成功，無錯誤。

- [ ] **Step 4: 跑 smoke 測試**

Run: `cd /Users/curtis/Dev/cockpit && . .venv/bin/activate && python -m pytest tests/test_smoke.py -v`
Expected: PASS（1 passed）。

- [ ] **Step 5: Commit**

```bash
cd /Users/curtis/Dev/cockpit && git add pyproject.toml cockpit/__init__.py tests/ && git commit -m "chore: scaffold python project and test toolchain"
```

---

### Task 2: 領域模型

**Files:**
- Create: `cockpit/cockpit/models.py`
- Test: `cockpit/tests/test_models.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_models.py`:

```python
from cockpit.models import Machine, Update, Install, Software, Inventory


def test_command_install():
    upd = Update(type="command", cmd="npm i -g x@latest")
    inst = Install(machine="mac", current_cmd="x --version", update=upd)
    assert inst.update.type == "command"
    assert inst.update.cmd == "npm i -g x@latest"
    assert inst.version_regex is None


def test_agent_update_fields():
    upd = Update(type="agent", runner="codex_exec", prompt="do it",
                 machine="macmini", cwd="/srv/x", invoke="codex exec --cd {cwd} {prompt}")
    assert upd.runner == "codex_exec"
    assert upd.cwd == "/srv/x"


def test_inventory_container():
    inv = Inventory(
        machines={"mac": Machine(name="mac", host="1.2.3.4", ssh_user="curtis", local=True)},
        software=[Software(name="x", kind="npm", latest_source="npm:x",
                           changelog="github:o/x", installs=[])],
    )
    assert inv.machines["mac"].local is True
    assert inv.software[0].name == "x"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_models.py -v`
Expected: FAIL（ModuleNotFoundError: cockpit.models）。

- [ ] **Step 3: 實作 models.py**

Create `cockpit/models.py`:

```python
from __future__ import annotations
from dataclasses import dataclass, field


@dataclass
class Machine:
    name: str
    host: str
    ssh_user: str
    local: bool = False


@dataclass
class Update:
    type: str                      # "command" | "agent"
    cmd: str | None = None         # command 型
    runner: str | None = None      # agent 型: codex_exec | claude_p | custom
    prompt: str | None = None
    machine: str | None = None     # agent 跑在哪台（預設同 install.machine）
    cwd: str | None = None
    invoke: str | None = None      # custom runner 的指令模板


@dataclass
class Install:
    machine: str
    current_cmd: str
    update: Update
    version_regex: str | None = None


@dataclass
class Software:
    name: str
    kind: str
    latest_source: str
    changelog: str | None
    installs: list[Install] = field(default_factory=list)


@dataclass
class Inventory:
    machines: dict[str, Machine]
    software: list[Software]
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_models.py -v`
Expected: PASS（3 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/models.py tests/test_models.py && git commit -m "feat: add domain models"
```

---

### Task 3: Inventory 載入與驗證

**Files:**
- Create: `cockpit/cockpit/inventory.py`
- Test: `cockpit/tests/test_inventory.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_inventory.py`:

```python
import pytest
from cockpit.inventory import load_inventory, InventoryError

VALID = """
machines:
  mac: { host: 1.2.3.4, ssh_user: curtis, local: true }
  box: { host: 5.6.7.8, ssh_user: root }
software:
  - name: claude-code
    kind: npm
    latest_source: "npm:@anthropic-ai/claude-code"
    changelog: "github:anthropics/claude-code"
    installs:
      - machine: mac
        current_cmd: "claude --version"
        update: { type: command, cmd: "npm i -g @anthropic-ai/claude-code@latest" }
  - name: multica
    kind: custom
    latest_source: "github:o/multica"
    changelog: "github:o/multica"
    installs:
      - machine: box
        current_cmd: "docker inspect multica --format '{{.Config.Labels.version}}'"
        update:
          type: agent
          runner: codex_exec
          cwd: /srv/multica
          prompt: "update multica to {latest_version}"
"""


def test_load_valid(tmp_path):
    p = tmp_path / "inv.yaml"
    p.write_text(VALID)
    inv = load_inventory(p)
    assert set(inv.machines) == {"mac", "box"}
    assert inv.software[0].installs[0].update.type == "command"
    assert inv.software[1].installs[0].update.runner == "codex_exec"


def test_unknown_machine_ref(tmp_path):
    bad = VALID.replace("machine: box", "machine: ghost")
    p = tmp_path / "inv.yaml"
    p.write_text(bad)
    with pytest.raises(InventoryError, match="ghost"):
        load_inventory(p)


def test_agent_requires_runner_and_prompt(tmp_path):
    bad = """
machines: { mac: { host: 1.2.3.4, ssh_user: curtis } }
software:
  - name: x
    kind: custom
    latest_source: "github:o/x"
    changelog: null
    installs:
      - machine: mac
        current_cmd: "x --version"
        update: { type: agent, runner: codex_exec }
"""
    p = tmp_path / "inv.yaml"
    p.write_text(bad)
    with pytest.raises(InventoryError, match="prompt"):
        load_inventory(p)
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_inventory.py -v`
Expected: FAIL（ModuleNotFoundError: cockpit.inventory）。

- [ ] **Step 3: 實作 inventory.py**

Create `cockpit/inventory.py`:

```python
from __future__ import annotations
from pathlib import Path
import yaml

from cockpit.models import Machine, Update, Install, Software, Inventory


class InventoryError(ValueError):
    pass


def _parse_update(raw: dict, ctx: str) -> Update:
    if not isinstance(raw, dict) or "type" not in raw:
        raise InventoryError(f"{ctx}: update 缺少 type")
    t = raw["type"]
    if t == "command":
        if not raw.get("cmd"):
            raise InventoryError(f"{ctx}: command 型 update 需要 cmd")
        return Update(type="command", cmd=raw["cmd"])
    if t == "agent":
        if not raw.get("runner"):
            raise InventoryError(f"{ctx}: agent 型 update 需要 runner")
        if not raw.get("prompt"):
            raise InventoryError(f"{ctx}: agent 型 update 需要 prompt")
        return Update(
            type="agent", runner=raw["runner"], prompt=raw["prompt"],
            machine=raw.get("machine"), cwd=raw.get("cwd"), invoke=raw.get("invoke"),
        )
    raise InventoryError(f"{ctx}: 未知 update.type={t!r}")


def load_inventory(path: str | Path) -> Inventory:
    data = yaml.safe_load(Path(path).read_text())
    if not isinstance(data, dict):
        raise InventoryError("inventory 根節點必須是 mapping")

    machines: dict[str, Machine] = {}
    for name, m in (data.get("machines") or {}).items():
        if "host" not in m or "ssh_user" not in m:
            raise InventoryError(f"machine {name}: 需要 host 與 ssh_user")
        machines[name] = Machine(name=name, host=m["host"],
                                 ssh_user=m["ssh_user"], local=bool(m.get("local", False)))

    software: list[Software] = []
    for sw in (data.get("software") or []):
        name = sw.get("name")
        if not name:
            raise InventoryError("software 條目缺少 name")
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
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_inventory.py -v`
Expected: PASS（3 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/inventory.py tests/test_inventory.py && git commit -m "feat: load and validate inventory yaml"
```

---

### Task 4: SQLite schema 與資料層

**Files:**
- Create: `cockpit/cockpit/schema.sql`
- Create: `cockpit/cockpit/db.py`
- Test: `cockpit/tests/test_db.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_db.py`:

```python
from cockpit import db


def _conn(tmp_path):
    c = db.connect(tmp_path / "c.db")
    db.init_db(c)
    return c


def test_upsert_and_list_installs(tmp_path):
    c = _conn(tmp_path)
    db.upsert_install(c, "claude-code", "mac", "2.1.98", "behind", "2026-06-03T00:00:00")
    db.upsert_install(c, "claude-code", "mac", "2.1.101", "up_to_date", "2026-06-03T01:00:00")
    rows = db.list_installs(c)
    assert len(rows) == 1
    assert rows[0]["current_version"] == "2.1.101"
    assert rows[0]["status"] == "up_to_date"


def test_versions(tmp_path):
    c = _conn(tmp_path)
    db.add_version(c, "claude-code", "2.1.101", "2026-04-10", "raw notes", "中文摘要")
    v = db.get_version(c, "claude-code", "2.1.101")
    assert v["changelog_zh"] == "中文摘要"
    assert db.get_version(c, "claude-code", "9.9.9") is None


def test_job_lifecycle(tmp_path):
    c = _conn(tmp_path)
    jid = db.create_job(c, "multica", "macmini", "agent", runner="codex_exec")
    db.set_job_running(c, jid)
    db.append_job_log(c, jid, "line 1")
    db.append_job_log(c, jid, "line 2")
    db.finish_job(c, jid, "success", 0, new_version="0.9.0")
    job = db.get_job(c, jid)
    assert job["status"] == "success"
    assert job["exit_code"] == 0
    assert job["new_version"] == "0.9.0"
    assert job["log"] == "line 1\nline 2\n"


def test_events(tmp_path):
    c = _conn(tmp_path)
    db.add_event(c, "check", "claude-code", "mac", "found 2.1.101")
    cur = c.execute("SELECT type, detail FROM events")
    row = cur.fetchone()
    assert row["type"] == "check"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_db.py -v`
Expected: FAIL（ImportError / no such table）。

- [ ] **Step 3: 寫 schema.sql**

Create `cockpit/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS installs (
  software TEXT NOT NULL,
  machine TEXT NOT NULL,
  current_version TEXT,
  status TEXT NOT NULL DEFAULT 'unknown',
  last_checked TEXT,
  PRIMARY KEY (software, machine)
);

CREATE TABLE IF NOT EXISTS versions (
  software TEXT NOT NULL,
  version TEXT NOT NULL,
  released_at TEXT,
  changelog_raw TEXT,
  changelog_zh TEXT,
  fetched_at TEXT DEFAULT (datetime('now')),
  PRIMARY KEY (software, version)
);

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
  log TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT DEFAULT (datetime('now')),
  type TEXT NOT NULL,
  software TEXT,
  machine TEXT,
  detail TEXT
);
```

- [ ] **Step 4: 實作 db.py**

Create `cockpit/db.py`:

```python
from __future__ import annotations
import sqlite3
from pathlib import Path

_SCHEMA = Path(__file__).with_name("schema.sql")


def connect(path: str | Path) -> sqlite3.Connection:
    conn = sqlite3.connect(str(path), check_same_thread=False)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    return conn


def init_db(conn: sqlite3.Connection) -> None:
    conn.executescript(_SCHEMA.read_text())
    conn.commit()


def upsert_install(conn, software, machine, current_version, status, last_checked):
    conn.execute(
        """INSERT INTO installs (software, machine, current_version, status, last_checked)
           VALUES (?, ?, ?, ?, ?)
           ON CONFLICT(software, machine) DO UPDATE SET
             current_version=excluded.current_version,
             status=excluded.status,
             last_checked=excluded.last_checked""",
        (software, machine, current_version, status, last_checked),
    )
    conn.commit()


def list_installs(conn):
    return list(conn.execute("SELECT * FROM installs ORDER BY software, machine"))


def add_version(conn, software, version, released_at, changelog_raw, changelog_zh):
    conn.execute(
        """INSERT INTO versions (software, version, released_at, changelog_raw, changelog_zh)
           VALUES (?, ?, ?, ?, ?)
           ON CONFLICT(software, version) DO UPDATE SET
             released_at=excluded.released_at,
             changelog_raw=excluded.changelog_raw,
             changelog_zh=COALESCE(excluded.changelog_zh, versions.changelog_zh)""",
        (software, version, released_at, changelog_raw, changelog_zh),
    )
    conn.commit()


def get_version(conn, software, version):
    return conn.execute(
        "SELECT * FROM versions WHERE software=? AND version=?", (software, version)
    ).fetchone()


def create_job(conn, software, machine, kind, runner=None) -> int:
    cur = conn.execute(
        "INSERT INTO jobs (software, machine, kind, runner) VALUES (?, ?, ?, ?)",
        (software, machine, kind, runner),
    )
    conn.commit()
    return cur.lastrowid


def set_job_running(conn, job_id):
    conn.execute(
        "UPDATE jobs SET status='running', started_at=datetime('now') WHERE id=?", (job_id,)
    )
    conn.commit()


def append_job_log(conn, job_id, line):
    conn.execute("UPDATE jobs SET log = log || ? || char(10) WHERE id=?", (line, job_id))
    conn.commit()


def finish_job(conn, job_id, status, exit_code, new_version=None):
    conn.execute(
        """UPDATE jobs SET status=?, exit_code=?, new_version=?, finished_at=datetime('now')
           WHERE id=?""",
        (status, exit_code, new_version, job_id),
    )
    conn.commit()


def get_job(conn, job_id):
    return conn.execute("SELECT * FROM jobs WHERE id=?", (job_id,)).fetchone()


def add_event(conn, type, software, machine, detail):
    conn.execute(
        "INSERT INTO events (type, software, machine, detail) VALUES (?, ?, ?, ?)",
        (type, software, machine, detail),
    )
    conn.commit()
```

- [ ] **Step 5: 跑測試確認通過**

Run: `python -m pytest tests/test_db.py -v`
Expected: PASS（4 passed）。

- [ ] **Step 6: Commit**

```bash
git add cockpit/schema.sql cockpit/db.py tests/test_db.py && git commit -m "feat: sqlite schema and data layer"
```

---

### Task 5: 版本解析與比較

**Files:**
- Create: `cockpit/cockpit/version_parse.py`
- Test: `cockpit/tests/test_version_parse.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_version_parse.py`:

```python
from cockpit.version_parse import parse_version, compare


def test_parse_plain_semver():
    assert parse_version("2.1.101") == "2.1.101"


def test_parse_embedded():
    assert parse_version("claude 2.1.98 (Claude Code)") == "2.1.98"
    assert parse_version("v0.9.0") == "0.9.0"


def test_parse_with_custom_regex():
    assert parse_version("image: multica:0.8.2", r"multica:([0-9.]+)") == "0.8.2"


def test_parse_none():
    assert parse_version("no version here") is None


def test_compare():
    assert compare("2.1.98", "2.1.101") == ("behind", 3)
    assert compare("2.1.101", "2.1.101") == ("up_to_date", 0)
    assert compare("1.0.0", "0.9.0") == ("up_to_date", 0)   # 本地比上游新 → 視為最新
    assert compare(None, "2.1.101") == ("unknown", 0)
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_version_parse.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作 version_parse.py**

Create `cockpit/version_parse.py`:

```python
from __future__ import annotations
import re

_SEMVER = re.compile(r"(\d+(?:\.\d+){1,3})")


def parse_version(text: str, regex: str | None = None) -> str | None:
    if text is None:
        return None
    pattern = re.compile(regex) if regex else _SEMVER
    m = pattern.search(text)
    return m.group(1) if m else None


def _key(v: str) -> list[int]:
    return [int(p) for p in v.split(".")]


def _pad(a: list[int], b: list[int]) -> tuple[list[int], list[int]]:
    n = max(len(a), len(b))
    return a + [0] * (n - len(a)), b + [0] * (n - len(b))


def compare(current: str | None, latest: str | None) -> tuple[str, int]:
    """回傳 (status, behind_count)。"""
    if not current or not latest:
        return ("unknown", 0)
    try:
        ck, lk = _pad(_key(current), _key(latest))
    except ValueError:
        return ("unknown", 0)
    if ck >= lk:
        return ("up_to_date", 0)
    # behind_count：以最末段差距估算，至少 1
    behind = lk[-1] - ck[-1] if lk[:-1] == ck[:-1] else 1
    return ("behind", max(behind, 1))
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_version_parse.py -v`
Expected: PASS（5 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/version_parse.py tests/test_version_parse.py && git commit -m "feat: version parsing and comparison"
```

---

### Task 6: 版本來源框架 + npm + github

**Files:**
- Create: `cockpit/cockpit/sources/__init__.py`
- Create: `cockpit/cockpit/sources/npm.py`
- Create: `cockpit/cockpit/sources/github.py`
- Test: `cockpit/tests/test_sources.py`

- [ ] **Step 1: 寫失敗測試**（用 httpx.MockTransport，不打真網路）

Create `tests/test_sources.py`:

```python
import httpx
from cockpit.models import Software
from cockpit.sources import fetch_latest


def _client(handler):
    return httpx.Client(transport=httpx.MockTransport(handler))


def test_npm_latest():
    def handler(req):
        assert "registry.npmjs.org" in str(req.url)
        return httpx.Response(200, json={"dist-tags": {"latest": "2.1.101"}})

    sw = Software(name="claude-code", kind="npm",
                  latest_source="npm:@anthropic-ai/claude-code", changelog=None, installs=[])
    res = fetch_latest(sw, client=_client(handler))
    assert res.version == "2.1.101"


def test_github_latest_and_changelog():
    def handler(req):
        return httpx.Response(200, json={"tag_name": "v0.9.0", "body": "## 0.9.0\n- fix"})

    sw = Software(name="multica", kind="github",
                  latest_source="github:o/multica", changelog="github:o/multica", installs=[])
    res = fetch_latest(sw, client=_client(handler))
    assert res.version == "0.9.0"
    assert "fix" in res.changelog_raw
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_sources.py -v`
Expected: FAIL（ModuleNotFoundError: cockpit.sources）。

- [ ] **Step 3: 實作 sources 框架與 npm/github**

Create `cockpit/sources/__init__.py`:

```python
from __future__ import annotations
from dataclasses import dataclass
import httpx

from cockpit.models import Software


@dataclass
class SourceResult:
    version: str
    changelog_raw: str | None = None


def _split(source: str) -> tuple[str, str]:
    provider, _, locator = source.partition(":")
    return provider, locator


def fetch_latest(software: Software, client: httpx.Client | None = None) -> SourceResult:
    from cockpit.sources import npm, github, pypi, brew, claude_plugin, custom

    registry = {
        "npm": npm.fetch,
        "github": github.fetch,
        "pypi": pypi.fetch,
        "brew": brew.fetch,
        "claude-plugin": claude_plugin.fetch,
        "custom": custom.fetch,
    }
    provider, locator = _split(software.latest_source)
    if provider not in registry:
        raise ValueError(f"未知 provider: {provider}")
    owns_client = client is None
    client = client or httpx.Client(timeout=20)
    try:
        return registry[provider](software, locator, client)
    finally:
        if owns_client:
            client.close()
```

Create `cockpit/sources/npm.py`:

```python
from __future__ import annotations
import httpx
from cockpit.sources import SourceResult


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    r = client.get(f"https://registry.npmjs.org/{locator}")
    r.raise_for_status()
    version = r.json()["dist-tags"]["latest"]
    changelog = None
    if software.changelog and software.changelog.startswith("github:"):
        from cockpit.sources.github import release_body
        changelog = release_body(software.changelog.split(":", 1)[1], version, client)
    return SourceResult(version=version, changelog_raw=changelog)
```

Create `cockpit/sources/github.py`:

```python
from __future__ import annotations
import os
import httpx
from cockpit.sources import SourceResult


def _headers():
    token = os.environ.get("COCKPIT_GITHUB_TOKEN")
    return {"Authorization": f"Bearer {token}"} if token else {}


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    r = client.get(f"https://api.github.com/repos/{locator}/releases/latest", headers=_headers())
    r.raise_for_status()
    data = r.json()
    version = data["tag_name"].lstrip("v")
    return SourceResult(version=version, changelog_raw=data.get("body"))


def release_body(repo, version, client: httpx.Client) -> str | None:
    for tag in (f"v{version}", version):
        r = client.get(f"https://api.github.com/repos/{repo}/releases/tags/{tag}",
                       headers=_headers())
        if r.status_code == 200:
            return r.json().get("body")
    return None
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_sources.py -v`
Expected: PASS（2 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/sources/ tests/test_sources.py && git commit -m "feat: version source framework with npm and github"
```

---

### Task 7: 其餘來源 pypi / brew / claude-plugin / custom

**Files:**
- Create: `cockpit/cockpit/sources/pypi.py`
- Create: `cockpit/cockpit/sources/brew.py`
- Create: `cockpit/cockpit/sources/claude_plugin.py`
- Create: `cockpit/cockpit/sources/custom.py`
- Test: `cockpit/tests/test_sources_more.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_sources_more.py`:

```python
import httpx
from cockpit.models import Software
from cockpit.sources import fetch_latest


def _client(handler):
    return httpx.Client(transport=httpx.MockTransport(handler))


def test_pypi():
    def handler(req):
        return httpx.Response(200, json={"info": {"version": "1.4.2"}})
    sw = Software(name="x", kind="pypi", latest_source="pypi:somepkg", changelog=None, installs=[])
    assert fetch_latest(sw, client=_client(handler)).version == "1.4.2"


def test_brew():
    def handler(req):
        return httpx.Response(200, json={"versions": {"stable": "3.2.1"}})
    sw = Software(name="x", kind="brew", latest_source="brew:wget", changelog=None, installs=[])
    assert fetch_latest(sw, client=_client(handler)).version == "3.2.1"


def test_claude_plugin_uses_github_release():
    def handler(req):
        return httpx.Response(200, json={"tag_name": "1.4.0", "body": "notes"})
    sw = Software(name="super-telegram", kind="claude-plugin",
                  latest_source="claude-plugin:curtis1215/super-telegram-plugin",
                  changelog="github:curtis1215/super-telegram-plugin", installs=[])
    res = fetch_latest(sw, client=_client(handler))
    assert res.version == "1.4.0"


def test_custom_uses_latest_cmd(monkeypatch):
    sw = Software(name="x", kind="custom",
                  latest_source="custom:echo 9.9.9", changelog=None, installs=[])
    res = fetch_latest(sw, client=_client(lambda req: httpx.Response(404)))
    assert res.version == "9.9.9"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_sources_more.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作四個來源**

Create `cockpit/sources/pypi.py`:

```python
from __future__ import annotations
import httpx
from cockpit.sources import SourceResult


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    r = client.get(f"https://pypi.org/pypi/{locator}/json")
    r.raise_for_status()
    version = r.json()["info"]["version"]
    changelog = None
    if software.changelog and software.changelog.startswith("github:"):
        from cockpit.sources.github import release_body
        changelog = release_body(software.changelog.split(":", 1)[1], version, client)
    return SourceResult(version=version, changelog_raw=changelog)
```

Create `cockpit/sources/brew.py`:

```python
from __future__ import annotations
import httpx
from cockpit.sources import SourceResult


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    r = client.get(f"https://formulae.brew.sh/api/formula/{locator}.json")
    r.raise_for_status()
    version = r.json()["versions"]["stable"]
    return SourceResult(version=version, changelog_raw=None)
```

Create `cockpit/sources/claude_plugin.py`:

```python
from __future__ import annotations
import httpx
from cockpit.sources import SourceResult
from cockpit.sources.github import fetch as gh_fetch


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    # claude-plugin 的版本來自其來源 GitHub repo 的最新 release
    return gh_fetch(software, locator, client)
```

Create `cockpit/sources/custom.py`:

```python
from __future__ import annotations
import subprocess
import httpx
from cockpit.sources import SourceResult
from cockpit.version_parse import parse_version


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    # locator 是一個本地 shell 指令，stdout 內含版本字串
    out = subprocess.run(["bash", "-lc", locator], capture_output=True, text=True, timeout=60)
    version = parse_version(out.stdout.strip()) or out.stdout.strip()
    return SourceResult(version=version, changelog_raw=None)
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_sources_more.py -v`
Expected: PASS（4 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/sources/ tests/test_sources_more.py && git commit -m "feat: add pypi, brew, claude-plugin, custom sources"
```

---

### Task 8: 執行器（本地 / SSH，可串流）

**Files:**
- Create: `cockpit/cockpit/runner.py`
- Test: `cockpit/tests/test_runner.py`

- [ ] **Step 1: 寫失敗測試**（只測本地路徑；SSH 路徑以 monkeypatch 驗證分派）

Create `tests/test_runner.py`:

```python
from cockpit.models import Machine
from cockpit import runner


def test_execute_local_captures_output_and_exit():
    m = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    lines = []
    res = runner.execute(m, "echo hello && echo world", on_line=lines.append)
    assert res.exit_code == 0
    assert "hello" in res.output and "world" in res.output
    assert lines == ["hello", "world"]


def test_execute_local_nonzero_exit():
    m = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    res = runner.execute(m, "exit 3")
    assert res.exit_code == 3


def test_remote_dispatches_to_ssh(monkeypatch):
    m = Machine(name="box", host="5.6.7.8", ssh_user="root", local=False)
    called = {}

    def fake_ssh(machine, shell_cmd, cwd, on_line, timeout):
        called["host"] = machine.host
        if on_line:
            on_line("remote-out")
        return runner.ExecResult(exit_code=0, output="remote-out\n")

    monkeypatch.setattr(runner, "_run_ssh", fake_ssh)
    res = runner.execute(m, "uname -a")
    assert called["host"] == "5.6.7.8"
    assert res.output == "remote-out\n"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_runner.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作 runner.py**

Create `cockpit/runner.py`:

```python
from __future__ import annotations
import shlex
import subprocess
from dataclasses import dataclass

from cockpit.models import Machine


@dataclass
class ExecResult:
    exit_code: int
    output: str


def execute(machine: Machine, shell_cmd: str, cwd: str | None = None,
            on_line=None, timeout: int = 900) -> ExecResult:
    if machine.local:
        return _run_local(shell_cmd, cwd, on_line, timeout)
    return _run_ssh(machine, shell_cmd, cwd, on_line, timeout)


def _wrap_cwd(shell_cmd: str, cwd: str | None) -> str:
    if cwd:
        return f"cd {shlex.quote(cwd)} && {shell_cmd}"
    return shell_cmd


def _run_local(shell_cmd, cwd, on_line, timeout) -> ExecResult:
    proc = subprocess.Popen(
        ["bash", "-lc", _wrap_cwd(shell_cmd, cwd)],
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1,
    )
    captured: list[str] = []
    assert proc.stdout is not None
    for line in proc.stdout:
        line = line.rstrip("\n")
        captured.append(line)
        if on_line:
            on_line(line)
    proc.wait(timeout=timeout)
    return ExecResult(exit_code=proc.returncode, output="\n".join(captured) + ("\n" if captured else ""))


def _run_ssh(machine: Machine, shell_cmd, cwd, on_line, timeout) -> ExecResult:
    import paramiko

    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(machine.host, username=machine.ssh_user, timeout=20)
    captured: list[str] = []
    try:
        _, stdout, _ = client.exec_command(_wrap_cwd(shell_cmd, cwd), timeout=timeout, get_pty=True)
        for raw in iter(stdout.readline, ""):
            line = raw.rstrip("\n")
            captured.append(line)
            if on_line:
                on_line(line)
        exit_code = stdout.channel.recv_exit_status()
    finally:
        client.close()
    return ExecResult(exit_code=exit_code, output="\n".join(captured) + ("\n" if captured else ""))
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_runner.py -v`
Expected: PASS（3 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/runner.py tests/test_runner.py && git commit -m "feat: local and ssh command runner with streaming"
```

---

### Task 9: changelog 翻譯（claude -p）

**Files:**
- Create: `cockpit/cockpit/translate.py`
- Test: `cockpit/tests/test_translate.py`

- [ ] **Step 1: 寫失敗測試**（monkeypatch subprocess，不呼叫真 claude）

Create `tests/test_translate.py`:

```python
from cockpit import translate


def test_translate_calls_claude(monkeypatch):
    seen = {}

    class FakeCompleted:
        returncode = 0
        stdout = "翻譯後的中文摘要"

    def fake_run(cmd, **kw):
        seen["cmd"] = cmd
        seen["input"] = kw.get("input")
        return FakeCompleted()

    monkeypatch.setattr(translate.subprocess, "run", fake_run)
    out = translate.translate_changelog("## 1.0\n- fix bug")
    assert out == "翻譯後的中文摘要"
    assert seen["cmd"][0] == "claude"
    assert "-p" in seen["cmd"]


def test_translate_empty_returns_none():
    assert translate.translate_changelog("") is None
    assert translate.translate_changelog(None) is None


def test_translate_failure_returns_none(monkeypatch):
    def boom(cmd, **kw):
        raise RuntimeError("claude not found")

    monkeypatch.setattr(translate.subprocess, "run", boom)
    assert translate.translate_changelog("notes") is None
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_translate.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作 translate.py**

Create `cockpit/translate.py`:

```python
from __future__ import annotations
import subprocess

_PROMPT = (
    "你是技術翻譯。把以下軟體 changelog 整理成繁體中文重點摘要，"
    "用條列列出重要變更（新功能/修正/安全/破壞性變更），精簡不逐字翻。\n\n"
    "---\n{raw}\n---"
)


def translate_changelog(raw: str | None, timeout: int = 120) -> str | None:
    if not raw or not raw.strip():
        return None
    prompt = _PROMPT.format(raw=raw)
    try:
        res = subprocess.run(["claude", "-p", prompt], capture_output=True,
                             text=True, timeout=timeout)
    except Exception:
        return None
    if res.returncode != 0:
        return None
    out = (res.stdout or "").strip()
    return out or None
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_translate.py -v`
Expected: PASS（3 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/translate.py tests/test_translate.py && git commit -m "feat: changelog translation via claude -p"
```

---

### Task 10: 採集器（collector）

**Files:**
- Create: `cockpit/cockpit/collector.py`
- Test: `cockpit/tests/test_collector.py`

- [ ] **Step 1: 寫失敗測試**（注入 fake fetch/execute/translate）

Create `tests/test_collector.py`:

```python
from cockpit import db
from cockpit.collector import run_collection
from cockpit.models import Machine, Update, Install, Software, Inventory
from cockpit.runner import ExecResult
from cockpit.sources import SourceResult


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="claude-code", kind="npm", latest_source="npm:cc",
                  changelog="github:o/cc",
                  installs=[Install(machine="mac", current_cmd="claude --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def test_collection_marks_behind_and_translates(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv()

    def fake_fetch(software, client=None):
        return SourceResult(version="2.1.101", changelog_raw="## notes")

    def fake_execute(machine, shell_cmd, cwd=None, on_line=None, timeout=900):
        return ExecResult(exit_code=0, output="claude 2.1.98\n")

    def fake_translate(raw, timeout=120):
        return "中文摘要"

    run_collection(c, inv, fetch=fake_fetch, execute=fake_execute, translate=fake_translate)

    row = db.list_installs(c)[0]
    assert row["current_version"] == "2.1.98"
    assert row["status"] == "behind"
    v = db.get_version(c, "claude-code", "2.1.101")
    assert v["changelog_zh"] == "中文摘要"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_collector.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作 collector.py**

Create `cockpit/collector.py`:

```python
from __future__ import annotations
from datetime import datetime, timezone

from cockpit import db
from cockpit.models import Inventory
from cockpit.sources import fetch_latest as _default_fetch
from cockpit.runner import execute as _default_execute
from cockpit.translate import translate_changelog as _default_translate
from cockpit.version_parse import parse_version, compare


def run_collection(conn, inv: Inventory, *, fetch=_default_fetch,
                   execute=_default_execute, translate=_default_translate) -> None:
    now = datetime.now(timezone.utc).isoformat()
    for sw in inv.software:
        try:
            latest = fetch(sw)
        except Exception as e:
            db.add_event(conn, "error", sw.name, None, f"fetch failed: {e}")
            continue

        # 記錄上游版本；若是新版且尚未翻譯，翻譯 changelog
        existing = db.get_version(conn, sw.name, latest.version)
        zh = existing["changelog_zh"] if existing else None
        if zh is None and latest.changelog_raw:
            zh = translate(latest.changelog_raw)
        db.add_version(conn, sw.name, latest.version, None, latest.changelog_raw, zh)

        for inst in sw.installs:
            machine = inv.machines[inst.machine]
            try:
                res = execute(machine, inst.current_cmd)
                current = parse_version(res.output, inst.version_regex)
                status, _ = compare(current, latest.version)
            except Exception as e:
                db.add_event(conn, "error", sw.name, inst.machine, f"current_cmd failed: {e}")
                current, status = None, "error"
            db.upsert_install(conn, sw.name, inst.machine, current, status, now)
            db.add_event(conn, "check", sw.name, inst.machine,
                         f"current={current} latest={latest.version} status={status}")
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_collector.py -v`
Expected: PASS（1 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/collector.py tests/test_collector.py && git commit -m "feat: collection pipeline"
```

---

### Task 11: Job 引擎（command + agent）

**Files:**
- Create: `cockpit/cockpit/jobs.py`
- Test: `cockpit/tests/test_jobs.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_jobs.py`:

```python
from cockpit import db, jobs
from cockpit.models import Machine, Update, Install, Software, Inventory
from cockpit.runner import ExecResult


def _inv_command():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _inv_agent():
    box = Machine(name="macmini", host="1.1.1.1", ssh_user="curtis", local=True)
    sw = Software(name="multica", kind="custom", latest_source="github:o/multica", changelog=None,
                  installs=[Install(machine="macmini", current_cmd="docker inspect …",
                                    update=Update(type="agent", runner="codex_exec",
                                                  cwd="/srv/multica",
                                                  prompt="update to {latest_version}"))])
    return Inventory(machines={"macmini": box}, software=[sw])


def test_build_command_cmd():
    inv = _inv_command()
    sw = inv.software[0]; inst = sw.installs[0]
    cmd, machine = jobs.build_update(inv, sw, inst, latest_version="2.1.101",
                                     current_version="2.1.98", changelog_zh=None)
    assert cmd == "npm i -g cc@latest"
    assert machine.name == "mac"


def test_build_agent_codex_exec_renders_prompt():
    inv = _inv_agent()
    sw = inv.software[0]; inst = sw.installs[0]
    cmd, machine = jobs.build_update(inv, sw, inst, latest_version="0.9.0",
                                     current_version="0.8.2", changelog_zh=None)
    assert cmd.startswith("codex exec --cd ")
    assert "update to 0.9.0" in cmd          # prompt 變數已渲染
    assert machine.name == "macmini"


def test_run_update_job_success(tmp_path, monkeypatch):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv_command()

    calls = []

    def fake_execute(machine, shell_cmd, cwd=None, on_line=None, timeout=900):
        calls.append(shell_cmd)
        if shell_cmd == "cc --version":          # 收尾重讀版本
            if on_line: on_line("cc 2.1.101")
            return ExecResult(0, "cc 2.1.101\n")
        if on_line: on_line("added 1 package")    # 更新本身
        return ExecResult(0, "added 1 package\n")

    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.run_job(c, inv, jid, execute=fake_execute)

    job = db.get_job(c, jid)
    assert job["status"] == "success"
    assert job["new_version"] == "2.1.101"
    assert "added 1 package" in job["log"]
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_jobs.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作 jobs.py**

Create `cockpit/jobs.py`:

```python
from __future__ import annotations
import shlex

from cockpit import db
from cockpit.models import Inventory, Software, Install, Machine
from cockpit.runner import execute as _default_execute
from cockpit.version_parse import parse_version


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

    # agent 型：先渲染 prompt 變數
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
    return db.create_job(conn, software, machine, inst.update.type,
                         runner=inst.update.runner)


def run_job(conn, inv: Inventory, job_id: int, *, execute=_default_execute) -> None:
    job = db.get_job(conn, job_id)
    sw, inst = _find(inv, job["software"], job["machine"])
    db.set_job_running(conn, job_id)

    latest_row = conn.execute(
        "SELECT version, changelog_zh FROM versions WHERE software=? ORDER BY rowid DESC LIMIT 1",
        (sw.name,)).fetchone()
    latest_version = latest_row["version"] if latest_row else None
    changelog_zh = latest_row["changelog_zh"] if latest_row else None
    cur_row = conn.execute(
        "SELECT current_version FROM installs WHERE software=? AND machine=?",
        (sw.name, inst.machine)).fetchone()
    current_version = cur_row["current_version"] if cur_row else None

    cmd, machine = build_update(inv, sw, inst, latest_version=latest_version,
                                current_version=current_version, changelog_zh=changelog_zh)
    cwd = inst.update.cwd if inst.update.type == "agent" else None

    try:
        res = execute(machine, cmd, cwd=cwd, on_line=lambda ln: db.append_job_log(conn, job_id, ln))
    except Exception as e:
        db.append_job_log(conn, job_id, f"[error] {e}")
        db.finish_job(conn, job_id, "failed", -1)
        db.add_event(conn, "update", sw.name, inst.machine, f"job {job_id} crashed: {e}")
        return

    new_version = None
    if res.exit_code == 0:
        verify = execute(machine, inst.current_cmd,
                         on_line=lambda ln: db.append_job_log(conn, job_id, ln))
        new_version = parse_version(verify.output, inst.version_regex)
        db.upsert_install(conn, sw.name, inst.machine, new_version, "up_to_date",
                          _now())
    status = "success" if res.exit_code == 0 else "failed"
    db.finish_job(conn, job_id, status, res.exit_code, new_version=new_version)
    db.add_event(conn, "update", sw.name, inst.machine,
                 f"job {job_id} {status} exit={res.exit_code} new={new_version}")


def _now() -> str:
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).isoformat()
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_jobs.py -v`
Expected: PASS（3 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/jobs.py tests/test_jobs.py && git commit -m "feat: update job engine for command and agent"
```

---

### Task 12: FastAPI app 與 API 路由

**Files:**
- Create: `cockpit/cockpit/web/__init__.py`
- Create: `cockpit/cockpit/web/app.py`
- Test: `cockpit/tests/test_web.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_web.py`:

```python
from fastapi.testclient import TestClient
from cockpit import db
from cockpit.web.app import create_app
from cockpit.models import Machine, Update, Install, Software, Inventory


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _app(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    db.upsert_install(c, "cc", "mac", "2.1.98", "behind", "2026-06-03T00:00:00")
    db.add_version(c, "cc", "2.1.101", "2026-04-10", "raw", "中文")
    return create_app(c, _inv()), c


def test_list_installs(tmp_path):
    app, _ = _app(tmp_path)
    r = TestClient(app).get("/api/installs")
    assert r.status_code == 200
    rows = r.json()
    assert rows[0]["software"] == "cc"
    assert rows[0]["status"] == "behind"
    assert rows[0]["latest_version"] == "2.1.101"


def test_changelog_endpoint(tmp_path):
    app, _ = _app(tmp_path)
    r = TestClient(app).get("/api/changelog/cc/2.1.101")
    assert r.status_code == 200
    assert r.json()["changelog_zh"] == "中文"


def test_trigger_update_creates_job(tmp_path, monkeypatch):
    app, c = _app(tmp_path)
    import cockpit.web.app as webapp
    monkeypatch.setattr(webapp, "_spawn_job", lambda conn, inv, jid: None)  # 不真跑
    r = TestClient(app).post("/api/installs/cc/mac/update")
    assert r.status_code == 200
    jid = r.json()["job_id"]
    assert db.get_job(c, jid)["software"] == "cc"
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_web.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作 web/app.py**

Create `cockpit/web/__init__.py`（空檔）。

Create `cockpit/web/app.py`:

```python
from __future__ import annotations
import threading
from pathlib import Path

from fastapi import FastAPI, HTTPException
from fastapi.staticfiles import StaticFiles

from cockpit import db, jobs
from cockpit.collector import run_collection
from cockpit.models import Inventory

STATIC_DIR = Path(__file__).with_name("static")


def _latest_map(conn) -> dict[str, str]:
    rows = conn.execute(
        "SELECT software, version FROM versions ORDER BY rowid").fetchall()
    return {r["software"]: r["version"] for r in rows}  # 後者覆蓋前者＝最新


def _spawn_job(conn, inv, job_id):
    threading.Thread(target=jobs.run_job, args=(conn, inv, job_id), daemon=True).start()


def create_app(conn, inv: Inventory) -> FastAPI:
    app = FastAPI(title="cockpit")

    @app.get("/api/installs")
    def list_installs():
        latest = _latest_map(conn)
        out = []
        for row in db.list_installs(conn):
            out.append({
                "software": row["software"], "machine": row["machine"],
                "current_version": row["current_version"], "status": row["status"],
                "last_checked": row["last_checked"],
                "latest_version": latest.get(row["software"]),
            })
        return out

    @app.get("/api/changelog/{software}/{version}")
    def changelog(software: str, version: str):
        v = db.get_version(conn, software, version)
        if not v:
            raise HTTPException(404, "version not found")
        return {"software": software, "version": version,
                "changelog_zh": v["changelog_zh"], "changelog_raw": v["changelog_raw"],
                "released_at": v["released_at"]}

    @app.post("/api/check")
    def check():
        threading.Thread(target=run_collection, args=(conn, inv), daemon=True).start()
        return {"started": True}

    @app.post("/api/installs/{software}/{machine}/update")
    def trigger_update(software: str, machine: str):
        try:
            jid = jobs.start_job(conn, inv, software, machine)
        except KeyError:
            raise HTTPException(404, "install not found")
        _spawn_job(conn, inv, jid)
        return {"job_id": jid}

    @app.get("/api/jobs/{job_id}")
    def get_job(job_id: int):
        job = db.get_job(conn, job_id)
        if not job:
            raise HTTPException(404, "job not found")
        return dict(job)

    if STATIC_DIR.exists():
        app.mount("/", StaticFiles(directory=str(STATIC_DIR), html=True), name="static")

    return app
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_web.py -v`
Expected: PASS（3 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/web/ tests/test_web.py && git commit -m "feat: fastapi app and api routes"
```

---

### Task 13: SSE 即時 log 串流

**Files:**
- Modify: `cockpit/cockpit/web/app.py`（新增 SSE 路由）
- Test: `cockpit/tests/test_sse.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_sse.py`:

```python
from fastapi.testclient import TestClient
from cockpit import db
from cockpit.web.app import create_app
from cockpit.models import Machine, Update, Install, Software, Inventory


def _app(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="x"))])
    inv = Inventory(machines={"mac": mac}, software=[sw])
    jid = db.create_job(c, "cc", "mac", "command")
    db.append_job_log(c, jid, "line A")
    db.append_job_log(c, jid, "line B")
    db.finish_job(c, jid, "success", 0, new_version="2.1.101")
    return create_app(c, inv), jid


def test_sse_streams_existing_log_then_done(tmp_path):
    app, jid = _app(tmp_path)
    with TestClient(app) as client:
        r = client.get(f"/api/jobs/{jid}/log/stream")
        body = r.text
    assert "line A" in body
    assert "line B" in body
    assert "event: done" in body
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_sse.py -v`
Expected: FAIL（404，尚無此路由）。

- [ ] **Step 3: 新增 SSE 路由**

在 `cockpit/web/app.py` 的 import 區加入：

```python
import asyncio
from sse_starlette.sse import EventSourceResponse
```

在 `create_app` 內、`get_job` 路由之後加入：

```python
    @app.get("/api/jobs/{job_id}/log/stream")
    async def stream_log(job_id: int):
        async def gen():
            sent = 0
            while True:
                job = db.get_job(conn, job_id)
                if not job:
                    yield {"event": "error", "data": "job not found"}
                    return
                log = job["log"] or ""
                lines = log.split("\n")
                # 已完成的行（最後一段可能是未換行的殘段，這裡 log 都以 \n 結尾）
                ready = lines[:-1] if log.endswith("\n") else lines
                for line in ready[sent:]:
                    yield {"event": "log", "data": line}
                sent = len(ready)
                if job["status"] in ("success", "failed"):
                    yield {"event": "done", "data": job["status"]}
                    return
                await asyncio.sleep(0.5)
        return EventSourceResponse(gen())
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_sse.py -v`
Expected: PASS（1 passed）。

- [ ] **Step 5: Commit**

```bash
git add cockpit/web/app.py tests/test_sse.py && git commit -m "feat: sse live job log streaming"
```

---

### Task 14: 設定、排程器、進入點

**Files:**
- Create: `cockpit/cockpit/config.py`
- Create: `cockpit/cockpit/scheduler.py`
- Create: `cockpit/cockpit/main.py`
- Test: `cockpit/tests/test_config_scheduler.py`

- [ ] **Step 1: 寫失敗測試**

Create `tests/test_config_scheduler.py`:

```python
from cockpit.config import Settings
from cockpit import scheduler


def test_settings_from_env(monkeypatch, tmp_path):
    monkeypatch.setenv("COCKPIT_DB_PATH", str(tmp_path / "c.db"))
    monkeypatch.setenv("COCKPIT_INVENTORY", str(tmp_path / "inv.yaml"))
    monkeypatch.setenv("COCKPIT_CHECK_HOURS", "6")
    s = Settings.from_env()
    assert s.db_path.endswith("c.db")
    assert s.check_hours == 6


def test_scheduler_registers_job(tmp_path):
    calls = []
    sch = scheduler.build_scheduler(lambda: calls.append(1), hours=12)
    jobs = sch.get_jobs()
    assert len(jobs) == 1
    sch.shutdown(wait=False)
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `python -m pytest tests/test_config_scheduler.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 實作 config / scheduler / main**

Create `cockpit/config.py`:

```python
from __future__ import annotations
import os
from dataclasses import dataclass


@dataclass
class Settings:
    db_path: str
    inventory_path: str
    check_hours: int

    @classmethod
    def from_env(cls) -> "Settings":
        return cls(
            db_path=os.environ.get("COCKPIT_DB_PATH", "cockpit.db"),
            inventory_path=os.environ.get("COCKPIT_INVENTORY", "inventory.yaml"),
            check_hours=int(os.environ.get("COCKPIT_CHECK_HOURS", "24")),
        )
```

Create `cockpit/scheduler.py`:

```python
from __future__ import annotations
from apscheduler.schedulers.background import BackgroundScheduler


def build_scheduler(func, hours: int) -> BackgroundScheduler:
    sch = BackgroundScheduler()
    sch.add_job(func, "interval", hours=hours, id="collection")
    return sch
```

Create `cockpit/main.py`:

```python
from __future__ import annotations
import uvicorn

from cockpit import db
from cockpit.config import Settings
from cockpit.inventory import load_inventory
from cockpit.collector import run_collection
from cockpit.scheduler import build_scheduler
from cockpit.web.app import create_app


def build() -> "tuple":
    settings = Settings.from_env()
    conn = db.connect(settings.db_path)
    db.init_db(conn)
    inv = load_inventory(settings.inventory_path)
    app = create_app(conn, inv)

    sch = build_scheduler(lambda: run_collection(conn, inv), hours=settings.check_hours)
    sch.start()
    return app, settings


app, _settings = build()


def main():
    uvicorn.run(app, host="127.0.0.1", port=8787)


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: 跑測試確認通過**

Run: `python -m pytest tests/test_config_scheduler.py -v`
Expected: PASS（2 passed）。

- [ ] **Step 5: 跑全部測試**

Run: `python -m pytest -v`
Expected: 全數 PASS。

- [ ] **Step 6: Commit**

```bash
git add cockpit/config.py cockpit/scheduler.py cockpit/main.py tests/test_config_scheduler.py && git commit -m "feat: settings, scheduler, and app entrypoint"
```

---

### Task 15: 範例清單與部署文件

**Files:**
- Create: `cockpit/inventory.example.yaml`
- Create: `cockpit/docs/deploy.md`
- Modify: `cockpit/README.md`

- [ ] **Step 1: 建立範例 inventory**

Create `inventory.example.yaml`:

```yaml
machines:
  mac:        { host: 192.168.1.10, ssh_user: curtis, local: true }
  ubuntu_llm: { host: 100.0.0.0, ssh_user: curtis }
  macmini:    { host: 192.168.1.10, ssh_user: curtis }

software:
  - name: claude-code
    kind: npm
    latest_source: "npm:@anthropic-ai/claude-code"
    changelog: "github:anthropics/claude-code"
    installs:
      - machine: mac
        current_cmd: "claude --version"
        update: { type: command, cmd: "npm i -g @anthropic-ai/claude-code@latest" }

  - name: multica
    kind: custom
    latest_source: "github:OWNER/multica"
    changelog: "github:OWNER/multica"
    installs:
      - machine: macmini
        current_cmd: "docker inspect multica --format '{{ index .Config.Labels \"version\" }}'"
        update:
          type: agent
          runner: codex_exec
          cwd: "/srv/multica"
          prompt: |
            multica 上游有新版 {latest_version}（目前 {current_version}）。請：
            1. 同步上游到最新
            2. 重新 build 鏡像
            3. 重新部署容器
            完成後回報新版本號與部署結果。
```

- [ ] **Step 2: 撰寫部署文件**

Create `docs/deploy.md`:

```markdown
# Cockpit 部署（mac mini）

## 1. 安裝
\`\`\`bash
cd /Users/curtis/Dev/cockpit
python3 -m venv .venv && . .venv/bin/activate && pip install -e .
cp inventory.example.yaml inventory.yaml   # 填入真實機器/軟體
\`\`\`

## 2. 環境變數
- `COCKPIT_DB_PATH`（預設 cockpit.db）
- `COCKPIT_INVENTORY`（預設 inventory.yaml）
- `COCKPIT_CHECK_HOURS`（預設 24）
- `COCKPIT_GITHUB_TOKEN`（避免 GitHub API rate limit；可由 1Password 注入）

## 3. 啟動
\`\`\`bash
COCKPIT_INVENTORY=inventory.yaml python -m cockpit.main   # 監聽 127.0.0.1:8787
\`\`\`
建議以 launchd 常駐；前端產物放 `cockpit/web/static/`。

## 4. 對外（Cloudflare Tunnel + Access）
- `cloudflared` 在 mac mini 建 tunnel，路由 `cockpit.<domain>` → `http://127.0.0.1:8787`。
- Cloudflare Access：Bypass policy（信任 IP 名單）+ Allow policy（Email/Google 登入）。
- origin 不開公網 port。

## 5. SSH 前置
mac mini → 各機器設定金鑰免密碼登入；agent 型更新需目標機器上 `codex` / `claude` CLI 已登入。
```

- [ ] **Step 3: 更新 README 進度區**

在 `README.md` 末端把進度行改為：

```markdown
目前進度：子系統 2（版本追蹤器）後端實作計畫已完成於 `docs/plans/`，可開始實作；前端 prototype 交付 claude design（見 `docs/frontend-brief.md`）。
```

- [ ] **Step 4: Commit**

```bash
git add inventory.example.yaml docs/deploy.md README.md && git commit -m "docs: example inventory and deployment guide"
```

---

## Self-Review（已執行）

**1. Spec coverage：**
- 4.1 YAML（command/agent、runner、machine/cwd/prompt）→ Task 2/3、Task 11 ✅
- 4.2 SQLite（installs/versions/jobs/events）→ Task 4 ✅（software 表以 YAML 取代，已於慣例註記說明）
- 4.3 採集流程 → Task 10 ✅
- 4.4 版本來源（npm/github/pypi/brew/claude-plugin/custom）→ Task 6/7 ✅
- 4.5 翻譯（claude -p、冪等）→ Task 9、Task 10（冪等：已翻不重翻）✅
- 4.6 job 模型 + 即時 log + 收尾重讀版本 + 安全（指令來自 YAML）→ Task 11/12/13 ✅
- 4.7 UI / API（list、changelog、check、update、job、SSE）→ Task 12/13 ✅；UI 視覺由前端 brief 負責
- 5 技術選型 → 全程一致 ✅
- 6 部署 → Task 15 ✅
- 9 驗收標準 → 各 Task 測試對應 ✅

**2. Placeholder scan：** 無 TODO/TBD/「類似 Task N」等紅旗；每個 code step 都有完整可貼上的程式碼。

**3. Type consistency：** `execute(machine, shell_cmd, cwd, on_line, timeout)`、`ExecResult(exit_code, output)`、`SourceResult(version, changelog_raw)`、db 函式簽章、`build_update`/`start_job`/`run_job` 命名跨 Task 一致 ✅。
