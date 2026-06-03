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
