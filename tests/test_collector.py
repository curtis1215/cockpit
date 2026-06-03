from cockpit import db
from cockpit.collector import run_collection
from cockpit.models import Machine, Update, Install, Software, Inventory
from cockpit.runner import ExecResult
from cockpit.sources import SourceResult


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="claude-code", kind="npm", latest_source="npm:cc",
                  changelog="github:o/cc",
                  installs=[Install(machine="mac", current_cmd="claude --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def test_collection_marks_behind_and_translates(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv()

    def fake_fetch(software, client=None):
        return SourceResult(version="2.1.101", changelog_raw="## notes")

    def fake_execute(machine, shell_cmd, cwd=None, on_line=None, timeout=900):
        return ExecResult(exit_code=0, output="claude 2.1.98\n")

    def fake_translate(raw, timeout=120):
        return "中文摘要"

    run_collection(c, inv, fetch=fake_fetch, execute=fake_execute, translate=fake_translate)

    row = db.list_installs(c)[0]
    assert row["current_version"] == "2.1.98"
    assert row["status"] == "behind"
    v = db.get_version(c, "claude-code", "2.1.101")
    assert v["changelog_zh"] == "中文摘要"
