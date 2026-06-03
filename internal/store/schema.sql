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
