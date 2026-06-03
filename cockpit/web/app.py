from __future__ import annotations
import asyncio
import threading
from pathlib import Path

from fastapi import FastAPI, HTTPException
from fastapi.staticfiles import StaticFiles
from sse_starlette.sse import EventSourceResponse

from cockpit import db, jobs
from cockpit.collector import refresh_upstream
from cockpit.models import Inventory
from cockpit.version_parse import compare

STATIC_DIR = Path(__file__).with_name("static")


def _latest_map(conn) -> dict[str, str]:
    return db.latest_version_map(conn)


def _spawn_job(conn, inv, job_id):
    threading.Thread(target=jobs.run_job, args=(conn, inv, job_id), daemon=True).start()


def create_app(conn, inv: Inventory) -> FastAPI:
    app = FastAPI(title="cockpit")

    @app.get("/api/installs")
    def list_installs():
        latest = _latest_map(conn)
        kind_of = {sw.name: sw.kind for sw in inv.software}
        update_kind_of = {(sw.name, ins.machine): ins.update.type
                          for sw in inv.software for ins in sw.installs}
        out = []
        for row in db.list_installs(conn):
            sw_name, m = row["software"], row["machine"]
            lv = latest.get(sw_name)
            status = row["status"]
            _, behind_count = compare(row["current_version"], lv)
            err = None
            if status == "error":
                e = db.get_last_error(conn, sw_name, m)
                err = e["detail"] if e else None
            out.append({
                "id": f"{sw_name}::{m}",
                "software": sw_name, "machine": m,
                "kind": kind_of.get(sw_name),
                "current_version": row["current_version"],
                "latest_version": lv,
                "status": status,
                "behind_count": behind_count,
                "update_kind": update_kind_of.get((sw_name, m)),
                "error": err,
                "last_checked": row["last_checked"],
            })
        return out

    @app.get("/api/machines")
    def list_machines():
        return list(inv.machines)

    @app.get("/api/jobs")
    def list_jobs(limit: int = 50):
        return [dict(j) for j in db.list_jobs(conn, limit)]

    @app.get("/api/changelog/{software}/{version}")
    def changelog(software: str, version: str):
        v = db.get_version(conn, software, version)
        if not v:
            raise HTTPException(404, "version not found")
        return {"software": software, "version": version,
                "changelog_zh": v["changelog_zh"], "changelog_raw": v["changelog_raw"],
                "released_at": v["released_at"]}

    @app.post("/api/check")
    def check():
        threading.Thread(target=refresh_upstream, args=(conn, inv), daemon=True).start()
        return {"started": True}

    @app.post("/api/installs/{software}/{machine}/update")
    def trigger_update(software: str, machine: str):
        try:
            jid = jobs.start_job(conn, inv, software, machine)
        except KeyError:
            raise HTTPException(404, "install not found")
        except jobs.ActiveJobExists:
            raise HTTPException(409, "update already in progress")
        _spawn_job(conn, inv, jid)
        return {"job_id": jid}

    @app.get("/api/jobs/{job_id}")
    def get_job(job_id: int):
        job = db.get_job(conn, job_id)
        if not job:
            raise HTTPException(404, "job not found")
        return dict(job)

    @app.post("/api/jobs/{job_id}/abort")
    def abort_update(job_id: int):
        job = jobs.request_abort(conn, job_id)
        if job is None:
            raise HTTPException(404, "job not found")
        return dict(job)

    @app.get("/api/jobs/{job_id}/log/stream")
    async def stream_log(job_id: int):
        async def gen():
            sent = 0
            while True:
                job = db.get_job(conn, job_id)
                if not job:
                    yield {"event": "error", "data": "job not found"}
                    return
                log = job["log"] or ""
                lines = log.split("\n")
                # 已完成的行（最後一段可能是未換行的殘段，這裡 log 都以 \n 結尾）
                ready = lines[:-1] if log.endswith("\n") else (lines if log else [])
                for line in ready[sent:]:
                    yield {"event": "log", "data": line}
                sent = len(ready)
                if job["status"] in ("success", "failed", "aborted"):
                    yield {"event": "done", "data": job["status"]}
                    return
                await asyncio.sleep(0.5)
        return EventSourceResponse(gen())

    if STATIC_DIR.exists():
        app.mount("/", StaticFiles(directory=str(STATIC_DIR), html=True), name="static")

    return app
