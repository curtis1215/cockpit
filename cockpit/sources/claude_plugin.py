from __future__ import annotations
import httpx
from cockpit.sources import SourceResult
from cockpit.sources.github import fetch as gh_fetch


def fetch(software, locator, client: httpx.Client) -> SourceResult:
    # claude-plugin 的版本來自其來源 GitHub repo 的最新 release
    return gh_fetch(software, locator, client)
