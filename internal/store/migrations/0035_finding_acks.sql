-- 0035_finding_acks.sql — T-2402 "Finding acknowledgement and mute".
--
-- vnprox runs 43 health checks across 15 sources and, until this migration,
-- offered no way to say "I know, that one is deliberate". A finding that is
-- understood and intentional — a deliberately asymmetric MTU, a bridge with
-- no guests on a staging node — reappeared on every recompute cycle forever,
-- and the only way to stop looking at it was to stop looking at the stream.
-- A stream nobody can triage becomes wallpaper, and wallpaper is
-- indistinguishable from a broken check.
--
-- App-owned data per CLAUDE.md's storage rule: an acknowledgement is vnprox's
-- own triage bookkeeping. It holds no PVE-authoritative config, and it never
-- changes what a check computes — only how the stream presents the result.
--
-- ACKNOWLEDGEMENT IS NOT SUPPRESSION (docs/roadmap-leverage.md's invariant).
-- An acked finding is still produced, still returned by GET /findings with
-- its ack attached, and still counted — in its own bucket. Nothing here can
-- make a finding invisible. If a check is wrong, the check gets fixed.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version.

-- One row per acknowledged finding, keyed on the finding's STABLE id —
-- internal/findings' ids are derived from the check name plus the refs (or
-- nodes) it fired against, and internal/findings/hysteresis.go already
-- guarantees that identity survives across recompute cycles. Keying on
-- anything else (a row id, a cycle sequence number) would lose the ack on
-- the very next poll, which is the whole point of the feature.
--
-- Deliberate consequence, tested in internal/findings/ack_test.go: a finding
-- whose condition CLEARS and later RETURNS with the same id is still acked.
-- That is the intent, not an oversight — a flapping condition is exactly the
-- case an operator is muting, and an ack that evaporated on the first clear
-- would be defeated by the findings it is most needed for. Expiry, not
-- flapping, is what ends an ack.
CREATE TABLE IF NOT EXISTS finding_acks (
  finding_id TEXT PRIMARY KEY,
  -- Required and non-empty, enforced in internal/findings before it ever
  -- reaches here: an acknowledgement with no reason is an unexplained
  -- silence, which is worse than the noise it removes.
  reason     TEXT NOT NULL,
  acked_by   TEXT NOT NULL,
  acked_at   INTEGER NOT NULL,
  -- Unix seconds, or 0 for "until explicitly un-acked". Expiry is evaluated
  -- at READ time, never by a sweeper: a daemon that is stopped, crashed, or
  -- simply not running its cleanup tick must not be able to leave a finding
  -- muted past the date its operator chose. There is deliberately no
  -- background job that deletes expired rows.
  expires_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_finding_acks_expires ON finding_acks (expires_at);
