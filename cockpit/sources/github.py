from __future__ import annotations
import os
import httpx
from cockpit.sources import SourceResult


def _headers():
    token = os.environ.get("COCKPIT_GITHUB_TOKEN")
    return {"Authorization": f"Bearer {token}"} if token else {}


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    r = client.get(f"https://api.github.com/repos/{locator}/releases/latest", headers=_headers())
    r.raise_for_status()
    data = r.json()
    version = data["tag_name"].lstrip("v")
    return SourceResult(version=version, changelog_raw=data.get("body"))


def release_body(repo, version, client: httpx.Client) -> str | None:
    for tag in (f"v{version}", version):
        r = client.get(f"https://api.github.com/repos/{repo}/releases/tags/{tag}",
                       headers=_headers())
        if r.status_code == 200:
            return r.json().get("body")
    return None
