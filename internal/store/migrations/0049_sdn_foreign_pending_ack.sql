-- 0049_sdn_foreign_pending_ack.sql — T-3101-followup-01 (debt-sweep
-- 2026-08-19, item 2): "surface and confirm" for sdn.apply's foreign-
-- pending-SDN-state gap.
--
-- PVE's own PUT /cluster/sdn applies ALL pending SDN config cluster-wide,
-- including edits staged outside vnprox's change engine (e.g. via the PVE
-- GUI) that never went through stage -> validate -> diff -> apply ->
-- confirm/rollback. The owner's decision
-- (planning/tasks/debt-sweep-2026-08-19.md): detect it, show it on the
-- review screen, and require an explicit, server-recorded operator
-- acknowledgement before apply proceeds — never a client-supplied boolean.
-- changeset_approvals (0034) set this precedent: an authorization decision
-- must be readable back from a row a prior, separately-audited call wrote,
-- never trusted from the apply request itself.
--
-- One row per changeset: the latest acknowledgement, replaced (not
-- history — the audit log is where that history lives) each time the
-- operator re-acknowledges. entries_json is the exact foreign-pending
-- list (change.SDNPendingEntry, JSON) the operator was actually shown at
-- acknowledgement time — kept for audit and for the "honest diff"
-- property CLAUDE.md requires here: proof of precisely what was
-- additionally committed, not just that a checkbox was ticked.
-- internal/change/apply.go's beginApply re-detects live at apply time and
-- refuses unless the current foreign-pending set is already covered by
-- this row — so a stale ack (foreign state changed after the operator
-- acknowledged it) does not silently cover a different set of changes.
--
-- App-owned data only (CLAUDE.md: vnprox's SQLite store never holds a
-- shadow copy of PVE config as authoritative state) — entries_json is a
-- point-in-time acknowledgement record, not a cache of PVE's SDN config.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS changeset_sdn_pending_acks (
  changeset_id    TEXT PRIMARY KEY REFERENCES changesets(id) ON DELETE CASCADE,
  acknowledged_by TEXT NOT NULL,
  entries_json    TEXT NOT NULL,
  acknowledged_at INTEGER NOT NULL
);
