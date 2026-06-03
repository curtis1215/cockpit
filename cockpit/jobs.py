from __future__ import annotations
import shlex

from cockpit import db
from cockpit.models import Inventory, Software, Install, Machine
from cockpit.runner import execute as _default_execute
from cockpit.version_parse import parse_version


def _find(inv: Inventory, software: str, machine: str) -> tuple[Software, Install]:
    for sw in inv.software:
        if sw.name == software:
            for inst in sw.installs:
                if inst.machine == machine:
                    return sw, inst
    raise KeyError(f"找不到 install: {software}@{machine}")


def _render(template: str, **vars) -> str:
    out = template
    for k, v in vars.items():
        out = out.replace("{" + k + "}", "" if v is None else str(v))
    return out


def build_update(inv: Inventory, sw: Software, inst: Install, *, latest_version,
                 current_version, changelog_zh) -> tuple[str, Machine]:
    upd = inst.update
    target_name = upd.machine or inst.machine
    machine = inv.machines[target_name]
    if upd.type == "command":
        return upd.cmd, machine

    # agent 型：先渲染 prompt 變數
    prompt = _render(upd.prompt, name=sw.name, machine=target_name,
                     current_version=current_version, latest_version=latest_version,
                     changelog_zh=changelog_zh, cwd=upd.cwd)
    if upd.runner == "codex_exec":
        cd = f"--cd {shlex.quote(upd.cwd)} " if upd.cwd else ""
        cmd = f"codex exec {cd}{shlex.quote(prompt)}"
    elif upd.runner == "claude_p":
        cd = f"cd {shlex.quote(upd.cwd)} && " if upd.cwd else ""
        cmd = f"{cd}claude -p {shlex.quote(prompt)}"
    elif upd.runner == "custom":
        cmd = _render(upd.invoke, prompt=shlex.quote(prompt),
                      cwd=shlex.quote(upd.cwd) if upd.cwd else "")
    else:
        raise ValueError(f"未知 runner: {upd.runner}")
    return cmd, machine


def start_job(conn, inv: Inventory, software: str, machine: str) -> int:
    sw, inst = _find(inv, software, machine)
    return db.create_job(conn, software, machine, inst.update.type,
                         runner=inst.update.runner)


def run_job(conn, inv: Inventory, job_id: int, *, execute=_default_execute) -> None:
    job = db.get_job(conn, job_id)
    sw, inst = _find(inv, job["software"], job["machine"])
    db.set_job_running(conn, job_id)

    latest_row = conn.execute(
        "SELECT version, changelog_zh FROM versions WHERE software=? ORDER BY rowid DESC LIMIT 1",
        (sw.name,)).fetchone()
    latest_version = latest_row["version"] if latest_row else None
    changelog_zh = latest_row["changelog_zh"] if latest_row else None
    cur_row = conn.execute(
        "SELECT current_version FROM installs WHERE software=? AND machine=?",
        (sw.name, inst.machine)).fetchone()
    current_version = cur_row["current_version"] if cur_row else None

    try:
        cmd, machine = build_update(inv, sw, inst, latest_version=latest_version,
                                    current_version=current_version, changelog_zh=changelog_zh)
    except (ValueError, KeyError) as e:
        db.append_job_log(conn, job_id, f"[build error] {e}")
        db.finish_job(conn, job_id, "failed", -1)
        db.add_event(conn, "update", sw.name, inst.machine, f"job {job_id} build error: {e}")
        return
    cwd = inst.update.cwd if inst.update.type == "agent" else None

    try:
        res = execute(machine, cmd, cwd=cwd, on_line=lambda ln: db.append_job_log(conn, job_id, ln))
    except Exception as e:
        db.append_job_log(conn, job_id, f"[error] {e}")
        db.finish_job(conn, job_id, "failed", -1)
        db.add_event(conn, "update", sw.name, inst.machine, f"job {job_id} crashed: {e}")
        return

    new_version = None
    if res.exit_code == 0:
        try:
            verify = execute(machine, inst.current_cmd,
                             on_line=lambda ln: db.append_job_log(conn, job_id, ln))
            new_version = parse_version(verify.output, inst.version_regex)
            install_status = "up_to_date" if new_version else "unknown"
            db.upsert_install(conn, sw.name, inst.machine, new_version, install_status, _now())
        except Exception as e:
            db.append_job_log(conn, job_id, f"[verify error] {e}")
    status = "success" if res.exit_code == 0 else "failed"
    db.finish_job(conn, job_id, status, res.exit_code, new_version=new_version)
    db.add_event(conn, "update", sw.name, inst.machine,
                 f"job {job_id} {status} exit={res.exit_code} new={new_version}")


def _now() -> str:
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).isoformat()
