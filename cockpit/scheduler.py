from __future__ import annotations
from apscheduler.schedulers.background import BackgroundScheduler


def build_scheduler(func, hours: int) -> BackgroundScheduler:
    sch = BackgroundScheduler()
    sch.add_job(func, "interval", hours=hours, id="collection")
    return sch
