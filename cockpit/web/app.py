from __future__ import annotations
import threading
from pathlib import Path

from fastapi import FastAPI, HTTPException
from fastapi.staticfiles import StaticFiles

from cockpit import db, jobs
from cockpit.collector import run_collection
from cockpit.models import Inventory

STATIC_DIR = Path(__file__).with_name("static")


def _latest_map(conn) -> dict[str, str]:
    rows = conn.execute(
        "SELECT software, version FROM versions ORDER BY rowid").fetchall()
    return {r["software"]: r["version"] for r in rows}  # 後者覆蓋前者＝最新


def _spawn_job(conn, inv, job_id):
    threading.Thread(target=jobs.run_job, args=(conn, inv, job_id), daemon=True).start()


def create_app(conn, inv: Inventory) -> FastAPI:
    app = FastAPI(title="cockpit")

    @app.get("/api/installs")
    def list_installs():
        latest = _latest_map(conn)
        out = []
        for row in db.list_installs(conn):
            out.append({
                "software": row["software"], "machine": row["machine"],
                "current_version": row["current_version"], "status": row["status"],
                "last_checked": row["last_checked"],
                "latest_version": latest.get(row["software"]),
            })
        return out

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
        threading.Thread(target=run_collection, args=(conn, inv), daemon=True).start()
        return {"started": True}

    @app.post("/api/installs/{software}/{machine}/update")
    def trigger_update(software: str, machine: str):
        try:
            jid = jobs.start_job(conn, inv, software, machine)
        except KeyError:
            raise HTTPException(404, "install not found")
        _spawn_job(conn, inv, jid)
        return {"job_id": jid}

    @app.get("/api/jobs/{job_id}")
    def get_job(job_id: int):
        job = db.get_job(conn, job_id)
        if not job:
            raise HTTPException(404, "job not found")
        return dict(job)

    if STATIC_DIR.exists():
        app.mount("/", StaticFiles(directory=str(STATIC_DIR), html=True), name="static")

    return app
