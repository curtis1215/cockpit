from __future__ import annotations
import uvicorn

from cockpit import db
from cockpit.config import Settings
from cockpit.inventory import load_inventory
from cockpit.collector import refresh_upstream
from cockpit.scheduler import build_scheduler
from cockpit.web.app import create_app


def build() -> "tuple":
    settings = Settings.from_env()
    conn = db.connect(settings.db_path)
    db.init_db(conn)
    inv = load_inventory(settings.inventory_path)
    app = create_app(conn, inv)

    sch = build_scheduler(lambda: refresh_upstream(conn, inv), hours=settings.check_hours)
    sch.start()
    return app, settings


app, _settings = build()


def main():
    uvicorn.run(app, host="127.0.0.1", port=8787)


if __name__ == "__main__":
    main()
