// SPDX-License-Identifier: Apache-2.0

package change

import "github.com/bgovanlu/vnprox/internal/inventory"

// policyValidate is T-2601's validator class: it evaluates the cluster's
// declarative policy set (SafetyOptions.Policy) over ops and returns the
// resulting findings — SeverityError for a `deny` rule, SeverityWarning for
// a `warn` one. It additionally publishes the full per-rule result through
// SafetyOptions.PolicyReport when the caller asked for it.
//
// An empty policy set produces no findings and touches nothing, so a
// deployment that has never installed a policy validates exactly as it did
// before this class existed (acceptance criterion 6).
func policyValidate(ops []Op, snap inventory.Snapshot, safety SafetyOptions) []Finding {
	if safety.Policy.IsEmpty() {
		return nil
	}
	result := EvaluatePolicy(PolicyInput{
		Set: safety.Policy, Protected: safety.Protected,
		EvalTime: safety.EvalTime, OverriddenTags: safety.OverriddenTags,
	}, ops, snap)
	if safety.PolicyReport != nil {
		*safety.PolicyReport = result
	}
	return result.Findings
}
