-- 0013_latency_samples.sql — T-1303 latency & loss mesh: a bounded ring of
-- node-to-node probe readings (internal/latmesh), one row per (link, tick).
--
-- App-owned data per CLAUDE.md's storage rule: this is vnprox's own
-- continuous probe observation, never a shadow copy of any PVE-authoritative
-- config. NOT a long-term warehouse — bounded by both a retention window
-- ([latmesh] retention_minutes) AND a hard row cap ([latmesh] max_rows),
-- whichever is smaller prunes first, the same tick-based prune-loop pattern
-- flow_samples (0007_flows.sql) already establishes.
--
-- id is a plain autoincrement surrogate key (like flow_samples, unlike
-- metric_samples' natural (ref, at) key): a link legitimately produces one
-- new reading every probe tick, and (link_id, at) could in principle collide
-- across two ticks landing on the same wall-clock second under a very short
-- configured interval, so id (not (link_id, at)) is the primary key.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS latency_samples (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  link_id    TEXT NOT NULL,             -- internal/latmesh.Pair.LinkID, e.g. "corosync:ring0|pve1->pve2"
  fabric     TEXT NOT NULL,             -- "corosync"|"guest"
  from_node  TEXT NOT NULL,
  to_node    TEXT NOT NULL,
  at         INTEGER NOT NULL,          -- unix seconds
  rtt_ms     REAL NOT NULL DEFAULT 0,   -- meaningless when loss_pct = 100
  loss_pct   REAL NOT NULL DEFAULT 0    -- this tick's own loss%, 0-100
);

-- The prune-by-age tick, GET /latmesh/history's ?fromTs=/?toTs= filters, and
-- the rolling-window heatmap/baseline reads all scan by (link_id, at) first.
CREATE INDEX IF NOT EXISTS idx_latency_samples_link_at ON latency_samples (link_id, at);
CREATE INDEX IF NOT EXISTS idx_latency_samples_at ON latency_samples (at);
