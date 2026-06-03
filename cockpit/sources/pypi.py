from __future__ import annotations
import httpx
from cockpit.sources import SourceResult


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    r = client.get(f"https://pypi.org/pypi/{locator}/json")
    r.raise_for_status()
    version = r.json()["info"]["version"]
    changelog = None
    if software.changelog and software.changelog.startswith("github:"):
        from cockpit.sources.github import release_body
        changelog = release_body(software.changelog.split(":", 1)[1], version, client)
    return SourceResult(version=version, changelog_raw=changelog)
