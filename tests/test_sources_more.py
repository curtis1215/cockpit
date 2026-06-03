import httpx
from cockpit.models import Software
from cockpit.sources import fetch_latest


def _client(handler):
    return httpx.Client(transport=httpx.MockTransport(handler))


def test_pypi():
    def handler(req):
        return httpx.Response(200, json={"info": {"version": "1.4.2"}})
    sw = Software(name="x", kind="pypi", latest_source="pypi:somepkg", changelog=None, installs=[])
    assert fetch_latest(sw, client=_client(handler)).version == "1.4.2"


def test_brew():
    def handler(req):
        return httpx.Response(200, json={"versions": {"stable": "3.2.1"}})
    sw = Software(name="x", kind="brew", latest_source="brew:wget", changelog=None, installs=[])
    assert fetch_latest(sw, client=_client(handler)).version == "3.2.1"


def test_claude_plugin_uses_github_release():
    def handler(req):
        return httpx.Response(200, json={"tag_name": "1.4.0", "body": "notes"})
    sw = Software(name="super-telegram", kind="claude-plugin",
                  latest_source="claude-plugin:curtis1215/super-telegram-plugin",
                  changelog="github:curtis1215/super-telegram-plugin", installs=[])
    res = fetch_latest(sw, client=_client(handler))
    assert res.version == "1.4.0"


def test_custom_uses_latest_cmd(monkeypatch):
    sw = Software(name="x", kind="custom",
                  latest_source="custom:echo 9.9.9", changelog=None, installs=[])
    res = fetch_latest(sw, client=_client(lambda req: httpx.Response(404)))
    assert res.version == "9.9.9"
