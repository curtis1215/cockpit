from __future__ import annotations
from dataclasses import dataclass
import httpx

from cockpit.models import Software


@dataclass
class SourceResult:
    version: str
    changelog_raw: str | None = None


def _split(source: str) -> tuple[str, str]:
    provider, _, locator = source.partition(":")
    return provider, locator


def fetch_latest(software: Software, client: httpx.Client | None = None) -> SourceResult:
    from cockpit.sources import npm, github, pypi, brew, claude_plugin, custom

    registry = {
        "npm": npm.fetch,
        "github": github.fetch,
        "pypi": pypi.fetch,
        "brew": brew.fetch,
        "claude-plugin": claude_plugin.fetch,
        "custom": custom.fetch,
    }
    provider, locator = _split(software.latest_source)
    if provider not in registry:
        raise ValueError(f"未知 provider: {provider}")
    owns_client = client is None
    client = client or httpx.Client(timeout=20)
    try:
        return registry[provider](software, locator, client)
    finally:
        if owns_client:
            client.close()
