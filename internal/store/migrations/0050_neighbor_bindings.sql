-- 0050_neighbor_bindings.sql — T-3905: IP<->MAC binding history, the
-- storage half of "the ARP table now" -> "what changed and when".
--
-- App-owned data per CLAUDE.md's storage rule: this is vnprox's own
-- observation of the local node's resolved neighbor table over time, never
-- a shadow copy of any PVE-authoritative config. NOT a long-term warehouse
-- (docs/features.md) — internal/neighbor's HistoryRecorder enforces a
-- retention-window prune AND a hard row cap, whichever is smaller prunes
-- first, the same dual-bound convention internal/flow's flow_samples ring
-- already establishes (see 0007_flows.sql).
--
-- Append-ON-CHANGE, not append-on-every-poll: a row is written only when a
-- (node, ip)'s resolved MAC first appears or differs from the
-- previously-recorded MAC for that (node, ip) — see
-- internal/neighbor/history.go's doc comment. This keeps the ring small
-- under normal, stable-network conditions (most poll ticks write nothing)
-- while making a flapping binding's row count itself the flap signal: a
-- burst of rows for one (node, ip) or one (node, mac) in a short window
-- *is* the transition history, not a derived count over a per-poll
-- snapshot ring.
--
-- node is always the *local* node this vnproxd instance is running on
-- (docs/architecture.md §7 / docs/api.md's GET /flows precedent:
-- node-local app data, never a peer-fanned-out write) — every row in one
-- node's store was observed by that node's own host.Reader.Neighbors
-- read, never a peer's. A cluster-wide view is assembled at READ time by
-- fanning the new GET /api/peer/host/neighbors/history route out to every
-- reachable peer and merging pages, exactly like GET /flows.
--
-- id is a plain autoincrement surrogate key (like flow_samples', unlike
-- metric_samples' natural (ref, at) key): id, not at, is the tiebreaker
-- MAX(id)-per-(node,ip) queries use to find "the current binding" even
-- when two transitions land in the same wall-clock second.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS neighbor_bindings (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  at       INTEGER NOT NULL,              -- unix seconds this transition was observed
  node     TEXT NOT NULL,                 -- always the local node (see header)
  ip       TEXT NOT NULL,
  mac      TEXT NOT NULL,                 -- the newly-observed MAC
  prev_mac TEXT,                          -- NULL for this (node, ip)'s first-ever row; otherwise the MAC it replaced
  iface    TEXT NOT NULL DEFAULT '',
  state    TEXT NOT NULL DEFAULT ''       -- host.Neighbor.State at observation time (REACHABLE|STALE|PERMANENT)
);

-- The prune-by-age tick and GET /neighbors/history's ?fromTs=/?toTs= filters
-- both scan by at first.
CREATE INDEX IF NOT EXISTS idx_neighbor_bindings_at ON neighbor_bindings (at);
-- "current binding" / per-IP timeline / IP-flap-window lookups.
CREATE INDEX IF NOT EXISTS idx_neighbor_bindings_node_ip ON neighbor_bindings (node, ip, id);
-- "one MAC claiming many IPs" flap-window lookups.
CREATE INDEX IF NOT EXISTS idx_neighbor_bindings_node_mac ON neighbor_bindings (node, mac, id);
