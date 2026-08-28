// SPDX-License-Identifier: Apache-2.0

package change

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file guards three hand-maintained mirrors of THIS package's constants
// that live in TypeScript, where the Go compiler cannot see them.
//
// The frontend cannot import a Go package, so `web/src/changesets/
// applyStrategy.ts` re-states canaryUnstageableKinds, planRequiresPVESession's
// step set, and the hold bounds in order to disable an ineligible option in
// the UI rather than let the operator submit it and read a 422 back. That is
// the right behaviour and it creates a copy, and a copy nothing checks is a
// copy that drifts.
//
// This project has already paid for that lesson twice in one week:
// `vnproxctl verify`'s backup check decoded field names the CLI never emitted
// (its fixture had invented the same wrong names, so check and test agreed
// with each other and both disagreed with the program), and the frontend's
// PlanStep union was missing two real step kinds. Both were found by running
// against reality, not by a test. These assertions are the cheap version of
// running against reality.
//
// Reading the .ts as text is deliberate. Any richer coupling — generating the
// TypeScript, or exporting a JSON manifest both sides load — would make the
// two agree by construction and would also mean nothing tests the agreement.
// The failure mode being defended against is a human editing one side.

const applyStrategyTS = "../../web/src/changesets/applyStrategy.ts"
const apiTypesTS = "../../web/src/api/types.ts"

// tsStringSet extracts the string literals from a `new Set([...])`
// initialiser assigned to the named const.
func tsStringSet(t *testing.T, source, constName string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(constName) + `\s*:[^=]*=\s*new Set\(\[(.*?)\]\)`)
	m := re.FindStringSubmatch(source)
	if m == nil {
		t.Fatalf("could not find a `new Set([...])` initialiser for %s in %s — the mirror moved or was renamed, which is itself the drift this test exists to catch", constName, applyStrategyTS)
	}
	return tsStringLiterals(m[1])
}

func tsStringLiterals(block string) []string {
	re := regexp.MustCompile(`"([a-z_]+)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

func tsNumberConst(t *testing.T, source, constName string) int {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(constName) + `\s*=\s*(\d+)`)
	m := re.FindStringSubmatch(source)
	if m == nil {
		t.Fatalf("could not find `%s = <number>` in %s", constName, applyStrategyTS)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("%s is not a number: %v", constName, err)
	}
	return n
}

func readTS(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

func sortedKinds(kinds ...StepKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	sort.Strings(out)
	return out
}

func TestCanaryUnstageableKindsMatchTheFrontendMirror(t *testing.T) {
	t.Parallel()
	source := readTS(t, applyStrategyTS)

	var want []string
	for kind := range canaryUnstageableKinds {
		want = append(want, string(kind))
	}
	sort.Strings(want)

	got := tsStringSet(t, source, "UNSTAGEABLE_KINDS")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("UNSTAGEABLE_KINDS in %s is %v, but change.canaryUnstageableKinds is %v.\n"+
			"The frontend disables the canary option for these kinds so the operator never submits a strategy the server will refuse; a mismatch means it either offers an option that 422s or hides one that would work.",
			applyStrategyTS, got, want)
	}
}

// TestPVESessionKindsMatchTheFrontendMirror pins planRequiresPVESession's
// switch. There is no exported set to range over — the Go side is a `case`
// list — so this names the kinds explicitly and the assertion below keeps
// that list honest against the function itself.
func TestPVESessionKindsMatchTheFrontendMirror(t *testing.T) {
	t.Parallel()
	source := readTS(t, applyStrategyTS)

	want := sortedKinds(StepSDNStage, StepSDNApply, StepFwApply, StepFwVerify, StepIpamAlloc)

	// Prove the list above is planRequiresPVESession's real behaviour rather
	// than a second hand-copy of it: every named kind must make the function
	// say yes, and every OTHER kind the package defines must make it say no.
	for _, name := range want {
		if !planRequiresPVESession(Plan{Steps: []Step{{Kind: StepKind(name)}}}) {
			t.Fatalf("planRequiresPVESession says %q needs no PVE session, but this test's list claims it does", name)
		}
	}
	for _, kind := range allStepKindsForContractTest() {
		named := false
		for _, w := range want {
			if w == string(kind) {
				named = true
			}
		}
		if named {
			continue
		}
		if planRequiresPVESession(Plan{Steps: []Step{{Kind: kind}}}) {
			t.Fatalf("planRequiresPVESession says %q needs a PVE session, but this test's list omits it — add it here AND to %s", kind, applyStrategyTS)
		}
	}

	got := tsStringSet(t, source, "PVE_SESSION_KINDS")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PVE_SESSION_KINDS in %s is %v, but planRequiresPVESession accepts %v.\n"+
			"The frontend disables `gate: \"auto\"` on these plans; a mismatch means it offers automatic promotion for a changeset the server will refuse to promote.",
			applyStrategyTS, got, want)
	}
}

func TestCanaryHoldBoundsMatchTheFrontendMirror(t *testing.T) {
	t.Parallel()
	source := readTS(t, applyStrategyTS)

	for _, tc := range []struct {
		constName string
		want      int
	}{
		{"MIN_CANARY_HOLD_SEC", int(MinCanaryHold.Seconds())},
		{"MAX_CANARY_HOLD_SEC", int(MaxCanaryHold.Seconds())},
		{"DEFAULT_CANARY_HOLD_SEC", int(DefaultCanaryHold.Seconds())},
	} {
		if got := tsNumberConst(t, source, tc.constName); got != tc.want {
			t.Errorf("%s in %s is %d, want %d — the UI would accept a hold the server refuses, or refuse one it accepts", tc.constName, applyStrategyTS, got, tc.want)
		}
	}
}

// TestPlanStepKindsMatchTheTypeScriptUnion is the one that would have caught
// `switch_apply` and `qos_apply` being absent from the frontend's PlanStep
// union — two real step kinds that were silently mistyped until 2026-08-16.
func TestPlanStepKindsMatchTheTypeScriptUnion(t *testing.T) {
	t.Parallel()
	source := readTS(t, apiTypesTS)

	// The union sits inside the PlanStep interface, introduced by `kind:`.
	re := regexp.MustCompile(`(?s)kind:\s*((?:\s*\|\s*"[a-z_]+")+)\s*;`)
	m := re.FindStringSubmatch(source)
	if m == nil {
		t.Fatalf("could not find PlanStep's multi-line `kind:` union in %s", apiTypesTS)
	}
	got := tsStringLiterals(m[1])

	want := make([]string, 0)
	for _, k := range allStepKindsForContractTest() {
		want = append(want, string(k))
	}
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PlanStep[\"kind\"] in %s is %v, but change.StepKind defines %v.\n"+
			"A missing kind is not a type error anywhere — it silently mistypes a real plan step the server sends.",
			apiTypesTS, got, want)
	}
}

// allStepKindsForContractTest is the authoritative list of this package's
// StepKind constants. It is hand-maintained BUT cannot silently rot: adding a
// StepKind without adding it here makes TestEveryStepKindIsInTheContractList
// fail, and that test derives its expectation from the source file rather
// than from this slice.
func allStepKindsForContractTest() []StepKind {
	return []StepKind{
		StepStageFile, StepReload, StepSDNApply, StepSDNStage, StepFwApply,
		StepFwVerify, StepWgApply, StepSwitchApply, StepIpamAlloc, StepQosApply,
		StepTcMirrorApply,
	}
}

func TestEveryStepKindIsInTheContractList(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("apply_plan.go")
	if err != nil {
		t.Fatalf("reading apply_plan.go: %v", err)
	}
	re := regexp.MustCompile(`Step[A-Za-z]+ +StepKind += +"([a-z_]+)"`)
	var declared []string
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		declared = append(declared, m[1])
	}
	sort.Strings(declared)

	var listed []string
	for _, k := range allStepKindsForContractTest() {
		listed = append(listed, string(k))
	}
	sort.Strings(listed)

	if strings.Join(declared, ",") != strings.Join(listed, ",") {
		t.Errorf("apply_plan.go declares StepKinds %v but allStepKindsForContractTest lists %v — add the new kind there, and to PlanStep[\"kind\"] in %s", declared, listed, apiTypesTS)
	}
}
