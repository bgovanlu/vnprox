-- 0018_wan.sql — T-1405 WAN & upstream health: configured reference targets
-- (wan_targets) plus a bounded ring of per-(node,uplink,target) probe
-- readings (wan_probe_samples), the exact same shape latency_samples
-- (0013_latency_samples.sql) already established for T-1303's node-to-node
-- mesh — this task reuses internal/latmesh's scheduler/Ring machinery
-- against its own table/config rather than mixing WAN readings into the
-- LAN mesh's own ring (a WAN link's natural jitter/loss profile and
-- retention needs are not the same as a corosync/guest-fabric link's, so
-- this task's own [wan] retention_minutes/max_rows tunables need their own
-- table to bound).
--
-- App-owned data per CLAUDE.md's storage rule: an operator-configured
-- reference-target list and vnprox's own continuous WAN probe observation,
-- never a shadow copy of anything PVE or an upstream ISP device owns.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS wan_targets (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  node       TEXT NOT NULL,             -- the node this uplink belongs to (node-local scope, see internal/wan's package doc comment)
  uplink     TEXT NOT NULL,             -- an operator-chosen label naming the uplink (e.g. the default-route-carrying iface name from GET /edge/routes)
  host       TEXT NOT NULL,             -- reference target: IP or hostname this uplink is probed against
  created_at INTEGER NOT NULL,
  UNIQUE (node, uplink, host)
);

-- wan_probe_samples mirrors latency_samples field-for-field except
-- to_node carries a configured reference target's host (an external IP or
-- hostname, not a cluster node name) and an additional uplink column
-- records which of a node's (possibly several, multi-WAN) uplinks the
-- reading belongs to.
CREATE TABLE IF NOT EXISTS wan_probe_samples (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  link_id    TEXT NOT NULL,             -- internal/latmesh.Pair.LinkID, e.g. "wan:vmbr0|pve1->1.1.1.1"
  from_node  TEXT NOT NULL,
  uplink     TEXT NOT NULL,
  to_node    TEXT NOT NULL,             -- the probed reference target's host
  at         INTEGER NOT NULL,          -- unix seconds
  rtt_ms     REAL NOT NULL DEFAULT 0,   -- meaningless when loss_pct = 100
  loss_pct   REAL NOT NULL DEFAULT 0    -- this tick's own loss%, 0-100
);  -- bounded: pruned to [wan] retention_minutes (default 60) AND a hard
    -- row cap ([wan] max_rows, default 500,000), whichever is smaller
    -- prunes first — the same tick-based prune-loop pattern
    -- latency_samples/flow_samples already establish. NOT a long-term
    -- warehouse.

CREATE INDEX IF NOT EXISTS idx_wan_probe_samples_link_at ON wan_probe_samples (link_id, at);
CREATE INDEX IF NOT EXISTS idx_wan_probe_samples_at ON wan_probe_samples (at);
CREATE INDEX IF NOT EXISTS idx_wan_targets_node ON wan_targets (node);
