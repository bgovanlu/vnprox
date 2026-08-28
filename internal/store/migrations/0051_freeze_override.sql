-- 0051_freeze_override.sql — T-4006's audited escape hatch for a declared
-- freeze-window policy rule.
--
-- A freeze window (internal/change/policy.go's PolicyRule, tagged
-- "freeze" — PolicyTagFreeze) is enforced as an ordinary VALIDATE-time
-- policy finding, unlike T-2604's two-person rule (an AUTHORIZATION gate
-- checked only in beginApply). This table follows that card's OWN shape —
-- reasoned, recorded, its own audit action — rather than reusing
-- changeset_breakglass (0040) for a conceptually different override: a
-- freeze bypass is not a two-person-rule bypass, and folding them into one
-- table/audit-action would make an auditor's "which ceremony happened
-- here" question ambiguous.
--
-- One row per changeset that has had a freeze-window override invoked on
-- it: the deliberate, reasoned decision that a specific changeset may
-- proceed through a declared freeze anyway (a genuine incident during a
-- change freeze, most commonly). Never deleted by an edit, an apply, a
-- rollback, or a discard of the override — it is the evidence trail, read
-- back by every path that evaluates policy for this changeset
-- (internal/change/policy_service.go's validationInputs/policyDenial), so
-- Diff's early refusal and the real validate/apply revalidation can never
-- disagree about whether an override applies.
--
-- ops_fingerprint pins the override to the exact ops it was invoked for,
-- identically to changeset_breakglass's own column: an override taken for
-- one draft must not silently authorize whatever it is edited into
-- afterwards.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version.
CREATE TABLE IF NOT EXISTS changeset_freeze_override (
  changeset_id     TEXT PRIMARY KEY REFERENCES changesets(id) ON DELETE CASCADE,
  reason           TEXT NOT NULL,         -- required, non-empty; enforced above this layer too
  invoked_by       TEXT NOT NULL,
  invoked_at       INTEGER NOT NULL,      -- unix seconds
  ops_fingerprint  TEXT NOT NULL DEFAULT ''
);
