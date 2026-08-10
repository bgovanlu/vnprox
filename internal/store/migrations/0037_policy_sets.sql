-- 0037_policy_sets.sql — T-2601's policy-as-code guardrails.
--
-- policy_sets holds one cluster's declarative policy rule set: the rules an
-- organisation wants the change engine to refuse (or annotate) at the
-- validate stage. It is app-owned intent, not a shadow copy of any PVE
-- config (CLAUDE.md) — PVE has no notion of these rules at all.
--
-- Cluster-scoped: one row per attached cluster, keyed by the same cluster_id
-- convention the rest of the schema uses ('' is the implicit default/local
-- cluster), so a federated deployment can hold a different rule set per
-- cluster and a single-cluster one needs no migration of its data.
--
-- `revision` is a monotonically increasing document revision stamped by the
-- daemon on every successful update — the "versioned in the store" half of
-- the card. It is NOT the document's format version (that lives inside
-- rules_json as `version`, mirroring protected.json's own Version field).
-- Per-revision history is deliberately NOT kept here: every update writes a
-- policy.update audit entry carrying the FULL rule-set diff (added, removed,
-- and both sides of every changed rule), so the audit log alone reconstructs
-- what changed — the same "current state here, history in the audit log"
-- split changeset_approvals (0034) documents.
--
-- policy_rule_stats backs the card's "a policy that matches nothing is an
-- error, not a silent pass": the statically-decidable half is a load error
-- (internal/change/policy.go's PolicySet.Validate), and this table carries
-- the other half — how many evaluations a rule has been through and when it
-- last matched anything, so a rule that has matched nothing for N days is
-- reported as probably-misconfigured instead of silently passing forever.
-- It is pure derived bookkeeping: dropping the table would cost the report,
-- never the enforcement.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version.

CREATE TABLE IF NOT EXISTS policy_sets (
    cluster_id TEXT PRIMARY KEY,          -- '' = implicit local/default cluster
    revision   INTEGER NOT NULL,          -- monotonic document revision, stamped by the daemon
    rules_json TEXT NOT NULL,             -- the whole policy document, canonical JSON
    updated_by TEXT NOT NULL DEFAULT '',  -- principal who installed this revision
    updated_at INTEGER NOT NULL           -- unix seconds
);

CREATE TABLE IF NOT EXISTS policy_rule_stats (
    cluster_id      TEXT NOT NULL,           -- '' = implicit local/default cluster
    rule_id         TEXT NOT NULL,           -- PolicyRule.id
    first_seen_at   INTEGER NOT NULL,        -- unix seconds: first evaluation this rule took part in
    last_matched_at INTEGER NOT NULL DEFAULT 0, -- unix seconds: last evaluation where it matched an op (0 = never)
    eval_count      INTEGER NOT NULL DEFAULT 0, -- evaluations this rule has been through
    match_count     INTEGER NOT NULL DEFAULT 0, -- ops it has matched, cumulative
    PRIMARY KEY (cluster_id, rule_id)
);

CREATE INDEX IF NOT EXISTS idx_policy_rule_stats_cluster ON policy_rule_stats (cluster_id);
