// SPDX-License-Identifier: Apache-2.0

// policy_service.go is the store-backed, audited half of T-2601: reading
// the cluster's installed policy set, replacing it (audited `policy.update`
// with the full rule-set diff), evaluating it on demand, and keeping the
// per-rule bookkeeping that turns "this rule has never matched anything"
// into a report instead of a silent pass.
//
// Nothing here is a second enforcement point. Enforcement happens in one
// place only — policyValidate, inside ValidateWithSafety — and everything
// in this file either feeds that call or reads what it produced.

package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// DefaultPolicyUnmatchedAfter is how long a rule may go without matching
// anything before it is reported as probably-misconfigured (the card's "an
// unmatched rule on every changeset for N days"). Two weeks is long enough
// that a genuinely rare rule (a quarterly firewall change) is not slandered,
// and short enough that a typo surfaces well before an audit does.
const DefaultPolicyUnmatchedAfter = 14 * 24 * time.Hour

// minPolicyEvalsBeforeReport keeps a freshly installed rule out of the
// report until the cluster has actually exercised it a few times — an
// idle weekend is not evidence of a misconfiguration.
const minPolicyEvalsBeforeReport = 10

// ErrPolicyNotConfigured is returned by the policy read/write API when this
// daemon was built without a policy store (Config.Policies nil). Evaluation
// still works — an unconfigured deployment simply has an empty rule set, and
// therefore no policy findings — so this is a "you cannot administer what
// isn't wired" error, never a fail-open on enforcement.
type ErrPolicyNotConfigured struct{}

func (e *ErrPolicyNotConfigured) Error() string {
	return "change: policy store is not configured on this daemon"
}

// PolicyRuleStatus is one rule's runtime bookkeeping as the API reports it.
type PolicyRuleStatus struct {
	RuleID        string `json:"ruleId"`
	FirstSeenAt   int64  `json:"firstSeenAt"`
	LastMatchedAt int64  `json:"lastMatchedAt"`
	EvalCount     int64  `json:"evalCount"`
	MatchCount    int64  `json:"matchCount"`
	// ProbablyMisconfigured is the card's "a policy that matches nothing is
	// an error, not a silent pass", runtime half: the rule has been through
	// enough evaluations, over a long enough window, without ever matching
	// an op. It is a report, not a refusal — a rule the operator confirms
	// is simply rare stays installed and keeps working.
	ProbablyMisconfigured bool `json:"probablyMisconfigured"`
}

// PolicyStatus is the whole administered state of one cluster's policy set:
// the rules themselves, the store revision they were installed as, and each
// rule's runtime bookkeeping.
type PolicyStatus struct {
	UpdatedBy string             `json:"updatedBy,omitempty"`
	Rules     []PolicyRuleStatus `json:"rules,omitempty"`
	Set       PolicySet          `json:"set"`
	Revision  int64              `json:"revision"`
	UpdatedAt int64              `json:"updatedAt,omitempty"`
}

// storedPolicySet reads clusterID's installed rule set. A cluster with no
// installed set (or a daemon with no policy store wired) yields the empty
// set, which evaluates to nothing.
//
// A stored document this build cannot parse is NOT degraded to "no policy":
// it returns an error, and every caller turns that into a blocking
// codePolicyInvalid finding. A cluster that has declared a policy must never
// silently validate as though it had none.
func (s *Service) storedPolicySet(ctx context.Context, clusterID string) (PolicySet, int64, error) {
	if s.policies == nil {
		return PolicySet{}, 0, nil
	}
	row, err := s.policies.Get(ctx, clusterID)
	if errors.Is(err, store.ErrNotFound) {
		return PolicySet{}, 0, nil
	}
	if err != nil {
		return PolicySet{}, 0, fmt.Errorf("change: reading policy set for cluster %q: %w", clusterID, err)
	}
	set, err := ParsePolicySet("", []byte(row.RulesJSON))
	if err != nil {
		return PolicySet{}, row.Revision, fmt.Errorf("change: stored policy set for cluster %q (revision %d) is unusable: %w", clusterID, row.Revision, err)
	}
	return set, row.Revision, nil
}

// policyForValidation resolves the rule set one validation run should use,
// plus any findings about the policy set ITSELF (as opposed to about the
// ops). It never returns an error: a broken stored set becomes a blocking
// finding on the changeset, which is what the operator needs to see.
func (s *Service) policyForValidation(ctx context.Context, clusterID string) (PolicySet, []Finding) {
	set, _, err := s.storedPolicySet(ctx, clusterID)
	if err != nil {
		s.log.Error("change: policy set unusable; refusing to validate as if unguarded", "cluster_id", clusterID, "error", err)
		return PolicySet{}, []Finding{errorf(codePolicyInvalid, "", "the cluster's installed policy set cannot be parsed by this daemon, so no changeset can be validated against it: %v", err)}
	}
	return set, nil
}

// validationInputs assembles every input the validator pipeline reads for
// one run scoped to clusterID: T-203's safety-interlock inputs, T-406's
// live IPAM allocations, T-1205's switch-push gates, and T-2601's policy
// set. It is deliberately the ONE place those are put together, so the
// validate route and the pre-apply revalidation (apply.go's beginApply)
// can never diverge on what the validator was given.
//
// report, when non-nil, collects the policy evaluation's per-rule detail
// for the caller to persist as bookkeeping (recordPolicyStats). The
// returned findings concern the POLICY SET itself (e.g. it is unparsable),
// not the ops, and are prepended by the caller to the pipeline's own.
// changesetID/ops (T-4006) are used only to look up a freeze-window
// override pinned to changesetID's current ops (overriddenPolicyTags,
// freeze_override.go) — "" / nil is a complete no-op, matching every
// pre-T-4006 caller.
func (s *Service) validationInputs(ctx context.Context, clusterID, changesetID string, ops []Op, report *PolicyResult) (SafetyOptions, []Finding) {
	safety := s.safetyOptions()
	safety.Allocations = s.dhcpAllocations(ctx)
	safety.TcMirror = s.tcMirrorUsage(ctx)
	safety.Switches = s.switchSafetyInput(ctx)
	policy, policyFindings := s.policyForValidation(ctx, clusterID)
	safety.Policy = policy
	safety.PolicyReport = report
	safety.EvalTime = s.now()
	safety.OverriddenTags = s.overriddenPolicyTags(ctx, changesetID, ops)
	return safety, policyFindings
}

// recordPolicyStats folds one evaluation's per-rule match counts into the
// store, and warns about any rule that has now gone long enough without
// matching anything to be worth an operator's attention. Best-effort by
// design: bookkeeping must never fail a validation.
func (s *Service) recordPolicyStats(ctx context.Context, clusterID string, result PolicyResult) {
	if s.policies == nil || len(result.Rules) == 0 {
		return
	}
	ids := make([]string, 0, len(result.Rules))
	for _, r := range result.Rules {
		ids = append(ids, r.RuleID)
	}
	if err := s.policies.RecordEvaluation(ctx, clusterID, ids, result.MatchCounts(), s.now().Unix()); err != nil {
		s.log.Warn("change: recording policy rule stats", "cluster_id", clusterID, "error", err)
		return
	}
	stats, err := s.policies.Stats(ctx, clusterID)
	if err != nil {
		return
	}
	now := s.now().Unix()
	for _, st := range stats {
		if s.policyRuleProbablyMisconfigured(st, now) {
			s.log.Warn("change: policy rule has never matched anything and is probably misconfigured",
				"cluster_id", clusterID, "rule_id", st.RuleID, "eval_count", st.EvalCount,
				"first_seen_at", st.FirstSeenAt)
		}
	}
}

func (s *Service) policyRuleProbablyMisconfigured(st store.PolicyRuleStat, nowUnix int64) bool {
	if st.MatchCount > 0 || st.EvalCount < minPolicyEvalsBeforeReport {
		return false
	}
	window := s.policyUnmatchedAfter
	if window <= 0 {
		window = DefaultPolicyUnmatchedAfter
	}
	return nowUnix-st.FirstSeenAt >= int64(window.Seconds())
}

// policyDenial reports whether cs is refused by the cluster's installed
// policy, as an *ErrValidationBlocked carrying the offending findings — or
// nil when it is not. It exists so Diff can refuse a denied changeset
// before computing anything (acceptance criterion 3) without re-running the
// whole validator pipeline, which diff has never done.
//
// A policy set this daemon cannot parse denies everything, for the same
// fail-closed reason policyForValidation gives.
func (s *Service) policyDenial(ctx context.Context, cs Changeset) error {
	set, loadFindings := s.policyForValidation(ctx, cs.ClusterID)
	if len(loadFindings) > 0 {
		return &ErrValidationBlocked{Findings: loadFindings}
	}
	if set.IsEmpty() {
		return nil
	}
	expanded, _ := s.expandRawReplaceOps(ctx, cs.Ops)
	result := EvaluatePolicy(PolicyInput{
		Set: set, Protected: s.safetyOptions().Protected,
		EvalTime: s.now(), OverriddenTags: s.overriddenPolicyTags(ctx, cs.ID, cs.Ops),
	}, expanded, s.inventorySnapshot())
	if !result.Denied() {
		return nil
	}
	blocking := make([]Finding, 0, len(result.Findings))
	for _, f := range result.Findings {
		if f.Severity == SeverityError {
			blocking = append(blocking, f)
		}
	}
	return &ErrValidationBlocked{Findings: blocking}
}

// PolicyStatus returns the cluster's installed policy set plus each rule's
// runtime bookkeeping — the read behind `GET /policies`.
func (s *Service) PolicyStatus(ctx context.Context) (PolicyStatus, error) {
	if s.policies == nil {
		return PolicyStatus{}, &ErrPolicyNotConfigured{}
	}
	clusterID := s.localClusterID
	set, revision, err := s.storedPolicySet(ctx, clusterID)
	if err != nil {
		return PolicyStatus{}, err
	}
	out := PolicyStatus{Set: set, Revision: revision}
	if row, rerr := s.policies.Get(ctx, clusterID); rerr == nil {
		out.UpdatedBy, out.UpdatedAt = row.UpdatedBy, row.UpdatedAt
	}

	stats := map[string]store.PolicyRuleStat{}
	if rows, serr := s.policies.Stats(ctx, clusterID); serr == nil {
		for _, st := range rows {
			stats[st.RuleID] = st
		}
	}
	now := s.now().Unix()
	for _, rule := range set.Rules {
		st := stats[rule.ID]
		out.Rules = append(out.Rules, PolicyRuleStatus{
			RuleID:                rule.ID,
			FirstSeenAt:           st.FirstSeenAt,
			LastMatchedAt:         st.LastMatchedAt,
			EvalCount:             st.EvalCount,
			MatchCount:            st.MatchCount,
			ProbablyMisconfigured: s.policyRuleProbablyMisconfigured(st, now),
		})
	}
	return out, nil
}

// SetPolicySet installs set as clusterID's policy, bumping the store
// revision and auditing `policy.update` with the FULL rule-set diff — both
// sides of every changed rule, and the whole body of every added/removed one
// — so the audit entry alone reconstructs what changed (acceptance
// criterion 7).
//
// The set is validated before anything is written: a malformed rule set is
// rejected with the same *PolicyLoadError the file loader produces, and
// nothing is stored and nothing is audited.
func (s *Service) SetPolicySet(ctx context.Context, author string, set PolicySet) (PolicyStatus, error) {
	if s.policies == nil {
		return PolicyStatus{}, &ErrPolicyNotConfigured{}
	}
	if err := set.Validate(""); err != nil {
		return PolicyStatus{}, err
	}
	if set.Version == 0 {
		set.Version = PolicyFormatVersion
	}

	clusterID := s.localClusterID
	current, revision, err := s.storedPolicySet(ctx, clusterID)
	if err != nil {
		// An unparsable CURRENT set must not block replacing it — that is
		// exactly the situation an operator needs to be able to fix. The
		// diff is then computed against an empty set, and says so.
		s.log.Warn("change: current policy set is unparsable; diffing the replacement against an empty set", "cluster_id", clusterID, "error", err)
		current = PolicySet{}
	}

	diff := DiffPolicySets(current, set)
	if diff.IsEmpty() && revision > 0 {
		// Idempotent re-install (the daemon re-applying its configured
		// policy file at every start): no new revision, no audit entry —
		// an audit trail full of "changed nothing" entries is what makes
		// the real ones easy to miss.
		return s.PolicyStatus(ctx)
	}

	rulesJSON, err := json.Marshal(set)
	if err != nil {
		return PolicyStatus{}, fmt.Errorf("change: marshaling policy set for cluster %q: %w", clusterID, err)
	}
	next := revision + 1
	row := store.PolicySet{
		ClusterID: clusterID,
		Revision:  next,
		RulesJSON: string(rulesJSON),
		UpdatedBy: author,
		UpdatedAt: s.now().Unix(),
	}
	if err := s.policies.Put(ctx, row); err != nil {
		return PolicyStatus{}, fmt.Errorf("change: installing policy set for cluster %q: %w", clusterID, err)
	}

	// Retired rules lose their bookkeeping, so a later rule reusing the id
	// does not inherit a stranger's "never matched" history.
	if len(diff.Removed) > 0 {
		removed := make([]string, 0, len(diff.Removed))
		for _, r := range diff.Removed {
			removed = append(removed, r.ID)
		}
		if err := s.policies.ForgetRules(ctx, clusterID, removed); err != nil {
			s.log.Warn("change: clearing stats for removed policy rules", "cluster_id", clusterID, "error", err)
		}
	}

	s.appendAudit(ctx, author, "policy.update", "success", "", map[string]any{
		"clusterId":    clusterID,
		"fromRevision": revision,
		"toRevision":   next,
		"ruleCount":    len(set.Rules),
		"diff":         diff,
	})
	s.log.Info("change: policy set updated", "cluster_id", clusterID, "revision", next,
		"added", len(diff.Added), "removed", len(diff.Removed), "changed", len(diff.Changed))

	return s.PolicyStatus(ctx)
}

// EvaluatePolicySet evaluates set (or, when set is empty, the cluster's
// installed one) over ops against the live inventory snapshot, WITHOUT
// staging anything — the read behind `POST /policies/test` and
// `vnproxctl policy test`, so a rule can be developed against a real
// changeset safely.
//
// This is also the interface the later cards in this phase consume:
// T-2604 reads PolicyResult.TaggedOps to learn which ops fall in a
// policy-tagged class, T-2705 policy-checks an MCP-staged op before it
// becomes a draft, and T-2706 reads PolicyResult.Rules as compliance
// evidence. All three go through this one function, so none of them can
// end up with its own idea of what a rule means.
func (s *Service) EvaluatePolicySet(ctx context.Context, set PolicySet, ops []Op) (PolicyResult, error) {
	if set.IsEmpty() {
		stored, _, err := s.storedPolicySet(ctx, s.localClusterID)
		if err != nil {
			return PolicyResult{}, err
		}
		set = stored
	} else if err := set.Validate(""); err != nil {
		return PolicyResult{}, err
	}
	if set.IsEmpty() {
		return PolicyResult{}, nil
	}
	expanded, _ := s.expandRawReplaceOps(ctx, ops)
	// EvalTime is "now": `vnproxctl policy test`/`POST /policies/test` is a
	// dry run, so it should show a freeze-tagged rule firing (or not)
	// exactly as it would right now — no OverriddenTags, deliberately: this
	// is a diagnostic tool, and hiding a violation behind an override here
	// would defeat the point of testing the rule.
	in := PolicyInput{Set: set, Protected: s.safetyOptions().Protected, EvalTime: s.now()}
	return EvaluatePolicy(in, expanded, s.inventorySnapshot()), nil
}

// EvaluatePolicyForChangeset is EvaluatePolicySet against an existing
// changeset's ops, looked up by id — `vnproxctl policy test --changeset=id`.
// It reads the changeset and nothing else: no status transition, no
// findings persisted, no staging.
func (s *Service) EvaluatePolicyForChangeset(ctx context.Context, set PolicySet, changesetID string) (PolicyResult, error) {
	cs, err := s.Get(ctx, changesetID)
	if err != nil {
		return PolicyResult{}, err
	}
	return s.EvaluatePolicySet(ctx, set, cs.Ops)
}
