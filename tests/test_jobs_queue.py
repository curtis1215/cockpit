import pytest
from cockpit import db, jobs
from cockpit.models import Machine, Update, Install, Software, Inventory


def _inv_command():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _seed(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    db.add_version(c, "cc", "2.1.101", None, "raw", "中文")
    db.upsert_install(c, "cc", "mac", "2.1.98", "behind", "t")
    return c


def test_claim_renders_and_marks_running(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")          # queued
    claimed = jobs.claim_next_job(c, inv, "mac")
    assert claimed["id"] == jid
    assert claimed["shell_cmd"] == "npm i -g cc@latest"
    assert claimed["current_cmd"] == "cc --version"
    assert db.get_job(c, jid)["status"] == "running"
    assert jobs.claim_next_job(c, inv, "mac") is None


def test_record_result_success_updates_install(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")
    jobs.record_result(c, inv, jid, "success", 0, "2.1.101")
    job = db.get_job(c, jid)
    assert job["status"] == "success"
    assert job["new_version"] == "2.1.101"
    inst = db.get_install(c, "cc", "mac")
    assert inst["current_version"] == "2.1.101"
    assert inst["status"] == "up_to_date"


def test_record_result_failed_keeps_behind(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")
    jobs.record_result(c, inv, jid, "failed", 1, None)
    assert db.get_job(c, jid)["status"] == "failed"
    assert db.get_install(c, "cc", "mac")["status"] == "behind"


def test_request_abort_running_sets_flag(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")                 # running
    job = jobs.request_abort(c, jid)
    assert job["status"] == "running"
    assert db.abort_requested(c, jid) is True


def test_request_abort_queued_marks_aborted(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")           # queued, 未 claim
    job = jobs.request_abort(c, jid)
    assert job["status"] == "aborted"


def test_record_result_aborted(tmp_path):
    c = _seed(tmp_path); inv = _inv_command()
    jid = jobs.start_job(c, inv, "cc", "mac")
    jobs.claim_next_job(c, inv, "mac")
    jobs.record_result(c, inv, jid, "aborted", -1, None)
    assert db.get_job(c, jid)["status"] == "aborted"
    assert db.get_install(c, "cc", "mac")["status"] == "behind"
