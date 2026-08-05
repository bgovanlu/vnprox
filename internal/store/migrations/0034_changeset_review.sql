-- 0034_changeset_review.sql — T-2003 "Change review: approvals, comments,
-- side-by-side diff". Generalizes T-1703's tenant request-changeset approval
-- queue into a review mechanism every changeset can use, plus per-op/
-- changeset comments — the changeset is this product's unit of work, and its
-- review surface (a single admin's "review & apply") is where a team, as
-- opposed to a single admin, actually lives.
--
-- App-owned data per CLAUDE.md's storage rule: both tables hold vnprox's own
-- review-workflow bookkeeping, never a shadow copy of any PVE-authoritative
-- config.
--
-- SECURITY: changeset_approvals is an authorization surface, not a UI
-- convenience — internal/change.Service.Apply (beginApply) reads it
-- server-side to decide whether an apply may proceed when this deployment's
-- [changesets] approval_required policy is on; nothing about it is ever
-- inferred from a client-supplied value (docs/security.md).
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version.

-- One row per review comment, attached either to a single op (op_id matches
-- that op's own stable Op.ID — internal/change/op.go) or to the changeset as
-- a whole (op_id ''). Comments are never edited in place (no updated_at) —
-- only added or deleted — mirroring the audit log's own append-only
-- convention for a review trail.
CREATE TABLE IF NOT EXISTS changeset_comments (
  id           TEXT PRIMARY KEY,          -- ULID
  changeset_id TEXT NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
  op_id        TEXT NOT NULL DEFAULT '',  -- '' = changeset-level comment
  author       TEXT NOT NULL,
  body         TEXT NOT NULL,
  created_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_changeset_comments_changeset ON changeset_comments (changeset_id);

-- One row per changeset: its current review-approval decision, or absent
-- entirely (the implicit "none" state every changeset starts in and the only
-- state a pre-T-2003 changeset can ever be in). A changeset may be rejected
-- and later approved (or vice versa) as the author edits and re-requests
-- review; only the LATEST decision is kept here — this is a live apply gate,
-- not an approval history (the audit log, which gets a row on every
-- comment/approve/reject, is where that history lives). Editing a draft's
-- ops (PUT /changesets/{id}) clears this row (change.Service.UpdateDraft):
-- an approval decision was made against a specific set of ops and must never
-- silently carry over to a materially different one.
CREATE TABLE IF NOT EXISTS changeset_approvals (
  changeset_id TEXT PRIMARY KEY REFERENCES changesets(id) ON DELETE CASCADE,
  status       TEXT NOT NULL,             -- approved|rejected
  decided_by   TEXT NOT NULL,
  reason       TEXT NOT NULL DEFAULT '',  -- rejection reason; '' for an approval
  decided_at   INTEGER NOT NULL
);
