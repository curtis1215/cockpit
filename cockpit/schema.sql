CREATE TABLE IF NOT EXISTS installs (
  software TEXT NOT NULL,
  machine TEXT NOT NULL,
  current_version TEXT,
  status TEXT NOT NULL DEFAULT 'unknown',
  last_checked TEXT,
  PRIMARY KEY (software, machine)
);

CREATE TABLE IF NOT EXISTS versions (
  software TEXT NOT NULL,
  version TEXT NOT NULL,
  released_at TEXT,
  changelog_raw TEXT,
  changelog_zh TEXT,
  fetched_at TEXT DEFAULT (datetime('now')),
  PRIMARY KEY (software, version)
);

CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  software TEXT NOT NULL,
  machine TEXT NOT NULL,
  kind TEXT NOT NULL,
  runner TEXT,
  status TEXT NOT NULL DEFAULT 'queued',
  started_at TEXT,
  finished_at TEXT,
  exit_code INTEGER,
  new_version TEXT,
  log TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT DEFAULT (datetime('now')),
  type TEXT NOT NULL,
  software TEXT,
  machine TEXT,
  detail TEXT
);
