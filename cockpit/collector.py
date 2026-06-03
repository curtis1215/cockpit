from __future__ import annotations
from datetime import datetime, timezone

from cockpit import db
from cockpit.models import Inventory
from cockpit.sources import fetch_latest as _default_fetch
from cockpit.runner import execute as _default_execute
from cockpit.translate import translate_changelog as _default_translate
from cockpit.version_parse import parse_version, compare


def run_collection(conn, inv: Inventory, *, fetch=_default_fetch,
                   execute=_default_execute, translate=_default_translate) -> None:
    now = datetime.now(timezone.utc).isoformat()
    for sw in inv.software:
        try:
            latest = fetch(sw)
        except Exception as e:
            db.add_event(conn, "error", sw.name, None, f"fetch failed: {e}")
            continue

        # 記錄上游版本；若是新版且尚未翻譯，翻譯 changelog
        existing = db.get_version(conn, sw.name, latest.version)
        zh = existing["changelog_zh"] if existing else None
        if zh is None and latest.changelog_raw:
            zh = translate(latest.changelog_raw)
        db.add_version(conn, sw.name, latest.version, None, latest.changelog_raw, zh)

        for inst in sw.installs:
            machine = inv.machines[inst.machine]
            try:
                res = execute(machine, inst.current_cmd)
                current = parse_version(res.output, inst.version_regex)
                status, _ = compare(current, latest.version)
            except Exception as e:
                db.add_event(conn, "error", sw.name, inst.machine, f"current_cmd failed: {e}")
                current, status = None, "error"
            db.upsert_install(conn, sw.name, inst.machine, current, status, now)
            db.add_event(conn, "check", sw.name, inst.machine,
                         f"current={current} latest={latest.version} status={status}")
