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


def test_create_job_unique_blocks_duplicate(tmp_path):
    c = _conn(tmp_path)
    j1 = db.create_job_unique(c, "cc", "mac", "command")
    assert j1 is not None
    assert db.create_job_unique(c, "cc", "mac", "command") is None
    db.finish_job(c, j1, "success", 0)
    assert db.create_job_unique(c, "cc", "mac", "command") is not None


def test_concurrent_db_access_is_serialized(tmp_path):
    import threading
    c = _conn(tmp_path)
    errors = []

    def worker(n):
        try:
            for i in range(50):
                db.upsert_install(c, f"sw{n}", "m", str(i), "behind", "t")
                db.list_installs(c)
                jid = db.create_job(c, f"sw{n}", "m", "command")
                db.append_job_log(c, jid, "x")
                db.get_job(c, jid)
        except Exception as e:  # pragma: no cover
            errors.append(e)

    threads = [threading.Thread(target=worker, args=(n,)) for n in range(4)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert errors == []


def test_list_jobs_orders_newest_first(tmp_path):
    c = _conn(tmp_path)
    j1 = db.create_job(c, "a", "m", "command")
    j2 = db.create_job(c, "b", "m", "command")
    rows = db.list_jobs(c, limit=10)
    assert [r["id"] for r in rows] == [j2, j1]


def test_get_last_error_returns_latest(tmp_path):
    c = _conn(tmp_path)
    db.add_event(c, "error", "cc", "mac", "first")
    db.add_event(c, "error", "cc", "mac", "second")
    assert db.get_last_error(c, "cc", "mac")["detail"] == "second"
    assert db.get_last_error(c, "cc", "nope") is None
