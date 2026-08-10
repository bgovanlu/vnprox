-- 0038_changeset_apply_stages.sql — T-2602's canary / staged multi-node apply.
--
-- changeset_apply_stages holds the PAUSED STATE of a staged apply: the one
-- row that exists between the canary stage completing and the sequence
-- either being promoted (the remaining nodes apply) or aborted (only the
-- stages that ran are restored).
--
-- It is a table, not an in-memory map, for exactly one reason: the card's
-- AC4. A daemon killed mid-hold must come back and either resume or roll
-- back **per the recorded strategy** — a changeset must never be left in an
-- unknown state because the process that knew about it died. Everything a
-- recovery needs is therefore here: which strategy was recorded, which
-- nodes have already been mutated, which have not been touched at all, and
-- the two absolute deadlines (the hold's own, and the commit-confirm
-- window's, which covers the WHOLE sequence and always wins).
--
-- It is app-owned intent and bookkeeping, never a shadow copy of PVE
-- config (CLAUDE.md) — PVE has no notion of a staged apply at all.
--
-- One row per changeset, deleted the moment the sequence leaves the paused
-- state by any path (promoted, aborted, recovered). A changeset with no row
-- here was applied the ordinary all-at-once way, which is what every
-- pre-T-2602 changeset is and what every changeset still is by default.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version.

CREATE TABLE IF NOT EXISTS changeset_apply_stages (
    changeset_id     TEXT PRIMARY KEY,
    -- 'canary_hold'  — the canary stage completed; the sequence is paused.
    -- 'promoting'    — a continue is executing the remaining stage right now.
    -- A crash in 'promoting' is unknowable-progress by definition, so
    -- recovery restores everything the plan touches and fails the changeset,
    -- exactly as recovery of an interrupted ordinary apply already does.
    state            TEXT NOT NULL,
    strategy_json    TEXT NOT NULL,          -- the recorded change.ApplyStrategy
    applied_nodes    TEXT NOT NULL,          -- JSON array: nodes whose steps have run
    pending_nodes    TEXT NOT NULL,          -- JSON array: nodes not yet contacted for a write
    author           TEXT NOT NULL DEFAULT '',
    hold_started_at  INTEGER NOT NULL,       -- unix seconds
    hold_deadline    INTEGER NOT NULL,       -- unix seconds; always <= confirm_deadline
    confirm_deadline INTEGER NOT NULL,       -- unix seconds; the WHOLE sequence's commit-confirm deadline
    FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON DELETE CASCADE
);
