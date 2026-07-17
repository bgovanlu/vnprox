-- 0006_annotations.sql — T-907 "Saved views & annotations": entity-pinned
-- sticky notes on the topology map. App-owned data per CLAUDE.md's storage
-- rule ("vnprox's SQLite store holds only app-owned data ... never persist
-- a shadow copy of PVE config"): an annotation is free-text a user typed
-- about an entity (a reminder, a warning, a context note), keyed by that
-- entity's Ref string — never a copy of any PVE-owned field. vnproxd never
-- interprets `content`; it is opaque free text, exactly like `layouts`'
-- `layout_json` blob is opaque JSON.
--
-- Unlike `layouts` (per-user, PK (username, name), one row overwritten in
-- place), annotations are a shared, additive team scratchpad on the map —
-- any authenticated netRead-capable user can see every pinned note (not
-- just their own), so this table is NOT keyed by username; `created_by`
-- records the author for display/audit only, it is not part of the
-- identity key. Multiple notes may be pinned to the same entity ref (one
-- row per note, ULID primary key), so creating a second note never
-- overwrites the first — see internal/api/annotations.go's doc comment for
-- the full route/capability rationale.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS annotations (
  id         TEXT PRIMARY KEY,   -- ULID
  ref        TEXT NOT NULL,      -- pinned entity's Ref string (kind:node:id)
  content    TEXT NOT NULL,      -- free text; opaque to vnproxd
  created_by TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_annotations_ref ON annotations (ref);
