from __future__ import annotations
import asyncio

from fastapi import APIRouter, Header, HTTPException, Request, Response

from cockpit import db, jobs
from cockpit.collector import apply_version_report
from cockpit.inventory import machine_for_token

POLL_MAX_WAIT = 25.0    # long-poll 上限秒
POLL_TICK = 0.5


def _machine(inv, authorization: str | None) -> str:
    if not authorization or not authorization.startswith("Bearer "):
        raise HTTPException(401, "missing agent token")
    token = authorization.split(" ", 1)[1].strip()
    machine = machine_for_token(inv, token)
    if machine is None:
        raise HTTPException(401, "invalid agent token")
    return machine


def build_agent_router(conn, inv) -> APIRouter:
    r = APIRouter(prefix="/api/agent")

    @r.get("/installs")
    def agent_installs(authorization: str | None = Header(default=None)):
        machine = _machine(inv, authorization)
        out = []
        for sw in inv.software:
            for inst in sw.installs:
                if inst.machine == machine:
                    out.append({"software": sw.name, "current_cmd": inst.current_cmd,
                                "version_regex": inst.version_regex})
        return out

    @r.post("/report-versions")
    async def agent_report(request: Request, authorization: str | None = Header(default=None)):
        machine = _machine(inv, authorization)
        reports = await request.json()
        applied = apply_version_report(conn, inv, machine, reports)
        return {"applied": applied}

    @r.get("/poll")
    async def agent_poll(wait: float = POLL_MAX_WAIT,
                         authorization: str | None = Header(default=None)):
        machine = _machine(inv, authorization)
        deadline = min(wait, POLL_MAX_WAIT)
        waited = 0.0
        while True:
            claimed = jobs.claim_next_job(conn, inv, machine)
            if claimed is not None:
                return {"type": "job", "job": claimed}
            if db.take_check_requested(conn, machine):
                return {"type": "check"}
            if waited >= deadline:
                return Response(status_code=204)
            await asyncio.sleep(POLL_TICK)
            waited += POLL_TICK

    @r.post("/jobs/{job_id}/log")
    async def agent_log(job_id: int, request: Request,
                        authorization: str | None = Header(default=None)):
        _machine(inv, authorization)
        body = await request.json()
        for line in body.get("lines", []):
            db.append_job_log(conn, job_id, line)
        return Response(status_code=204)

    @r.post("/jobs/{job_id}/result")
    async def agent_result(job_id: int, request: Request,
                           authorization: str | None = Header(default=None)):
        _machine(inv, authorization)
        body = await request.json()
        jobs.record_result(conn, inv, job_id, body.get("status", "failed"),
                           body.get("exit_code", -1), body.get("new_version"))
        return dict(db.get_job(conn, job_id))

    @r.get("/jobs/{job_id}/control")
    def agent_control(job_id: int, authorization: str | None = Header(default=None)):
        _machine(inv, authorization)
        return {"abort": db.abort_requested(conn, job_id)}

    return r
