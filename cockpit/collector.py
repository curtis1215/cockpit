from __future__ import annotations
from datetime import datetime, timezone

from cockpit import db
from cockpit.models import Inventory
from cockpit.sources import fetch_latest as _default_fetch
from cockpit.translate import translate_changelog as _default_translate
from cockpit.version_parse import compare


def refresh_upstream(conn, inv: Inventory, *, fetch=_default_fetch,
                     translate=_default_translate) -> None:
    """server 端：抓每個 software 的上游最新版 + 翻譯 changelog，存入 versions。"""
    for sw in inv.software:
        try:
            latest = fetch(sw)
        except Exception as e:
            db.add_event(conn, "error", sw.name, None, f"fetch failed: {e}")
            continue
        existing = db.get_version(conn, sw.name, latest.version)
        zh = existing["changelog_zh"] if existing else None
        if zh is None and latest.changelog_raw:
            zh = translate(latest.changelog_raw)
        db.add_version(conn, sw.name, latest.version, None, latest.changelog_raw, zh)


def apply_version_report(conn, inv: Inventory, machine: str, reports) -> int:
    """agent 回報目前版：對每筆比對已知上游最新版、寫 installs。回寫入筆數。"""
    now = datetime.now(timezone.utc).isoformat()
    applied = 0
    for r in reports:
        software = r.get("software")
        current = r.get("current_version")
        if not software:
            continue
        latest = db.get_latest_version(conn, software)
        latest_version = latest["version"] if latest else None
        status, _ = compare(current, latest_version)
        db.upsert_install(conn, software, machine, current, status, now)
        db.add_event(conn, "check", software, machine,
                     f"current={current} latest={latest_version} status={status}")
        applied += 1
    return applied
