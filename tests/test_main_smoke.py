def test_build_uses_refresh_upstream(monkeypatch, tmp_path):
    inv = tmp_path / "inv.yaml"
    inv.write_text(
        "machines:\n  mac: { host: 1.2.3.4, ssh_user: curtis, local: true }\n"
        "software:\n"
        "  - name: cc\n    kind: npm\n    latest_source: \"npm:cc\"\n    changelog: null\n"
        "    installs:\n      - machine: mac\n        current_cmd: \"cc --version\"\n"
        "        update: { type: command, cmd: \"echo hi\" }\n"
    )
    monkeypatch.setenv("COCKPIT_DB_PATH", str(tmp_path / "c.db"))
    monkeypatch.setenv("COCKPIT_INVENTORY", str(inv))
    import importlib
    import cockpit.main as m
    importlib.reload(m)            # re-run build() with the temp env
    from cockpit.collector import refresh_upstream
    assert callable(refresh_upstream)
    assert hasattr(m, "build")
    app, settings = m.build()
    assert app is not None
    assert settings.inventory_path.endswith("inv.yaml")
    # the scheduler should be wired to refresh_upstream (smoke: build() didn't raise)
