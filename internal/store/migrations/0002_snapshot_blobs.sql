-- 0002_snapshot_blobs.sql — zstd-compressed, hash-deduplicated blob storage
-- for snapshot file contents (T-206, docs/data-model.md §2). 0001_init.sql's
-- snapshots.files_json documented shape was `[{node,path,sha256,
-- content_zstd}]`; T-205 stored inline plaintext `content` instead (a
-- documented interim shape — see planning/reports/T-205.md §3 note 5) and
-- T-206 completes the move to the documented content-addressed blob store:
--
--   - `blobs` holds each distinct file content exactly once, keyed by its
--     sha256 (dedup: two snapshots' identical /etc/network/interfaces content
--     share one row here regardless of how many snapshots reference it).
--   - `snapshot_files` is the normalized per-(snapshot,node,path) reference
--     into `blobs`, so retention pruning can find and delete orphaned blobs
--     with a plain anti-join instead of parsing every snapshot's files_json.
--
-- snapshots.files_json keeps storing `[{node,path,sha256}]` (no inline
-- content) for cheap listing; snapshot_files is the source of truth for
-- blob references.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS blobs (
  sha256       TEXT PRIMARY KEY,
  content_zstd BLOB NOT NULL,
  size         INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS snapshot_files (
  snapshot_id TEXT NOT NULL REFERENCES snapshots (id),
  node        TEXT NOT NULL,
  path        TEXT NOT NULL,
  sha256      TEXT NOT NULL REFERENCES blobs (sha256),
  PRIMARY KEY (snapshot_id, node, path)
);
CREATE INDEX IF NOT EXISTS idx_snapshot_files_sha256 ON snapshot_files (sha256);

-- note: a manual snapshot (docs/api.md POST /snapshots {note}) needs
-- somewhere to keep its user-supplied note.
ALTER TABLE snapshots ADD COLUMN note TEXT;
