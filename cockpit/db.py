from __future__ import annotations
import sqlite3
import threading
import functools
from pathlib import Path

_SCHEMA = Path(__file__).with_name("schema.sql")

_LOCK = threading.RLock()


def _synchronized(fn):
    @functools.wraps(fn)
    def wrapper(*args, **kwargs):
        with _LOCK:
            return fn(*args, **kwargs)
    return wrapper


def connect(path: str | Path) -> sqlite3.Connection:
    conn = sqlite3.connect(str(path), check_same_thread=False)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    return conn


@_synchronized
def init_db(conn: sqlite3.Connection) -> None:
    conn.executescript(_SCHEMA.read_text())
    conn.commit()


@_synchronized
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


@_synchronized
def list_installs(conn):
    return list(conn.execute("SELECT * FROM installs ORDER BY software, machine"))


@_synchronized
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


@_synchronized
def get_version(conn, software, version):
    return conn.execute(
        "SELECT * FROM versions WHERE software=? AND version=?", (software, version)
    ).fetchone()


@_synchronized
def create_job(conn, software, machine, kind, runner=None) -> int:
    cur = conn.execute(
        "INSERT INTO jobs (software, machine, kind, runner) VALUES (?, ?, ?, ?)",
        (software, machine, kind, runner),
    )
    conn.commit()
    return cur.lastrowid


@_synchronized
def set_job_running(conn, job_id):
    conn.execute(
        "UPDATE jobs SET status='running', started_at=datetime('now') WHERE id=?", (job_id,)
    )
    conn.commit()


@_synchronized
def append_job_log(conn, job_id, line):
    conn.execute("UPDATE jobs SET log = log || ? || char(10) WHERE id=?", (line, job_id))
    conn.commit()


@_synchronized
def finish_job(conn, job_id, status, exit_code, new_version=None):
    conn.execute(
        """UPDATE jobs SET status=?, exit_code=?, new_version=?, finished_at=datetime('now')
           WHERE id=?""",
        (status, exit_code, new_version, job_id),
    )
    conn.commit()


@_synchronized
def get_job(conn, job_id):
    return conn.execute("SELECT * FROM jobs WHERE id=?", (job_id,)).fetchone()


@_synchronized
def list_jobs(conn, limit=50):
    return list(conn.execute(
        "SELECT * FROM jobs ORDER BY id DESC LIMIT ?", (limit,)))


@_synchronized
def get_last_error(conn, software, machine):
    return conn.execute(
        "SELECT detail FROM events WHERE type='error' AND software=? AND machine=? "
        "ORDER BY id DESC LIMIT 1", (software, machine)).fetchone()


@_synchronized
def add_event(conn, type, software, machine, detail):
    conn.execute(
        "INSERT INTO events (type, software, machine, detail) VALUES (?, ?, ?, ?)",
        (type, software, machine, detail),
    )
    conn.commit()


@_synchronized
def create_job_unique(conn, software, machine, kind, runner=None):
    existing = conn.execute(
        "SELECT id FROM jobs WHERE software=? AND machine=? "
        "AND status IN ('queued','running') LIMIT 1", (software, machine)).fetchone()
    if existing:
        return None
    cur = conn.execute(
        "INSERT INTO jobs (software, machine, kind, runner) VALUES (?, ?, ?, ?)",
        (software, machine, kind, runner))
    conn.commit()
    return cur.lastrowid


@_synchronized
def latest_version_map(conn):
    rows = conn.execute(
        "SELECT software, version FROM versions ORDER BY rowid").fetchall()
    return {r["software"]: r["version"] for r in rows}  # 後者覆蓋前者＝最新


@_synchronized
def get_latest_version(conn, software):
    return conn.execute(
        "SELECT version, changelog_zh FROM versions WHERE software=? "
        "ORDER BY rowid DESC LIMIT 1", (software,)).fetchone()


@_synchronized
def get_install(conn, software, machine):
    return conn.execute(
        "SELECT * FROM installs WHERE software=? AND machine=?",
        (software, machine)).fetchone()


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
