-- 0031_ha.sql — T-1704 "vnproxd HA": the leader-lease/fencing record that is
-- the sole source of truth for which daemon in an active/standby pair may
-- drive apply/confirm/rollback at any instant (docs/architecture.md HA
-- topology section; docs/data-model.md §2 ha_lease).
--
-- Migration numbering note: migrations through 0027 have shipped; 0028-0030
-- are reserved for sibling Phase-17 tasks cut from the same base, so this
-- card is assigned 0031 to avoid a migration-number collision at merge time
-- (per T-1704's orchestration constraints). A gap in this branch's own
-- migration sequence is harmless — loadMigrations()/migrate() key off each
-- file's own version prefix, never contiguity (see 0021_clusters.sql's
-- identical note) — and disappears once the sibling migrations land.
--
-- `ha_lease` is a SINGLETON row (id is a fixed constant, haLeaseSingletonID
-- in haleases.go): a daemon holds at most one leader lease at a time. Each
-- daemon persists its OWN best-known view of the lease in its OWN app store,
-- replicated between the pair over internal/peer's TLS+HMAC channel exactly
-- like changesets/schedules/api_tokens/audit are (internal/ha). It is app-
-- owned HA coordination state, NOT PVE config — CLAUDE.md's storage rule /
-- docs/architecture §7's new-domain invariant.
--
-- Fencing model (see internal/ha/lease.go's doc comment for the full
-- rationale): `term` is a monotonically-increasing fencing token — a standby
-- only ever promotes by writing a strictly-higher term, and any action or
-- heartbeat carrying an older term than the one a daemon has already observed
-- is rejected/no-oped. `holder` names the instance that owns the lease for
-- `term`; `expires_at` is an ABSOLUTE unix-seconds deadline (never a relative
-- duration, so it survives replication and restart verbatim, mirroring
-- changesets.confirm_deadline and changeset_schedules.window_* — T-304/
-- T-1103) past which, plus a fencing margin, a standby may promote.
--
-- Migrations are forward-only: once released, never edit this file; a schema
-- change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS ha_lease (
  id          TEXT PRIMARY KEY,   -- singleton key (haLeaseSingletonID)
  holder      TEXT NOT NULL,      -- instance id of the daemon that owns this term's lease
  term        INTEGER NOT NULL,   -- monotonic fencing token; a promotion strictly increases it
  expires_at  INTEGER NOT NULL,   -- absolute unix seconds; standby may promote past this + fencing margin
  acquired_at INTEGER NOT NULL,   -- absolute unix seconds the current holder first acquired this term
  updated_at  INTEGER NOT NULL    -- absolute unix seconds of the last renew/observe write
);
