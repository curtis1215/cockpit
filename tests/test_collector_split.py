from cockpit import db
from cockpit.collector import refresh_upstream, apply_version_report
from cockpit.models import Machine, Update, Install, Software, Inventory
from cockpit.sources import SourceResult


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog="github:o/cc",
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def test_refresh_upstream_stores_and_translates(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv()

    def fake_fetch(software, client=None):
        return SourceResult(version="2.1.101", changelog_raw="## notes")

    def fake_translate(raw, timeout=120):
        return "中文摘要"

    refresh_upstream(c, inv, fetch=fake_fetch, translate=fake_translate)
    v = db.get_version(c, "cc", "2.1.101")
    assert v["changelog_zh"] == "中文摘要"


def test_apply_version_report_marks_behind(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv()
    db.add_version(c, "cc", "2.1.101", None, "raw", "中文")     # 上游已知
    n = apply_version_report(c, inv, "mac", [{"software": "cc", "current_version": "2.1.98"}])
    assert n == 1
    inst = db.get_install(c, "cc", "mac")
    assert inst["current_version"] == "2.1.98"
    assert inst["status"] == "behind"
