from __future__ import annotations
import re

# 抓 X.Y / X.Y.Z / X.Y.Z.W（下限兩段是刻意的）
_SEMVER = re.compile(r"(\d+(?:\.\d+){1,3})")


def parse_version(text: str, regex: str | None = None) -> str | None:
    if text is None:
        return None
    if regex:
        try:
            pattern = re.compile(regex)
        except re.error:
            return None
    else:
        pattern = _SEMVER
    m = pattern.search(text)
    if not m:
        return None
    # 若自訂 regex 沒有 capture group，退回整段 match（避免 group(1) 崩潰）
    return m.group(1 if m.lastindex else 0)


def _key(v: str) -> list[int]:
    return [int(p) for p in v.split(".")]


def _pad(a: list[int], b: list[int]) -> tuple[list[int], list[int]]:
    n = max(len(a), len(b))
    return a + [0] * (n - len(a)), b + [0] * (n - len(b))


def compare(current: str | None, latest: str | None) -> tuple[str, int]:
    """回傳 (status, behind_count)。"""
    if not current or not latest:
        return ("unknown", 0)
    try:
        ck, lk = _pad(_key(current), _key(latest))
    except ValueError:
        return ("unknown", 0)
    if ck >= lk:
        return ("up_to_date", 0)
    # behind_count：以最末段差距估算，至少 1
    behind = lk[-1] - ck[-1] if lk[:-1] == ck[:-1] else 1
    return ("behind", max(behind, 1))
