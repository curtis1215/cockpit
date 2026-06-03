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


def test_sse_emits_first_line_when_log_starts_empty(tmp_path, monkeypatch):
    from cockpit import db as dbmod
    app, jid = _app(tmp_path)

    states = [
        {"log": "", "status": "running"},
        {"log": "line A\n", "status": "running"},
        {"log": "line A\nline B\n", "status": "success"},
    ]
    calls = {"i": 0}

    def fake_get_job(conn, job_id):
        i = min(calls["i"], len(states) - 1)
        calls["i"] += 1
        s = states[i]
        return {"id": job_id, "software": "cc", "machine": "mac",
                "log": s["log"], "status": s["status"]}

    monkeypatch.setattr(dbmod, "get_job", fake_get_job)
    with TestClient(app) as client:
        body = client.get(f"/api/jobs/{jid}/log/stream").text
    assert "line A" in body
    assert "line B" in body
    assert "event: done" in body
