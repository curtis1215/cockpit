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
