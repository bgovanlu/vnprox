-- 0003_node_timers.sql — per-node local commit-confirm rollback timers
-- (T-304, docs/features/change-management.md §4: "each node arms its own
-- local timer at step start — no cross-node dependency for safety").
--
-- This table is written by *any* node's daemon acting as the peer-API
-- target of a coordinator's arm-timer call (docs/architecture.md §5: the
-- coordinating daemon is just "the one the user's browser is talking to" —
-- every node, including a non-coordinating peer, must be able to record and
-- honor an armed rollback deadline for a changeset it knows nothing else
-- about). It is deliberately independent of the `changesets` table: a peer
-- node being asked to arm a timer for changeset X may have no `changesets`
-- row for X at all (that row lives only in the coordinating daemon's own
-- store) — node_timers carries everything this node needs to self-restore
-- (the pre-apply content) without consulting anything else.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS node_timers (
  changeset_id TEXT NOT NULL,
  node         TEXT NOT NULL,
  pre_content  TEXT NOT NULL,          -- byte-exact /etc/network/interfaces to restore
  deadline     INTEGER NOT NULL,       -- unix; when an unresolved timer self-fires
  status       TEXT NOT NULL,          -- armed|cancelled|rolled_back|rollback_failed
  armed_at     INTEGER NOT NULL,
  resolved_at  INTEGER,                -- unix; set when status leaves armed
  error        TEXT,                   -- populated only for rollback_failed
  PRIMARY KEY (changeset_id, node)
);

CREATE INDEX IF NOT EXISTS idx_node_timers_status ON node_timers (status);
