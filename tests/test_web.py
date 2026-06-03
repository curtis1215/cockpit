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


def test_trigger_update_creates_job(tmp_path):
    app, c = _app(tmp_path)
    r = TestClient(app).post("/api/installs/cc/mac/update")
    assert r.status_code == 200
    jid = r.json()["job_id"]
    assert db.get_job(c, jid)["software"] == "cc"


def test_trigger_update_conflict_returns_409(tmp_path):
    app, c = _app(tmp_path)
    client = TestClient(app)
    assert client.post("/api/installs/cc/mac/update").status_code == 200
    # first job stays 'queued' (spawn stubbed) → second is blocked
    assert client.post("/api/installs/cc/mac/update").status_code == 409


def test_list_machines(tmp_path):
    app, _ = _app(tmp_path)
    r = TestClient(app).get("/api/machines")
    assert r.status_code == 200
    assert r.json() == ["mac"]


def test_installs_enriched_fields(tmp_path):
    app, _ = _app(tmp_path)
    rows = TestClient(app).get("/api/installs").json()
    row = rows[0]
    assert row["id"] == "cc::mac"
    assert row["kind"] == "npm"
    assert row["update_kind"] == "command"
    assert row["behind_count"] == 3      # 2.1.98 -> 2.1.101
    assert "error" in row                # present (None here)


def test_list_jobs_endpoint(tmp_path):
    app, c = _app(tmp_path)
    db.create_job(c, "cc", "mac", "command")
    rows = TestClient(app).get("/api/jobs").json()
    assert len(rows) == 1
    assert rows[0]["software"] == "cc"


def test_abort_endpoint(tmp_path):
    app, c = _app(tmp_path)
    client = TestClient(app)
    jid = client.post("/api/installs/cc/mac/update").json()["job_id"]   # stays queued
    r = client.post(f"/api/jobs/{jid}/abort")
    assert r.status_code == 200
    assert r.json()["status"] == "aborted"
    assert client.post("/api/jobs/9999/abort").status_code == 404


def test_installs_status_recomputed_when_upstream_advances(tmp_path):
    # install was last reported up_to_date at version 2.1.101; then a newer upstream 3.0.1 arrives.
    app, c = _app(tmp_path)
    db.upsert_install(c, "cc", "mac", "2.1.101", "up_to_date", "t")
    db.add_version(c, "cc", "3.0.1", None, "raw", None)        # newer upstream appears
    row = [r for r in TestClient(app).get("/api/installs").json() if r["software"] == "cc"][0]
    assert row["latest_version"] == "3.0.1"
    assert row["behind_count"] >= 1
    assert row["status"] == "behind"          # NOT "up_to_date" — must agree with behind_count


def test_installs_error_status_preserved(tmp_path):
    app, c = _app(tmp_path)
    db.upsert_install(c, "cc", "mac", None, "error", "t")
    row = [r for r in TestClient(app).get("/api/installs").json() if r["software"] == "cc"][0]
    assert row["status"] == "error"           # agent execution error preserved
