-- 0052_maintenance_windows.sql — T-4007's declared node maintenance
-- windows: the {start, end} pairs internal/findings suppresses a node's
-- findings/alerts against.
--
-- This is deliberately its OWN table, not a reuse of policy_sets'
-- PolicyRule rows (the mechanism T-4006's freeze windows use). A freeze
-- window IS an ordinary PolicyRule because it must be evaluated by the
-- SAME EvaluatePolicy path every other deny/warn rule runs through — "no
-- second enforcement point" is that card's own stated constraint. A
-- maintenance window is not an enforcement rule at all: it says nothing
-- about whether a changeset may apply, and folding it into PolicySet would
-- either (a) silently start blocking applies during "maintenance" too,
-- which this card never asked for, or (b) require every PolicyRule
-- consumer (EvaluatePolicy, the calendar's freeze-only renderer, the
-- freeze-override ceremony) to grow a special case that ignores
-- maintenance-tagged rows. What IS reused from T-4006, faithfully: the
-- time representation (an absolute unix instant range, resolved once at
-- declare time) and the mandatory-IANA-zone discipline
-- (internal/change/maintenance.go's DeclareMaintenanceWindow refuses an
-- empty zone exactly as PolicySet.Validate refuses a wall-clock fact with
-- none) — and the calendar VIEW: GET /calendar renders this table's rows
-- alongside freeze windows and pending schedules on the same timeline,
-- via change.Service.Calendar.
--
-- Cluster-aware by construction: node is any node name vnprox's inventory
-- knows about, local or peer — there is nothing here that assumes
-- localhost.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version.
CREATE TABLE IF NOT EXISTS maintenance_windows (
  id          TEXT PRIMARY KEY,   -- ULID (store.NewULID)
  node        TEXT NOT NULL,      -- the single node this window suppresses findings for
  reason      TEXT NOT NULL DEFAULT '',
  created_by  TEXT NOT NULL,
  -- zone (mandatory, T-4006's line held): the IANA name the operator
  -- declared start/end in. Stored for display/audit fidelity even though
  -- start/end below are already resolved absolute instants — an operator
  -- reading "declared in America/New_York" a year later should not have to
  -- reverse-engineer the offset from the epoch alone.
  zone        TEXT NOT NULL,
  start_epoch INTEGER NOT NULL,   -- unix seconds, resolved from the declared local wall clock + zone
  end_epoch   INTEGER NOT NULL,   -- unix seconds; must be > start_epoch (enforced above this layer)
  created_at  INTEGER NOT NULL
);

-- Suppression evaluation (internal/findings) runs once per findings cycle
-- and asks "which windows are active for node N right now" — indexed on
-- node, since that is every lookup's first filter; end_epoch is included so
-- a future "expire and archive" sweep (not implemented — expiry here is
-- evaluated at READ time, the same finding_acks/0035 discipline, never by a
-- sweeper) could scan cheaply without a table scan.
CREATE INDEX IF NOT EXISTS idx_maintenance_windows_node ON maintenance_windows (node, end_epoch);
