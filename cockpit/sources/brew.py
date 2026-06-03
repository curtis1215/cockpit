from __future__ import annotations
import httpx
from cockpit.sources import SourceResult


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    r = client.get(f"https://formulae.brew.sh/api/formula/{locator}.json")
    r.raise_for_status()
    version = r.json()["versions"]["stable"]
    return SourceResult(version=version, changelog_raw=None)
