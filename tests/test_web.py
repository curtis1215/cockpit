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
