from __future__ import annotations
import re

_SEMVER = re.compile(r"(\d+(?:\.\d+){1,3})")


def parse_version(text: str, regex: str | None = None) -> str | None:
    if text is None:
        return None
    pattern = re.compile(regex) if regex else _SEMVER
    m = pattern.search(text)
    return m.group(1) if m else None


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
