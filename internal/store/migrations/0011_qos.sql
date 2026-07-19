-- 0011_qos.sql — T-1505 "QoS & traffic shaping": app-owned intent for a
-- bridge-level tc/HTB shape. Per CLAUDE.md's storage rule / docs/
-- architecture.md §7's new-domain invariant, vnprox persists intent +
-- audit only here — the live tc/HTB state on the node itself stays
-- authoritative and is never shadow-copied into this table (there is no
-- polled "observed shape" reconciliation the way, say, inventory polls PVE
-- config; the on-node tc invocation is re-derived from this row's own
-- fields by internal/qos.RenderTC every time it is (re)applied).
--
-- Every mutation to this table happens exclusively through the ordinary
-- qos.shape.create/update/delete changeset op lifecycle
-- (internal/change's apply/rollback executor, cmd/vnproxd's hostQosGateway)
-- — there is no second mutation path (CLAUDE.md's change-engine invariant).
--
-- Migrations are forward-only: once released, never edit this file; a
-- schema change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS qos_shapes (
  id          TEXT PRIMARY KEY,        -- caller-chosen (docs/data-model.md §3's qos.shape.create target id)
  node        TEXT NOT NULL,           -- owning PVE node
  bridge      TEXT NOT NULL,           -- the bridge interface this shape governs
  match_cidr  TEXT NOT NULL DEFAULT '', -- optional traffic-selector CIDR; '' = whole-bridge shape
  match_vlan  INTEGER,                  -- optional traffic-selector 802.1Q VID; NULL = no VLAN match
  rate_mbit   INTEGER NOT NULL,
  ceil_mbit   INTEGER,                  -- NULL = no explicit ceiling (HTB borrows up to the parent's rate)
  priority    INTEGER,                  -- NULL = tc's own default HTB priority
  created_by  TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_qos_shapes_node_bridge ON qos_shapes (node, bridge);
