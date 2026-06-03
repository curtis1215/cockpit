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
    assert db.take_check_requested(c, "mac") is True


def test_trigger_update_enqueues_only(tmp_path):
    app, c = _app(tmp_path)
    r = TestClient(app).post("/api/installs/cc/mac/update")
    assert r.status_code == 200
    jid = r.json()["job_id"]
    job = db.get_job(c, jid)
    assert job["software"] == "cc"
    assert job["status"] == "queued"        # 不再就地執行；等 agent claim
