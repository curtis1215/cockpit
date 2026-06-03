from cockpit.models import Machine
from cockpit import runner


def test_execute_local_captures_output_and_exit():
    m = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    lines = []
    res = runner.execute(m, "echo hello && echo world", on_line=lines.append)
    assert res.exit_code == 0
    assert "hello" in res.output and "world" in res.output
    assert lines == ["hello", "world"]


def test_execute_local_nonzero_exit():
    m = Machine(name="mac", host="x", ssh_user="curtis", local=True)
    res = runner.execute(m, "exit 3")
    assert res.exit_code == 3


def test_remote_dispatches_to_ssh(monkeypatch):
    m = Machine(name="box", host="5.6.7.8", ssh_user="root", local=False)
    called = {}

    def fake_ssh(machine, shell_cmd, cwd, on_line, timeout):
        called["host"] = machine.host
        if on_line:
            on_line("remote-out")
        return runner.ExecResult(exit_code=0, output="remote-out\n")

    monkeypatch.setattr(runner, "_run_ssh", fake_ssh)
    res = runner.execute(m, "uname -a")
    assert called["host"] == "5.6.7.8"
    assert res.output == "remote-out\n"
