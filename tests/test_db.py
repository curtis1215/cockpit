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
