// SPDX-License-Identifier: Apache-2.0

package change

import (
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// SafetyOptions carries T-203's safety-interlock inputs: the persisted,
// onboarding-confirmed protected-interface set (docs/features/blueprints.md
// §3) and the allow_dangerous_ops config flag (docs/security.md "Safety
// interlocks": "override only via config flag allow_dangerous_ops"). Both
// are threaded in by the caller (Service, which owns the file/config
// reads) rather than read from disk/config inside this pure validation
// package — see safetyValidate in validate_safety.go.
//
// Field order is densest-pointer-first: govet's fieldalignment measures
// bytes up to the final pointer, so fields whose own pointer bytes stop
// short of their full size (a slice or a struct with a pointer-free tail,
// like SwitchSafetyInput's trailing bool, Allocations' unused len/cap, or
// PolicySet's trailing Version int) sort later, even though they were
// declared earlier.
type SafetyOptions struct {
	// EvalTime (T-4006) is threaded straight into PolicyInput.EvalTime —
	// the instant a freeze-window rule's time.* facts are computed
	// against. The caller (Service.validationInputs/policyDenial) supplies
	// its own now() at the moment it is validating, so a freeze declared
	// after a changeset was first validated is still caught the next time
	// anything revalidates (including the scheduler's own fire-time
	// revalidation — see schedule.go's safety-analysis point 4).
	EvalTime  time.Time
	Protected ProtectedSet
	// PolicyReport, when non-nil, receives the full per-rule evaluation
	// result the policy class produced. It is an optional out-parameter
	// because ValidateWithSafety is a pure function and the "which rules
	// matched" bookkeeping (Service.recordPolicyStats, which backs the
	// probably-misconfigured report) is a Service concern, not a
	// validator one — collecting it here rather than re-evaluating in the
	// Service keeps exactly one evaluation per validate call.
	PolicyReport *PolicyResult
	// OverriddenTags (T-4006) is threaded straight into
	// PolicyInput.OverriddenTags — see that field's own doc comment.
	OverriddenTags map[string]string
	Switches       SwitchSafetyInput
	Allocations    []DHCPRangeAllocation
	// TcMirror (T-4014) carries the server-enforced tc.mirror.* ceilings
	// (max concurrent sessions / aggregate declared bandwidth per node /
	// max duration) plus each node's current active-session usage, both
	// assembled once by Service.validationInputs from its own store read
	// (tcMirrorUsage) — the identical "Service reads live state, the pure
	// validator only compares against what it's given" shape Allocations
	// above already uses. Its zero value has zero ceilings, which
	// tcMirrorCapFindings (validate_safety.go) treats as "unconfigured —
	// skip the cap check", exactly like a nil Allocations skips
	// checkDHCPRangeOverlap.
	TcMirror TcMirrorSafetyInput
	// Policy (T-2601) is the cluster's declarative policy-as-code rule set
	// (policy.go). Its zero value is an empty set, which evaluates to
	// nothing at all — so every caller that does not know about policies
	// gets byte-for-byte the pre-T-2601 findings, and the engine can run
	// unconditionally in the pipeline rather than behind a flag.
	Policy            PolicySet
	AllowDangerousOps bool
}

// DHCPRangeAllocation is one existing IPAM allocation's (subnet, address,
// who) triple, as advisoryValidate's checkDHCPRangeOverlap needs it to
// detect and describe a DHCP range that would overlap already-allocated
// addresses (T-406 acceptance criterion 4). Deliberately this package's own
// small, independent shape (not internal/ipam.Allocation) — the same
// "small seam, adapted by the caller" convention every other cross-package
// input to this pure validation package already follows (compare
// InventorySource/ProtectedSet).
type DHCPRangeAllocation struct {
	// Subnet is the owning SdnSubnet's CIDR (Ref.ID convention) — matched
	// against the op's own Target.ID, not recomputed from IP containment,
	// so an allocation the caller mis-attributes to the wrong subnet never
	// silently leaks into another subnet's overlap check.
	Subnet   string
	IP       string
	Hostname string
	MAC      string
}

// Validate runs the full layered validator pipeline (docs/features/
// change-management.md §2) over ops, evaluated against snap — an injected
// inventory.Graph snapshot, so this function is pure and callable from
// table-driven tests with no real network/collector dependency. It is a
// thin wrapper around ValidateWithSafety with a zero-value SafetyOptions
// (no protected interfaces, allow_dangerous_ops=false), kept so every
// existing caller/test that doesn't care about T-203's safety-interlock
// inputs is unaffected by this signature not changing.
func Validate(ops []Op, snap inventory.Snapshot) []Finding {
	return ValidateWithSafety(ops, snap, SafetyOptions{})
}

// ValidateWithSafety is Validate plus T-203's safety-interlock class,
// parameterized by safety (the protected-interface set and the
// allow_dangerous_ops flag — see SafetyOptions). It is the single entry
// point both Service.Validate (the POST /changesets/{id}/validate route and
// auto-validation on draft mutation) and the golden test suite exercise.
//
// Classes run in the documented order and short-circuit at the first class
// that produces any error-severity finding: docs/features/
// change-management.md §2 lists schema(1), referential(2), safety(3),
// cross-node(4), advisory(5) in that order, and this task's card asks for
// "short-circuit on schema errors" — generalized here to any earlier class,
// since referential/safety/cross-node checks over a schema-invalid op (or
// safety checks over a referentially-broken graph) would themselves be
// operating on nonsense.
//
// Cross-node consistency checks (spec item 4) run as class 4 between safety
// and advisory (T-801; see crossnodeValidate in validate_crossnode.go).
func ValidateWithSafety(ops []Op, snap inventory.Snapshot, safety SafetyOptions) []Finding {
	var findings []Finding

	schemaFindings := schemaValidate(ops)
	findings = append(findings, schemaFindings...)
	if hasError(schemaFindings) {
		return findings
	}

	referentialFindings := referentialValidate(ops, snap)
	findings = append(findings, referentialFindings...)
	if hasError(referentialFindings) {
		return findings
	}

	// T-402's SDN pre-apply validator class (zone node coverage, bridge
	// existence on member nodes, tag uniqueness — docs/features/sdn.md §4)
	// runs here: after referential (targets must exist first) and before
	// safety (a zone that can't even apply shouldn't also be evaluated for
	// interlocks against inconsistent state).
	sdnFindings := sdnValidate(ops, snap)
	findings = append(findings, sdnFindings...)
	if hasError(sdnFindings) {
		return findings
	}

	// T-1205's switch-push authorization + interlock class: the feature-flag/
	// enabled/PVE-facing gates plus the no-override protected-switch-port
	// interlock. Runs after referential/sdn (the op must be well-formed) and
	// before safetyValidate — its protected_switch_port finding lives here,
	// not in safetyValidate, precisely so AllowDangerousOps never downgrades
	// it.
	switchFindings := switchValidate(ops, safety.Switches)
	findings = append(findings, switchFindings...)
	if hasError(switchFindings) {
		return findings
	}

	// T-4014's tc.mirror.* concurrent-session/bandwidth/duration ceiling
	// class — its own class, not folded into safetyValidate, for the exact
	// reason switchFindings' codeProtectedSwitchPort lives outside it too
	// (see tcMirrorCapValidate's own doc comment): a resource ceiling is
	// not an AllowDangerousOps-overridable connectivity interlock.
	tcMirrorCapFindings := tcMirrorCapValidate(ops, safety.TcMirror)
	findings = append(findings, tcMirrorCapFindings...)
	if hasError(tcMirrorCapFindings) {
		return findings
	}

	// T-2601's policy-as-code class. It runs here — after the classes that
	// establish an op is well-formed and referentially coherent (a policy
	// asserting over net-effect inventory facts would be reasoning about
	// nonsense otherwise), and immediately BEFORE the safety class — so an
	// organisation's own refusal is never masked by a built-in interlock
	// firing on the same op. Conceptually it is the generalization of the
	// single organisational rule protected.go hard-codes, so it sits
	// adjacent to it.
	//
	// It is inside this function, not layered on by Service, precisely
	// because this is the ONE entry point both the validate route and the
	// pre-apply revalidation (apply.go's beginApply) share: a policy gate
	// bolted on anywhere else would be bypassable by the other path, and
	// CLAUDE.md's stage→validate→diff→apply→confirm sequence has no room
	// for a second gate.
	policyFindings := policyValidate(ops, snap, safety)
	findings = append(findings, policyFindings...)
	if hasError(policyFindings) {
		return findings
	}

	safetyFindings := safetyValidate(ops, snap, safety)
	findings = append(findings, safetyFindings...)
	if hasError(safetyFindings) {
		return findings
	}

	// Cross-node consistency class (spec item 4, T-801): folds the
	// changeset's projected effect across the whole cluster and compares
	// same-named bridges/MTU/SDN-zone realization across nodes, via the
	// comparison families internal/change shares with internal/drift through
	// internal/xnode. Runs after safety and before advisory, short-circuiting
	// advisory on any error exactly like the earlier classes.
	crossnodeFindings := crossnodeValidate(ops, snap)
	findings = append(findings, crossnodeFindings...)
	if hasError(crossnodeFindings) {
		return findings
	}

	advisoryFindings := advisoryValidate(ops, snap, safety.Allocations)
	findings = append(findings, advisoryFindings...)

	return findings
}

// hasError reports whether findings contains any SeverityError entry.
func hasError(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// errorf builds a SeverityError Finding.
func errorf(code, ref, format string, args ...any) Finding {
	return Finding{Severity: SeverityError, Code: code, Message: fmt.Sprintf(format, args...), Ref: ref}
}

// warnf builds a SeverityWarning Finding.
func warnf(code, ref, format string, args ...any) Finding {
	return Finding{Severity: SeverityWarning, Code: code, Message: fmt.Sprintf(format, args...), Ref: ref}
}
