// SPDX-License-Identifier: Apache-2.0

package runbook_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/runbook"
)

// TestCatalog_BuiltInsPassLint is the built-in library's own self-check:
// every shipped runbook must be attached to a check that actually exists,
// declare a well-shaped Steps sequence, and name a Template Render
// actually implements.
func TestCatalog_BuiltInsPassLint(t *testing.T) {
	if problems := runbook.Lint(runbook.Runbooks()); len(problems) != 0 {
		t.Fatalf("Lint(Runbooks()) = %v, want no problems", problems)
	}
}

func TestCatalog_NamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, rb := range runbook.Runbooks() {
		if seen[rb.Name] {
			t.Errorf("duplicate runbook name %q", rb.Name)
		}
		seen[rb.Name] = true
	}
}

func TestCatalog_ByName(t *testing.T) {
	for _, rb := range runbook.Runbooks() {
		got, ok := runbook.ByName(rb.Name)
		if !ok {
			t.Errorf("ByName(%q): not found", rb.Name)
			continue
		}
		if got.Name != rb.Name {
			t.Errorf("ByName(%q).Name = %q", rb.Name, got.Name)
		}
	}
	if _, ok := runbook.ByName("does-not-exist"); ok {
		t.Error("ByName(\"does-not-exist\"): want ok=false")
	}
}

func TestCatalog_ForCheck(t *testing.T) {
	got := runbook.ForCheck("orphan_vnet")
	if len(got) != 1 || got[0].Name != runbook.DeleteOrphanVnet {
		t.Errorf("ForCheck(\"orphan_vnet\") = %+v, want exactly [%s]", got, runbook.DeleteOrphanVnet)
	}
	if got := runbook.ForCheck("no_such_check"); len(got) != 0 {
		t.Errorf("ForCheck(\"no_such_check\") = %+v, want none", got)
	}
}

// TestLint_CatchesEachProblemClass is T-4003's own required table-driven
// test: "a runbook attached to a finding type that no longer exists must be
// caught", generalized to every problem class Lint checks, each isolated
// from an otherwise-valid runbook so the table shows exactly which check
// fires for which mistake.
func TestLint_CatchesEachProblemClass(t *testing.T) {
	valid := runbook.Runbook{
		Name:      "valid",
		CheckName: "orphan_vnet", // a real, current findings check name
		Template:  runbook.TemplateDeleteOrphanVnet,
		Steps: []runbook.Step{
			{Kind: runbook.StepReadCheck, Description: "check something"},
			{Kind: runbook.StepOpTemplate, Description: "propose something"},
		},
	}

	tests := []struct { //nolint:govet // fieldalignment: table-driven test struct, declaration-order readability over packing
		name      string
		mutate    func(runbook.Runbook) runbook.Runbook
		wantClean bool
	}{
		{"valid runbook alone", func(rb runbook.Runbook) runbook.Runbook { return rb }, true},
		{
			"attached to a check that no longer exists",
			func(rb runbook.Runbook) runbook.Runbook { rb.CheckName = "check_that_was_removed_long_ago"; return rb },
			false,
		},
		{
			"empty CheckName",
			func(rb runbook.Runbook) runbook.Runbook { rb.CheckName = ""; return rb },
			false,
		},
		{
			"empty Name",
			func(rb runbook.Runbook) runbook.Runbook { rb.Name = ""; return rb },
			false,
		},
		{
			"unimplemented template",
			func(rb runbook.Runbook) runbook.Runbook { rb.Template = "not-a-real-template"; return rb },
			false,
		},
		{
			"no read-check before the op template",
			func(rb runbook.Runbook) runbook.Runbook {
				rb.Steps = []runbook.Step{{Kind: runbook.StepOpTemplate, Description: "propose something"}}
				return rb
			},
			false,
		},
		{
			"op template is not the last step",
			func(rb runbook.Runbook) runbook.Runbook {
				rb.Steps = []runbook.Step{
					{Kind: runbook.StepOpTemplate, Description: "propose something"},
					{Kind: runbook.StepReadCheck, Description: "check something"},
				}
				return rb
			},
			false,
		},
		{
			"no steps at all",
			func(rb runbook.Runbook) runbook.Runbook { rb.Steps = nil; return rb },
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problems := runbook.Lint([]runbook.Runbook{tc.mutate(valid)})
			clean := len(problems) == 0
			if clean != tc.wantClean {
				t.Errorf("Lint = %v, wantClean %v", problems, tc.wantClean)
			}
		})
	}
}

func TestLint_DuplicateNames(t *testing.T) {
	rb := runbook.Runbook{
		Name:      "dup",
		CheckName: "orphan_vnet",
		Template:  runbook.TemplateDeleteOrphanVnet,
		Steps: []runbook.Step{
			{Kind: runbook.StepReadCheck, Description: "x"},
			{Kind: runbook.StepOpTemplate, Description: "y"},
		},
	}
	if problems := runbook.Lint([]runbook.Runbook{rb, rb}); len(problems) == 0 {
		t.Error("Lint([rb, rb]): want a duplicate-name problem, got none")
	}
}
