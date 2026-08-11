-- 0040_two_person_rule.sql — T-2604's enforced two-person rule on protected
-- op classes.
--
-- T-2003's changeset_approvals (0034) records ONE decision per changeset:
-- approved or rejected, by whoever decided last. That is enough to answer
-- "has anyone approved this?" and nothing else — in particular it cannot
-- answer "have TWO DISTINCT PEOPLE approved this?", which is the whole
-- question a two-person rule asks. changeset_signoffs below is that second
-- question's storage, and it is deliberately a separate table rather than
-- a widening of changeset_approvals: the existing gate's semantics
-- ("current decision, latest wins") and this one's ("the set of people who
-- have endorsed these ops") are different enough that folding them into one
-- row would make both harder to reason about.
--
-- SECURITY: both tables are AUTHORIZATION surfaces, not UI conveniences.
-- internal/change.Service.beginApply reads them server-side to decide
-- whether an apply in a protected class may proceed; nothing about either is
-- ever inferred from a client-supplied value (docs/security.md), and the
-- frontend not rendering an Apply button is never the enforcement.
--
-- App-owned data per CLAUDE.md's storage rule: neither table holds a shadow
-- copy of any PVE-authoritative config. PVE has no notion of an approval.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version.

-- One row per (changeset, PRINCIPAL) — never per approval request, per
-- session, or per token. The PRIMARY KEY is what makes "approvers must be
-- distinct principals" a storage-level property rather than a counting
-- convention someone can later get wrong: the same person approving twice,
-- through two different API tokens or two different browser sessions,
-- collapses onto one row (their username is the same either way — a bearer
-- token's identity is its creating user, internal/auth/middleware.go), so
-- the count of rows IS the count of distinct people.
--
-- Cleared wholesale whenever a draft's ops are replaced (the same
-- "editing invalidates the decision" rule changeset_approvals already
-- follows), and a single row is removed when its principal later rejects
-- the changeset: an endorsement withdrawn is not an endorsement.
CREATE TABLE IF NOT EXISTS changeset_signoffs (
  changeset_id TEXT NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
  principal    TEXT NOT NULL,             -- the approving identity (session username)
  decided_at   INTEGER NOT NULL,          -- unix seconds; most recent approval by this principal
  PRIMARY KEY (changeset_id, principal)
);

-- One row per changeset that has had emergency break-glass invoked on it:
-- the deliberate, reasoned override of the two-person requirement when
-- there is nobody else awake to be the second person.
--
-- The row is NEVER deleted by an edit, an apply, a rollback, or a discard of
-- the override: it is the evidence trail. The break-glass finding
-- (findings' change_break_glass check) is computed from these rows, and that
-- finding cannot be acknowledged for 24 hours after invoked_at — so deleting
-- the row on any ordinary lifecycle event would be an obvious way to make
-- the whole ceremony disappear. Only the changeset's own deletion cascades
-- it away.
--
-- ops_fingerprint pins the override to the exact ops it was invoked for. A
-- break-glass taken for "restart the corosync bridge" must not silently
-- authorize whatever the draft is edited into afterwards, so apply refuses a
-- fingerprint mismatch and the operator has to invoke break-glass again —
-- which raises a second finding, which is the correct outcome.
CREATE TABLE IF NOT EXISTS changeset_breakglass (
  changeset_id     TEXT PRIMARY KEY REFERENCES changesets(id) ON DELETE CASCADE,
  reason           TEXT NOT NULL,         -- required, non-empty; enforced above this layer too
  invoked_by       TEXT NOT NULL,
  invoked_at       INTEGER NOT NULL,      -- unix seconds; the 24h ack floor counts from here
  ops_fingerprint  TEXT NOT NULL DEFAULT ''
);
