from cockpit.models import Machine, Update, Install, Software, Inventory


def test_command_install():
    upd = Update(type="command", cmd="npm i -g x@latest")
    inst = Install(machine="mac", current_cmd="x --version", update=upd)
    assert inst.update.type == "command"
    assert inst.update.cmd == "npm i -g x@latest"
    assert inst.version_regex is None


def test_agent_update_fields():
    upd = Update(type="agent", runner="codex_exec", prompt="do it",
                 machine="macmini", cwd="/srv/x", invoke="codex exec --cd {cwd} {prompt}")
    assert upd.runner == "codex_exec"
    assert upd.cwd == "/srv/x"


def test_inventory_container():
    inv = Inventory(
        machines={"mac": Machine(name="mac", host="1.2.3.4", ssh_user="curtis", local=True)},
        software=[Software(name="x", kind="npm", latest_source="npm:x",
                           changelog="github:o/x", installs=[])],
    )
    assert inv.machines["mac"].local is True
    assert inv.software[0].name == "x"
