-- 0007_flows.sql — T-1002 flow ingestion engine: a bounded ring of
-- normalized flow.Record samples ingested from sFlow/NetFlow/IPFIX
-- listeners (and, later, T-1004's host-local conntrack/eBPF samplers).
--
-- App-owned data per CLAUDE.md's storage rule: flow_samples is vnprox's own
-- observation of live traffic, never a shadow copy of any PVE-authoritative
-- config. NOT a long-term warehouse — see internal/flow's package doc
-- comment for the enforced bound (a retention-window prune AND a hard row
-- cap, whichever is smaller prunes first, on the same tick cadence
-- internal/metrics' metric_samples ring already establishes).
--
-- id is a plain autoincrement surrogate key (unlike metric_samples' natural
-- (ref, at) primary key): many distinct flow observations can legitimately
-- share the exact same (node, src, dst, port, at) tuple at one-second
-- resolution (a real conversation is many packets/exports, not one sample
-- per second like a counter poll), so there is no natural dedup key here —
-- every decoded Record is its own row.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS flow_samples (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  at         INTEGER NOT NULL,              -- unix seconds
  node       TEXT NOT NULL,
  src_ip     TEXT NOT NULL,
  dst_ip     TEXT NOT NULL,
  src_port   INTEGER NOT NULL DEFAULT 0,
  dst_port   INTEGER NOT NULL DEFAULT 0,
  proto      INTEGER NOT NULL DEFAULT 0,     -- IP protocol number
  bytes      INTEGER NOT NULL DEFAULT 0,
  packets    INTEGER NOT NULL DEFAULT 0,
  vlan       INTEGER NOT NULL DEFAULT 0,     -- 0 = unset
  src_ref    TEXT NOT NULL DEFAULT '',       -- inventory Ref string, '' if unresolved
  dst_ref    TEXT NOT NULL DEFAULT '',
  ingress_if INTEGER NOT NULL DEFAULT 0,     -- 0 = unset
  egress_if  INTEGER NOT NULL DEFAULT 0,
  source     TEXT NOT NULL                  -- "sflow"|"netflow5"|"netflow9"|"ipfix"|"conntrack"
);

-- The prune-by-age tick and GET /flows' ?fromTs=/?toTs= filters both scan
-- by at first.
CREATE INDEX IF NOT EXISTS idx_flow_samples_at ON flow_samples (at);
CREATE INDEX IF NOT EXISTS idx_flow_samples_node ON flow_samples (node);
CREATE INDEX IF NOT EXISTS idx_flow_samples_src_ref ON flow_samples (src_ref);
CREATE INDEX IF NOT EXISTS idx_flow_samples_dst_ref ON flow_samples (dst_ref);
