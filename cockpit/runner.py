from __future__ import annotations
import shlex
import subprocess
from dataclasses import dataclass

from cockpit.models import Machine


@dataclass
class ExecResult:
    exit_code: int
    output: str


def execute(machine: Machine, shell_cmd: str, cwd: str | None = None,
            on_line=None, timeout: int = 900) -> ExecResult:
    if machine.local:
        return _run_local(shell_cmd, cwd, on_line, timeout)
    return _run_ssh(machine, shell_cmd, cwd, on_line, timeout)


def _wrap_cwd(shell_cmd: str, cwd: str | None) -> str:
    if cwd:
        return f"cd {shlex.quote(cwd)} && {shell_cmd}"
    return shell_cmd


def _run_local(shell_cmd, cwd, on_line, timeout) -> ExecResult:
    proc = subprocess.Popen(
        ["bash", "-lc", _wrap_cwd(shell_cmd, cwd)],
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1,
    )
    captured: list[str] = []
    assert proc.stdout is not None
    for line in proc.stdout:
        line = line.rstrip("\n")
        captured.append(line)
        if on_line:
            on_line(line)
    proc.wait(timeout=timeout)
    return ExecResult(exit_code=proc.returncode, output="\n".join(captured) + ("\n" if captured else ""))


def _run_ssh(machine: Machine, shell_cmd, cwd, on_line, timeout) -> ExecResult:
    import paramiko

    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(machine.host, username=machine.ssh_user, timeout=20)
    captured: list[str] = []
    try:
        _, stdout, _ = client.exec_command(_wrap_cwd(shell_cmd, cwd), timeout=timeout, get_pty=True)
        for raw in iter(stdout.readline, ""):
            line = raw.rstrip("\n")
            captured.append(line)
            if on_line:
                on_line(line)
        exit_code = stdout.channel.recv_exit_status()
    finally:
        client.close()
    return ExecResult(exit_code=exit_code, output="\n".join(captured) + ("\n" if captured else ""))
