package change

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// SafetyOptions carries T-203's safety-interlock inputs: the persisted,
// onboarding-confirmed protected-interface set (docs/features/blueprints.md
// §3) and the allow_dangerous_ops config flag (docs/security.md "Safety
// interlocks": "override only via config flag allow_dangerous_ops"). Both
// are threaded in by the caller (Service, which owns the file/config
// reads) rather than read from disk/config inside this pure validation
// package — see safetyValidate in validate_safety.go.
type SafetyOptions struct {
	// Protected is the onboarding-confirmed set of protected interfaces per
	// node, keyed by node name. A nil/empty ProtectedSet means "nothing is
	// protected yet" (e.g. onboarding hasn't run) — safetyValidate then
	// only evaluates the guest-bearing-bridge check.
	Protected ProtectedSet

	// Allocations is T-406's DHCP-range-overlap advisory input: every
	// currently-known IPAM allocation, already fetched fresh by the
	// caller (Service.dhcpAllocations, via the optional
	// Config.Allocations seam — see AllocationsSource in service.go) the
	// same way Protected above is fetched fresh from disk on every
	// validation call. A nil/empty Allocations means either "no live IPAM
	// data is wired" or "genuinely nothing allocated yet" — either way,
	// advisoryValidate's checkDHCPRangeOverlap simply has nothing to warn
	// about, never an error.
	Allocations []DHCPRangeAllocation

	// AllowDangerousOps downgrades every finding this class would
	// otherwise emit at SeverityError down to SeverityWarning, without
	// changing Validate's short-circuit behavior (a warning never
	// short-circuits, matching every other class).
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
// Cross-node consistency checks (spec item 4) are not assigned to any task
// in the current plan and are left as a marked insertion point below.
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

	safetyFindings := safetyValidate(ops, snap, safety)
	findings = append(findings, safetyFindings...)
	if hasError(safetyFindings) {
		return findings
	}

	// --- a future task's cross-node consistency class (spec item 4) would
	// insert here, after safety and before advisory.

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
