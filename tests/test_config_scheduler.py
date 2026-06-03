from cockpit.config import Settings
from cockpit import scheduler


def test_settings_from_env(monkeypatch, tmp_path):
    monkeypatch.setenv("COCKPIT_DB_PATH", str(tmp_path / "c.db"))
    monkeypatch.setenv("COCKPIT_INVENTORY", str(tmp_path / "inv.yaml"))
    monkeypatch.setenv("COCKPIT_CHECK_HOURS", "6")
    s = Settings.from_env()
    assert s.db_path.endswith("c.db")
    assert s.check_hours == 6


def test_scheduler_registers_job(tmp_path):
    calls = []
    sch = scheduler.build_scheduler(lambda: calls.append(1), hours=12)
    jobs = sch.get_jobs()
    assert len(jobs) == 1
    from apscheduler.schedulers import SchedulerNotRunningError
    try:
        sch.shutdown(wait=False)
    except SchedulerNotRunningError:
        pass
