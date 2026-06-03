from __future__ import annotations
import subprocess
import httpx
from cockpit.sources import SourceResult
from cockpit.version_parse import parse_version


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    # locator 是一個本地 shell 指令，stdout 內含版本字串
    out = subprocess.run(["bash", "-lc", locator], capture_output=True, text=True, timeout=60)
    if out.returncode != 0:
        raise RuntimeError(
            f"custom source 指令失敗 (exit {out.returncode}): {out.stderr.strip()!r}"
        )
    version = parse_version(out.stdout.strip()) or out.stdout.strip()
    return SourceResult(version=version, changelog_raw=None)
