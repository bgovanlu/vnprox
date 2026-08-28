-- 0053_tc_mirror_sessions.sql — T-4014's SPAN/mirror session bookkeeping:
-- app-owned intent + accounting for a tc.mirror.create/update/delete
-- changeset op's live tc/clsact/mirred state on one node, plus the durable
-- expires_at deadline that lets the session bound and stop itself.
--
-- Per CLAUDE.md's storage rule / docs/architecture.md §7's new-domain
-- invariant, vnprox persists intent + accounting only here — the live tc
-- state on the node itself stays authoritative and is never shadow-copied
-- (there is no polled "observed session" reconciliation; the on-node tc
-- invocation is re-derived from this row's own fields by
-- internal/tcmirror.RenderTC/RenderTCTeardown every time it is applied or
-- torn down), mirroring 0020_qos.sql's identical qos_shapes design.
--
-- Every mutation to this table happens exclusively through the ordinary
-- tc.mirror.create/update/delete changeset op lifecycle
-- (internal/change's apply/rollback executor, cmd/vnproxd's
-- hostTcMirrorGateway) OR the daemon's own unattended expiry sweep
-- (internal/change/tcmirror_expiry.go, which applies+confirms an ordinary
-- tc.mirror.delete changeset — see that file's doc comment for why this is
-- not a second mutation path) — there is no third way to touch it.
--
-- expires_at is the durable, daemon-restart-safe deadline
-- RunTcMirrorSweep checks: computed once at apply time
-- (started_at + max_duration_sec) and never recomputed from a live clock,
-- so a session's bound survives a daemon restart exactly like
-- capture_sessions' retention window does (internal/capture's Sweep doc
-- comment) — an orphaned session (status still 'active' because the
-- daemon was down when its deadline passed) is caught and torn down the
-- moment the daemon's sweep next primes, not silently left running.
--
-- Migrations are forward-only: once released, never edit this file; a
-- schema change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS tc_mirror_sessions (
  id               TEXT PRIMARY KEY,        -- caller-chosen (tc.mirror.create target id)
  node             TEXT NOT NULL,           -- owning PVE node
  source_iface     TEXT NOT NULL,
  dest_iface       TEXT NOT NULL,
  max_mbit         INTEGER,                  -- NULL = no declared bandwidth ceiling
  max_duration_sec INTEGER NOT NULL,
  status           TEXT NOT NULL,            -- 'active' | 'expired' | 'stopped'
  created_by       TEXT NOT NULL,
  started_at       INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL,         -- started_at + max_duration_sec
  stopped_at       INTEGER
);

CREATE INDEX IF NOT EXISTS idx_tc_mirror_sessions_node ON tc_mirror_sessions (node, status);
CREATE INDEX IF NOT EXISTS idx_tc_mirror_sessions_expiry ON tc_mirror_sessions (status, expires_at);
