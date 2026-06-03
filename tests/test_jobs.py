import pytest

from cockpit import db, jobs
from cockpit.models import Machine, Update, Install, Software, Inventory


def _inv_command():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _inv_agent():
    box = Machine(name="macmini", host="1.1.1.1", ssh_user="curtis", local=True)
    sw = Software(name="multica", kind="custom", latest_source="github:o/multica", changelog=None,
                  installs=[Install(machine="macmini", current_cmd="docker inspect …",
                                    update=Update(type="agent", runner="codex_exec",
                                                  cwd="/srv/multica",
                                                  prompt="update to {latest_version}"))])
    return Inventory(machines={"macmini": box}, software=[sw])


def test_build_command_cmd():
    inv = _inv_command()
    sw = inv.software[0]; inst = sw.installs[0]
    cmd, machine = jobs.build_update(inv, sw, inst, latest_version="2.1.101",
                                     current_version="2.1.98", changelog_zh=None)
    assert cmd == "npm i -g cc@latest"
    assert machine.name == "mac"


def test_build_agent_codex_exec_renders_prompt():
    inv = _inv_agent()
    sw = inv.software[0]; inst = sw.installs[0]
    cmd, machine = jobs.build_update(inv, sw, inst, latest_version="0.9.0",
                                     current_version="0.8.2", changelog_zh=None)
    assert cmd.startswith("codex exec --cd ")
    assert "update to 0.9.0" in cmd          # prompt 變數已渲染
    assert machine.name == "macmini"


def test_build_agent_claude_p_renders_prompt():
    m = Machine(name="m", host="x", ssh_user="c", local=True)
    sw = Software(name="s", kind="custom", latest_source="github:o/s", changelog=None,
                  installs=[Install(machine="m", current_cmd="s --version",
                                    update=Update(type="agent", runner="claude_p",
                                                  cwd="/srv/s", prompt="bump to {latest_version}"))])
    inv = Inventory(machines={"m": m}, software=[sw])
    cmd, machine = jobs.build_update(inv, sw, sw.installs[0], latest_version="2.0.0",
                                     current_version="1.0.0", changelog_zh=None)
    assert cmd.startswith("cd ") and "claude -p " in cmd
    assert "bump to 2.0.0" in cmd


def test_build_agent_custom_invoke_template():
    m = Machine(name="m", host="x", ssh_user="c", local=True)
    sw = Software(name="s", kind="custom", latest_source="github:o/s", changelog=None,
                  installs=[Install(machine="m", current_cmd="s --version",
                                    update=Update(type="agent", runner="custom", cwd="/srv/s",
                                                  invoke="mytool --dir {cwd} --task {prompt}",
                                                  prompt="do {latest_version}"))])
    inv = Inventory(machines={"m": m}, software=[sw])
    cmd, _ = jobs.build_update(inv, sw, sw.installs[0], latest_version="3.3.3",
                               current_version=None, changelog_zh=None)
    assert "mytool --dir" in cmd
    assert "do 3.3.3" in cmd


def test_build_unknown_runner_raises():
    m = Machine(name="m", host="x", ssh_user="c", local=True)
    sw = Software(name="s", kind="custom", latest_source="github:o/s", changelog=None,
                  installs=[Install(machine="m", current_cmd="s --version",
                                    update=Update(type="agent", runner="bogus", prompt="x"))])
    inv = Inventory(machines={"m": m}, software=[sw])
    with pytest.raises(ValueError):
        jobs.build_update(inv, sw, sw.installs[0], latest_version="1",
                          current_version=None, changelog_zh=None)


def test_start_job_blocks_when_active(tmp_path):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    inv = _inv_command()
    jobs.start_job(c, inv, "cc", "mac")
    with pytest.raises(jobs.ActiveJobExists):
        jobs.start_job(c, inv, "cc", "mac")
