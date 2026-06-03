from __future__ import annotations
import shlex

from cockpit import db
from cockpit.models import Inventory, Software, Install, Machine


class ActiveJobExists(Exception):
    pass


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
    jid = db.create_job_unique(conn, software, machine, inst.update.type,
                               runner=inst.update.runner)
    if jid is None:
        raise ActiveJobExists(f"active job already exists for {software}@{machine}")
    return jid


def claim_next_job(conn, inv: Inventory, machine: str) -> dict | None:
    """原子取該機最舊 queued job（標 running）、渲染指令、寫入 dispatch 欄位，回 dict。"""
    row = db.claim_oldest_queued(conn, machine)   # 原子 queued→running
    if row is None:
        return None
    job_id = row["id"]
    sw, inst = _find(inv, row["software"], row["machine"])
    latest = db.get_latest_version(conn, sw.name)
    latest_version = latest["version"] if latest else None
    changelog_zh = latest["changelog_zh"] if latest else None
    cur = db.get_install(conn, sw.name, inst.machine)
    current_version = cur["current_version"] if cur else None
    cmd, _machine = build_update(inv, sw, inst, latest_version=latest_version,
                                 current_version=current_version, changelog_zh=changelog_zh)
    cwd = inst.update.cwd if inst.update.type == "agent" else None
    db.set_job_dispatch(conn, job_id, cmd=cmd, cwd=cwd,
                        current_cmd=inst.current_cmd, version_regex=inst.version_regex)
    return {
        "id": job_id, "software": sw.name, "machine": inst.machine,
        "shell_cmd": cmd, "cwd": cwd,
        "current_cmd": inst.current_cmd, "version_regex": inst.version_regex,
    }


def record_result(conn, inv: Inventory, job_id: int, status: str,
                  exit_code: int, new_version: str | None) -> None:
    job = db.get_job(conn, job_id)
    if job is None:
        return
    if status == "success" and new_version:
        db.upsert_install(conn, job["software"], job["machine"], new_version,
                          "up_to_date", _now())
    db.finish_job(conn, job_id, status, exit_code, new_version=new_version)
    db.add_event(conn, "update", job["software"], job["machine"],
                 f"job {job_id} {status} exit={exit_code} new={new_version}")


def request_abort(conn, job_id: int) -> dict | None:
    job = db.get_job(conn, job_id)
    if job is None:
        return None
    if job["status"] == "queued":
        db.finish_job(conn, job_id, "aborted", -1)
        db.add_event(conn, "update", job["software"], job["machine"], f"job {job_id} aborted (queued)")
        return dict(db.get_job(conn, job_id))
    if job["status"] == "running":
        db.request_abort(conn, job_id)
    return dict(db.get_job(conn, job_id))


def _now() -> str:
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).isoformat()
