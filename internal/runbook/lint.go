// SPDX-License-Identifier: Apache-2.0

package runbook

import (
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// Lint validates a set of Runbook values structurally — the same job
// `vnproxctl policy lint` does for policy-as-code rules (the task card's own
// precedent: "vnproxctl policy has test/lint/examples, and runbooks should
// get the same treatment"). It never touches live state; every check here
// is a property of the Runbook values themselves.
//
// It returns every problem found, not just the first, so a catalog with
// several mistakes reports all of them in one pass — catalog_test.go runs
// this against Runbooks() and requires an empty result; it also runs it
// against deliberately-broken synthetic Runbook values to prove each check
// actually fires (T-4003's own required test: "a runbook attached to a
// finding type that no longer exists must be caught").
func Lint(rbs []Runbook) []error {
	var problems []error
	seenNames := map[string]bool{}
	knownChecks := findingsCheckNames()

	for _, rb := range rbs {
		if rb.Name == "" {
			problems = append(problems, fmt.Errorf("runbook: a runbook has an empty Name (CheckName %q)", rb.CheckName))
		} else if seenNames[rb.Name] {
			problems = append(problems, fmt.Errorf("runbook: duplicate runbook name %q", rb.Name))
		} else {
			seenNames[rb.Name] = true
		}

		if rb.CheckName == "" {
			problems = append(problems, fmt.Errorf("runbook %q: CheckName is empty", rb.Name))
		} else if !knownChecks[rb.CheckName] {
			problems = append(problems, fmt.Errorf("runbook %q: attached to check %q, which findings.AllCheckNames() does not currently list", rb.Name, rb.CheckName))
		}

		if err := lintSteps(rb); err != nil {
			problems = append(problems, fmt.Errorf("runbook %q: %w", rb.Name, err))
		}

		if err := lintTemplateImplemented(rb); err != nil {
			problems = append(problems, fmt.Errorf("runbook %q: %w", rb.Name, err))
		}
	}
	return problems
}

// lintSteps enforces the "ordered steps: read-check | op-template" shape
// the task card asks for: at least one StepReadCheck, followed by exactly
// one StepOpTemplate as the last step, and no StepOpTemplate anywhere
// before the end.
func lintSteps(rb Runbook) error {
	if len(rb.Steps) == 0 {
		return errors.New("has no declared Steps")
	}
	readChecks := 0
	for i, st := range rb.Steps {
		switch st.Kind {
		case StepReadCheck:
			readChecks++
		case StepOpTemplate:
			if i != len(rb.Steps)-1 {
				return fmt.Errorf("has a StepOpTemplate at position %d that is not its last step", i)
			}
		default:
			return fmt.Errorf("step %d has unknown StepKind %q", i, st.Kind)
		}
	}
	if readChecks == 0 {
		return errors.New("has no StepReadCheck before its op template")
	}
	last := rb.Steps[len(rb.Steps)-1]
	if last.Kind != StepOpTemplate {
		return errors.New("does not end in a StepOpTemplate")
	}
	return nil
}

// lintTemplateImplemented proves rb.Template is a case Render's switch
// actually handles, without duplicating that switch's set anywhere: it
// calls Render with a Finding whose Check matches (so ErrNotAttached can
// never be the reason) and an empty ReadContext, and checks that the
// failure — every synthetic call fails one way or another, since there is
// no real entity behind it — is never specifically ErrUnimplementedTemplate.
// Render's own default case is the single source of truth this reuses.
func lintTemplateImplemented(rb Runbook) error {
	probe := findings.Finding{Check: rb.CheckName, ID: "lint:probe"}
	_, _, err := Render(rb, probe, ReadContext{})
	if errors.Is(err, ErrUnimplementedTemplate) {
		return fmt.Errorf("names Template %q, which Render does not implement", rb.Template)
	}
	return nil
}
