-- T-1301: distributed packet-capture engine.
--
-- capture_sessions is app-owned intent + accounting only — NOT a shadow copy
-- of anything Proxmox owns, and NEVER a place payload bytes land. The captured
-- packets live solely in the bounded per-session .pcap file named by
-- file_path on the capturing node (auto-purged past retention_hours by
-- internal/capture.Coordinator.Sweep); this table records who started what,
-- where, under which server-enforced caps, and running byte/packet counts.
--
-- One row per capturing node. A multi-point capture (the same logical flow
-- captured on ≥2 nodes) is a set of rows sharing group_id — the correlation
-- key T-1302 matches the two nodes' decoded streams up by. nodes_json carries
-- the full participating-node list on every row so any one row knows its
-- siblings without a second query.
CREATE TABLE capture_sessions (
  id          TEXT PRIMARY KEY,               -- ULID, one per node-local session
  group_id    TEXT NOT NULL,                  -- correlation key for a multi-point capture
  target_ref  TEXT NOT NULL,                  -- inventory Ref of the captured target
  node        TEXT NOT NULL,                  -- capturing node (peer-aware)
  nodes_json  TEXT NOT NULL DEFAULT '[]',     -- full node set of this session's group
  filter      TEXT NOT NULL DEFAULT '',       -- validated BPF filter (never payload)
  caps_json   TEXT NOT NULL,                  -- effective, server-clamped caps
  status      TEXT NOT NULL,                  -- running|completed|stopped|error|purged
  started_by  TEXT NOT NULL,                  -- actor (audit attribution)
  started_at  INTEGER NOT NULL,
  stopped_at  INTEGER NOT NULL DEFAULT 0,
  file_path   TEXT NOT NULL DEFAULT '',       -- on-disk .pcap path on `node`
  file_bytes  INTEGER NOT NULL DEFAULT 0,     -- accounting only
  packets     INTEGER NOT NULL DEFAULT 0      -- accounting only
);

CREATE INDEX idx_capture_sessions_group ON capture_sessions (group_id);
CREATE INDEX idx_capture_sessions_started ON capture_sessions (started_at DESC);
