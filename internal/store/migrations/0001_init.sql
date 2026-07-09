-- 0001_init.sql — initial schema, per docs/data-model.md §2 ("App store
-- (SQLite)"). Table and column names/types here are a contract other tasks
-- depend on; do not rename without updating that doc.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS sessions (
  id             TEXT PRIMARY KEY,
  username       TEXT NOT NULL,
  realm          TEXT NOT NULL,
  pve_ticket_enc BLOB NOT NULL,
  csrf_token_enc BLOB NOT NULL,
  caps_json      TEXT NOT NULL,
  created_at     INTEGER NOT NULL,
  expires_at     INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_username ON sessions (username);

CREATE TABLE IF NOT EXISTS changesets (
  id               TEXT PRIMARY KEY,             -- ULID
  title            TEXT,
  author           TEXT NOT NULL,
  status           TEXT NOT NULL,                -- draft|validated|applying|awaiting_confirm|
                                                  -- committed|rolled_back|failed|discarded
  ops_json         TEXT NOT NULL,                -- ordered []Op
  findings_json    TEXT,                         -- validation results
  plan_json        TEXT,                         -- ordered apply steps (rendered pre-apply)
  apply_log_json   TEXT,                         -- per-step outcomes
  confirm_deadline INTEGER,                      -- unix; NULL unless awaiting_confirm
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_changesets_status ON changesets (status);
CREATE INDEX IF NOT EXISTS idx_changesets_author ON changesets (author);

CREATE TABLE IF NOT EXISTS snapshots (
  id           TEXT PRIMARY KEY,
  changeset_id TEXT REFERENCES changesets (id),
  taken_at     INTEGER NOT NULL,
  kind         TEXT NOT NULL,                    -- pre|post|manual|scheduled
  files_json   TEXT NOT NULL                     -- [{node,path,sha256,content_zstd}]
);

CREATE INDEX IF NOT EXISTS idx_snapshots_changeset_id ON snapshots (changeset_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_taken_at ON snapshots (taken_at);

CREATE TABLE IF NOT EXISTS audit_log (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  at           INTEGER NOT NULL,
  username     TEXT NOT NULL,
  action       TEXT NOT NULL,
  target       TEXT,
  changeset_id TEXT,
  result       TEXT NOT NULL,
  detail_json  TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_log_at ON audit_log (at);
CREATE INDEX IF NOT EXISTS idx_audit_log_changeset_id ON audit_log (changeset_id);

CREATE TABLE IF NOT EXISTS layouts (
  username    TEXT NOT NULL,
  name        TEXT NOT NULL,
  layout_json TEXT NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (username, name)
);

CREATE TABLE IF NOT EXISTS metric_samples (
  ref      TEXT NOT NULL,
  at       INTEGER NOT NULL,
  rx_bytes INTEGER,
  tx_bytes INTEGER,
  rx_pkts  INTEGER,
  tx_pkts  INTEGER,
  rx_errs  INTEGER,
  tx_errs  INTEGER,
  rx_drop  INTEGER,
  tx_drop  INTEGER,
  PRIMARY KEY (ref, at)
); -- pruned to 24h; longer horizons are out of scope for v1

CREATE TABLE IF NOT EXISTS kv (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
