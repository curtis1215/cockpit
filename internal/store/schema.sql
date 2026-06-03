CREATE TABLE IF NOT EXISTS systems (
  id            TEXT PRIMARY KEY,
  label         TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT '',
  os            TEXT NOT NULL DEFAULT '',
  arch          TEXT NOT NULL DEFAULT '',
  kind          TEXT NOT NULL DEFAULT 'physical',
  host_id       TEXT,
  status        TEXT NOT NULL DEFAULT 'pending',
  agent_version TEXT NOT NULL DEFAULT '',
  agent_status  TEXT NOT NULL DEFAULT 'pending',
  last_seen     TEXT NOT NULL DEFAULT '',
  agent_token   TEXT UNIQUE,
  created       INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS versions (
  software TEXT NOT NULL, version TEXT NOT NULL, released_at TEXT,
  changelog_raw TEXT, changelog_zh TEXT, fetched_at TEXT DEFAULT (datetime('now')),
  PRIMARY KEY (software, version)
);
CREATE TABLE IF NOT EXISTS installs (
  software TEXT NOT NULL, machine TEXT NOT NULL, current_version TEXT,
  status TEXT NOT NULL DEFAULT 'unknown', last_checked TEXT,
  PRIMARY KEY (software, machine)
);
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, software TEXT NOT NULL, machine TEXT NOT NULL,
  kind TEXT NOT NULL, runner TEXT, status TEXT NOT NULL DEFAULT 'queued',
  started_at TEXT, finished_at TEXT, exit_code INTEGER, new_version TEXT,
  log TEXT NOT NULL DEFAULT '', cmd TEXT, cwd TEXT, current_cmd TEXT, version_regex TEXT,
  abort_requested INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT DEFAULT (datetime('now')),
  type TEXT NOT NULL, software TEXT, machine TEXT, detail TEXT
);
CREATE TABLE IF NOT EXISTS machine_state (
  machine TEXT PRIMARY KEY, check_requested INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT DEFAULT (datetime('now'))
);
