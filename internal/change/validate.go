package change

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Validate runs the full layered validator pipeline (docs/features/
// change-management.md §2) over ops, evaluated against snap — an injected
// inventory.Graph snapshot, so this function is pure and callable from
// table-driven tests with no real network/collector dependency. It is the
// single entry point both Service.Validate (the POST /changesets/{id}/
// validate route and auto-validation on draft mutation) and the golden
// test suite exercise.
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
// T-203 (safety interlocks: protected interfaces, corosync links,
// guest-bearing bridge deletion) slots in as another ordered class between
// referential and advisory below — see the comment marking that insertion
// point. Cross-node consistency checks (spec item 4) are not assigned to
// any task in the current plan and are left as a second, later insertion
// point in the same place.
func Validate(ops []Op, snap inventory.Snapshot) []Finding {
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

	// --- T-203 inserts its safety-interlock class here (docs/security.md's
	// "Safety interlocks" section; docs/features/change-management.md §2
	// class 3): protected-interface/corosync/guest-bearing-bridge checks,
	// evaluated against the same snap plus the ops-so-far projection
	// referential.go already builds. It would short-circuit here too,
	// before advisory runs, exactly like the two classes above.

	// --- a future task's cross-node consistency class (spec item 4) would
	// insert here, after safety and before advisory.

	advisoryFindings := advisoryValidate(ops, snap)
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
