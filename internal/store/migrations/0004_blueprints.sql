-- 0004_blueprints.sql — T-603 blueprints: user-authored, parameterized
-- topology templates (docs/features/blueprints.md §1). App-owned data per
-- CLAUDE.md's storage rule ("vnprox's SQLite store holds only app-owned
-- data ... never persist a shadow copy of PVE config"): a blueprint is a
-- template a user wrote, not a copy of any PVE-owned network object.
--
-- The five bundled starters are NOT rows in this table — they are
-- compiled into the vnproxd binary (internal/blueprint.Starters) and
-- served alongside these rows by GET /blueprints; only user-authored (or
-- captured, or starter-copied-to-edit) blueprints are ever persisted here.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a
-- higher version number.

CREATE TABLE IF NOT EXISTS blueprints (
  id             TEXT PRIMARY KEY,   -- ULID
  name           TEXT NOT NULL,
  blueprint_json TEXT NOT NULL,      -- the full Blueprint, incl. id/name (kept in sync)
  created_by     TEXT NOT NULL,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
