// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PolicySet is the policy_sets table's one row per cluster (T-2601,
// 0037_policy_sets.sql): that cluster's declarative policy-as-code rule set.
// RulesJSON is opaque here — internal/store never imports the packages that
// use it, so the document is encoded/decoded by the caller
// (internal/change), exactly as BaselineProfile.ProfileJSON is.
type PolicySet struct {
	ClusterID string
	RulesJSON string
	UpdatedBy string
	Revision  int64
	UpdatedAt int64
}

// PolicyRuleStat is one rule's cumulative evaluation bookkeeping
// (policy_rule_stats), from which a never-matching rule is reported as
// probably-misconfigured rather than passing silently.
type PolicyRuleStat struct {
	ClusterID     string
	RuleID        string
	FirstSeenAt   int64
	LastMatchedAt int64
	EvalCount     int64
	MatchCount    int64
}

// PolicySetRepo is the policy_sets / policy_rule_stats repository.
type PolicySetRepo struct {
	db *DB
}

// NewPolicySetRepo constructs a PolicySetRepo.
func NewPolicySetRepo(db *DB) *PolicySetRepo {
	return &PolicySetRepo{db: db}
}

// Get returns clusterID's current policy set, or ErrNotFound if that
// cluster has never had one installed.
func (r *PolicySetRepo) Get(ctx context.Context, clusterID string) (PolicySet, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT cluster_id, revision, rules_json, updated_by, updated_at
		FROM policy_sets WHERE cluster_id = ?`, clusterID,
	)
	var s PolicySet
	err := row.Scan(&s.ClusterID, &s.Revision, &s.RulesJSON, &s.UpdatedBy, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicySet{}, ErrNotFound
	}
	if err != nil {
		return PolicySet{}, fmt.Errorf("store: reading policy set for cluster %q: %w", clusterID, err)
	}
	return s, nil
}

// Put upserts s, replacing that cluster's rule set wholesale. The caller
// stamps Revision (internal/change.Service, which reads the current one
// first and increments it) so the revision sequence is the daemon's, not a
// side effect of two concurrent writers racing on a SQL expression.
func (r *PolicySetRepo) Put(ctx context.Context, s PolicySet) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO policy_sets (cluster_id, revision, rules_json, updated_by, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (cluster_id) DO UPDATE SET
			revision   = excluded.revision,
			rules_json = excluded.rules_json,
			updated_by = excluded.updated_by,
			updated_at = excluded.updated_at`,
		s.ClusterID, s.Revision, s.RulesJSON, s.UpdatedBy, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting policy set for cluster %q: %w", s.ClusterID, err)
	}
	return nil
}

// RecordEvaluation folds one policy evaluation into the per-rule stats:
// every rule in ruleIDs took part, and matched[ruleID] ops matched it.
// First sight of a rule stamps first_seen_at, which is what the
// "unmatched for N days" report measures from.
func (r *PolicySetRepo) RecordEvaluation(ctx context.Context, clusterID string, ruleIDs []string, matched map[string]int, at int64) error {
	for _, id := range ruleIDs {
		n := int64(matched[id])
		lastMatched := int64(0)
		if n > 0 {
			lastMatched = at
		}
		_, err := r.db.ExecContext(ctx, `
			INSERT INTO policy_rule_stats (cluster_id, rule_id, first_seen_at, last_matched_at, eval_count, match_count)
			VALUES (?, ?, ?, ?, 1, ?)
			ON CONFLICT (cluster_id, rule_id) DO UPDATE SET
				last_matched_at = MAX(policy_rule_stats.last_matched_at, excluded.last_matched_at),
				eval_count      = policy_rule_stats.eval_count + 1,
				match_count     = policy_rule_stats.match_count + excluded.match_count`,
			clusterID, id, at, lastMatched, n,
		)
		if err != nil {
			return fmt.Errorf("store: recording policy evaluation for rule %q: %w", id, err)
		}
	}
	return nil
}

// Stats returns clusterID's per-rule bookkeeping, ordered by rule id.
func (r *PolicySetRepo) Stats(ctx context.Context, clusterID string) ([]PolicyRuleStat, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT cluster_id, rule_id, first_seen_at, last_matched_at, eval_count, match_count
		FROM policy_rule_stats WHERE cluster_id = ? ORDER BY rule_id`, clusterID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing policy rule stats for cluster %q: %w", clusterID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []PolicyRuleStat
	for rows.Next() {
		var s PolicyRuleStat
		if err := rows.Scan(&s.ClusterID, &s.RuleID, &s.FirstSeenAt, &s.LastMatchedAt, &s.EvalCount, &s.MatchCount); err != nil {
			return nil, fmt.Errorf("store: scanning policy rule stat: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing policy rule stats for cluster %q: %w", clusterID, err)
	}
	return out, nil
}

// ForgetRules drops stats for rules that no longer exist in the installed
// set, so a rule id reused later starts from a clean first_seen_at rather
// than inheriting a retired rule's history.
func (r *PolicySetRepo) ForgetRules(ctx context.Context, clusterID string, ruleIDs []string) error {
	for _, id := range ruleIDs {
		if _, err := r.db.ExecContext(ctx, `DELETE FROM policy_rule_stats WHERE cluster_id = ? AND rule_id = ?`, clusterID, id); err != nil {
			return fmt.Errorf("store: forgetting policy rule stats for rule %q: %w", id, err)
		}
	}
	return nil
}
