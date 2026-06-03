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
