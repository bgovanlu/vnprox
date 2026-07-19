-- 0012_pinned_spec.sql — T-1102 "Pinned-spec drift mode": the GitOps
-- reconciler's declared desired state. This is app-owned data per
-- docs/architecture.md §7/CLAUDE.md's storage rule (the pin is vnprox's own
-- record of what an operator asked to reconcile toward, never a shadow copy
-- of PVE-authoritative config) — the same status T-1101's "pin nodes to
-- blueprint" P1 note in docs/features/blueprints.md §2 already flagged.
--
-- Singleton row (id fixed to 1 via CHECK): there is exactly one cluster-wide
-- pinned spec at a time, not one per user or per node — GET/POST/DELETE
-- /spec/pin (internal/api/specpin.go) all operate on this one row.
-- internal/drift's spec_drift check family (internal/drift/specdrift.go)
-- reads content fresh every drift cycle and reconciles it against live state
-- via internal/spec.Import (T-1101's diff engine), reused unchanged.
--
-- content is the raw pinned YAML document (specVersion: 1, internal/spec's
-- own schema — docs/data-model.md §6); pinned_by/pinned_at are the acting
-- user and unix-seconds timestamp POST /spec/pin recorded, surfaced by
-- GET /spec/pin for display ("pinned by X at Y").
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number. Note for the next agent: 0010/0011 are reserved for
-- in-flight sibling tasks (T-1103 scheduled changesets, T-1104 event
-- stream/tokens) landing independently — this file starts at 0012 rather
-- than colliding with either, the same "reserved gap" precedent
-- docs/data-model.md §2 already documents for 0007.

CREATE TABLE IF NOT EXISTS pinned_spec (
  id        INTEGER PRIMARY KEY CHECK (id = 1),
  content   TEXT NOT NULL,
  pinned_by TEXT NOT NULL,
  pinned_at INTEGER NOT NULL
);
