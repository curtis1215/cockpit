import httpx
from cockpit.models import Software
from cockpit.sources import fetch_latest


def _client(handler):
    return httpx.Client(transport=httpx.MockTransport(handler))


def test_npm_latest():
    def handler(req):
        assert "registry.npmjs.org" in str(req.url)
        return httpx.Response(200, json={"dist-tags": {"latest": "2.1.101"}})

    sw = Software(name="claude-code", kind="npm",
                  latest_source="npm:@anthropic-ai/claude-code", changelog=None, installs=[])
    res = fetch_latest(sw, client=_client(handler))
    assert res.version == "2.1.101"


def test_github_latest_and_changelog():
    def handler(req):
        return httpx.Response(200, json={"tag_name": "v0.9.0", "body": "## 0.9.0\n- fix"})

    sw = Software(name="multica", kind="github",
                  latest_source="github:o/multica", changelog="github:o/multica", installs=[])
    res = fetch_latest(sw, client=_client(handler))
    assert res.version == "0.9.0"
    assert "fix" in res.changelog_raw
