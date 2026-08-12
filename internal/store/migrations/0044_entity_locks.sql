-- 0044_entity_locks.sql — T-2805 "Multi-user presence and changeset locking".
--
-- One table, holding the ADVISORY lock a staged draft takes on the entities
-- it touches. Two operators editing the same bridge at the same time is
-- currently invisible to both of them: the change engine serialises the
-- apply, so the outcome is safe but arbitrary, and the loser is never told.
-- A row here is what makes that collision visible at STAGING time.
--
-- What this table is emphatically NOT:
--
--   * It is NOT a gate on apply. Nothing in the apply path reads it — the
--     change engine has no reference to the package that owns this table
--     (internal/presence), asserted structurally rather than by convention
--     (internal/presence's TestChangeEngineDoesNotImportPresence). T-2805's
--     own words: "a lock never prevents an emergency change; it prevents an
--     accidental one." The single cluster-wide apply interlock
--     docs/architecture.md §4 describes is a different mechanism entirely
--     and is untouched here.
--
--   * It is NOT a copy of PVE state. A `ref` names a PVE entity, but the row
--     says nothing about that entity's configuration — only that a vnprox
--     operator currently has a draft open against it. That is app-owned data
--     in exactly the sense docs/architecture.md §7 permits, the same
--     category `layouts` and `annotations` already occupy.
--
--   * It is NOT presence. Who is currently LOOKING at a changeset or entity
--     is derived from live WebSocket connections and is deliberately never
--     persisted: a presence record that outlives its connection is a lie,
--     and a restart must not resurrect one. Only the lock — which has a
--     holder, an expiry, and an override that must be auditable — is
--     durable.
--
-- Why persist the lock at all, when the session holding it lives in
-- `sessions`? Because a lock outlives a daemon restart the same way that
-- session does (`sessions` is a table, not a process map), and because
-- dropping every lock on restart would silently reopen exactly the collision
-- window this table exists to close. Expiry (below) bounds the worst case
-- either way.
--
--   entity_locks
--     ref          the locked entity's Ref string ("bridge:pve1:vmbr0"), and
--                  the PRIMARY KEY: one holder per entity is the whole rule,
--                  expressed as a constraint rather than as application logic
--                  that could drift from it.
--     changeset_id the draft the lock was taken for. Discarding that draft
--                  releases the lock. Deliberately not a foreign key with ON
--                  DELETE: changesets are never deleted, they transition to
--                  `discarded`, so the release is an explicit DELETE on that
--                  path rather than a cascade that would never fire.
--     holder       the username the lock is attributed to. This is the
--                  identity a colliding operator is shown, and the identity
--                  an override is audited against.
--     session_id   the `sessions.id` (or bearer token id) that took it.
--                  Deliberately NOT a foreign key: a bearer-token principal
--                  has no `sessions` row, and a lock whose session row was
--                  already reaped must still be releasable/expirable rather
--                  than failing an INSERT. '' means "not bound to a live
--                  connection", which simply means only expiry can free it.
--     acquired_at  unix seconds.
--     expires_at   unix seconds, computed at acquire time from the configured
--                  TTL. Expiry is enforced at READ time (a lock whose
--                  expires_at has passed is neither returned nor able to
--                  block an acquire), so a stopped daemon can never leave a
--                  lock standing — the same read-time-expiry discipline
--                  T-2806's annotations use. The sweep that deletes expired
--                  rows keeps the table bounded; it is never the correctness
--                  argument.
--
-- Migrations are forward-only: once released, never edit this file.

CREATE TABLE IF NOT EXISTS entity_locks (
    ref          TEXT PRIMARY KEY,
    changeset_id TEXT NOT NULL,
    holder       TEXT NOT NULL,
    session_id   TEXT NOT NULL DEFAULT '',
    acquired_at  INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);

-- The two release paths that are not "by ref": "release everything this
-- session held" (a dropped WebSocket connection) and "release everything this
-- draft held" (a discarded changeset).
CREATE INDEX IF NOT EXISTS idx_entity_locks_session ON entity_locks (session_id);
CREATE INDEX IF NOT EXISTS idx_entity_locks_changeset ON entity_locks (changeset_id);
