from cockpit import translate


def test_translate_calls_claude(monkeypatch):
    seen = {}

    class FakeCompleted:
        returncode = 0
        stdout = "翻譯後的中文摘要"

    def fake_run(cmd, **kw):
        seen["cmd"] = cmd
        seen["input"] = kw.get("input")
        return FakeCompleted()

    monkeypatch.setattr(translate.subprocess, "run", fake_run)
    out = translate.translate_changelog("## 1.0\n- fix bug")
    assert out == "翻譯後的中文摘要"
    assert seen["cmd"][0] == "claude"
    assert "-p" in seen["cmd"]


def test_translate_empty_returns_none():
    assert translate.translate_changelog("") is None
    assert translate.translate_changelog(None) is None


def test_translate_failure_returns_none(monkeypatch):
    def boom(cmd, **kw):
        raise RuntimeError("claude not found")

    monkeypatch.setattr(translate.subprocess, "run", boom)
    assert translate.translate_changelog("notes") is None
