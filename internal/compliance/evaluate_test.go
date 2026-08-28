// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// evaluate_test.go covers T-2706's acceptance criteria at the model: AC1
// (a control whose mapped checks all pass reports `pass`, with each check
// named), AC2's model half (no mapping ⇒ `unmapped`, never `pass`), AC3
// (one failing mapped check fails the control and is named), and AC6 (an
// unmapped check is reported, and degrades no control).
//
// The renderer half of AC2 and all of AC4 live in
// internal/docexport/compliance_test.go, which drives every registered
// output format.

var testNow = time.Unix(1_700_000_000, 0)

func testProfile(controls ...Control) Profile {
	return Profile{
		FormatVersion: ProfileFormatVersion,
		ID:            "test-profile",
		Title:         "Test profile",
		Version:       "9.9.9",
		Notice:        "Not a certification.",
		Controls:      controls,
	}
}

func checkControl(id string, checks ...string) Control {
	c := Control{ID: id, Title: "Control " + id, Statement: "Statement for " + id}
	for _, name := range checks {
		c.Evidence = append(c.Evidence, Evidence{Kind: EvidenceCheck, Check: name})
	}
	return c
}

func controlByID(t *testing.T, r Report, id string) ControlResult {
	t.Helper()
	for _, c := range r.Controls {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("report has no control %q", id)
	return ControlResult{}
}

func TestEvaluate_ControlStatuses(t *testing.T) {
	factors := []posture.Factor{
		{Name: posture.FactorSegmentation, ScorePct: 80, Evaluated: true, Detail: "80%"},
		{Name: posture.FactorAnomalyRate, ScorePct: posture.NotEvaluatedScore, Evaluated: false, Caveat: "cold start"},
		{Name: posture.FactorExposedPorts, ScorePct: 40, Evaluated: true, Detail: "6 ports"},
	}

	tests := []struct {
		name    string
		want    Status
		control Control
		// wantEvidenceNamed are evidence keys the result must name.
		wantEvidenceNamed []string
		// wantFailingNamed are evidence keys the result must name as failing.
		wantFailingNamed []string
		findings         []FindingRef
		policy           PolicyState
		posture          posture.Posture
		postOK           bool
	}{
		{
			// AC1.
			name:              "all mapped checks clean passes and names each check",
			control:           checkControl("C1", "mgmt_single_path", "bond_slave_down"),
			want:              StatusPass,
			wantEvidenceNamed: []string{"check:mgmt_single_path", "check:bond_slave_down"},
		},
		{
			// AC3.
			name:    "one failing mapped check fails the control and is named",
			control: checkControl("C1", "mgmt_single_path", "bond_slave_down"),
			findings: []FindingRef{
				{ID: "health:bond_slave_down|bond0", Check: "bond_slave_down", Severity: "warning"},
			},
			want:              StatusFail,
			wantEvidenceNamed: []string{"check:mgmt_single_path", "check:bond_slave_down"},
			wantFailingNamed:  []string{"check:bond_slave_down"},
		},
		{
			// AC2, model half.
			name:    "a control with no mapping is unmapped, never pass",
			control: Control{ID: "C1", Title: "t", Statement: "s", UnmappedReason: "vnprox observes none of this"},
			want:    StatusUnmapped,
		},
		{
			name:     "an info finding does not fail a control whose threshold is warning",
			control:  checkControl("C1", "trunk_unused_vlans"),
			findings: []FindingRef{{ID: "health:trunk_unused_vlans|x", Check: "trunk_unused_vlans", Severity: "info"}},
			want:     StatusPass,
		},
		{
			name:     "an info finding fails a control whose profile lowered the threshold",
			control:  Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidenceCheck, Check: "trunk_unused_vlans", FailAt: "info"}}},
			findings: []FindingRef{{ID: "health:trunk_unused_vlans|x", Check: "trunk_unused_vlans", Severity: "info"}},
			want:     StatusFail,
		},
		{
			// Acknowledgement is triage, not remediation.
			name:     "an acknowledged finding still fails its control",
			control:  checkControl("C1", "bond_slave_down"),
			findings: []FindingRef{{ID: "health:bond_slave_down|b", Check: "bond_slave_down", Severity: "error", Acked: true}},
			want:     StatusFail,
		},
		{
			// A severity we could not grade must not read as clean.
			name:     "a finding with unknown severity fails its control",
			control:  checkControl("C1", "bond_slave_down"),
			findings: []FindingRef{{ID: "health:bond_slave_down|b", Check: "bond_slave_down"}},
			want:     StatusFail,
		},
		{
			name:    "a posture factor at or above the minimum passes",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePosture, Factor: posture.FactorSegmentation, MinScore: 70}}},
			posture: posture.Posture{Factors: factors}, postOK: true,
			want: StatusPass,
		},
		{
			name:    "a posture factor below the minimum fails",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePosture, Factor: posture.FactorExposedPorts, MinScore: 70}}},
			posture: posture.Posture{Factors: factors}, postOK: true,
			want:             StatusFail,
			wantFailingNamed: []string{"posture:" + posture.FactorExposedPorts},
		},
		{
			// T-1607's honesty channel is carried through, not flattened.
			name:    "a posture factor posture itself could not evaluate is not evaluated here either",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePosture, Factor: posture.FactorAnomalyRate, MinScore: 1}}},
			posture: posture.Posture{Factors: factors}, postOK: true,
			want: StatusNotEvaluated,
		},
		{
			name:    "a posture factor this build does not report is not evaluated",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePosture, Factor: "invented_factor", MinScore: 1}}},
			posture: posture.Posture{Factors: factors}, postOK: true,
			want: StatusNotEvaluated,
		},
		{
			name:    "no posture score at all leaves posture evidence not evaluated",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePosture, Factor: posture.FactorSegmentation, MinScore: 1}}},
			want:    StatusNotEvaluated,
		},
		{
			name:    "an installed, matching policy rule is evidence",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Rule: "no-flat-vlan"}}},
			policy:  PolicyState{Configured: true, Rules: []PolicyRuleRef{{ID: "no-flat-vlan", MatchCount: 12, EvalCount: 200}}},
			want:    StatusPass,
		},
		{
			// The T-2601 author's instruction, honoured: an unmatched rule
			// must not render as pass.
			name:    "an installed rule the policy engine calls probably-misconfigured is not evidence",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Rule: "no-flat-vlan"}}},
			policy:  PolicyState{Configured: true, Rules: []PolicyRuleRef{{ID: "no-flat-vlan", EvalCount: 400, ProbablyMisconfigured: true}}},
			want:    StatusNotEvaluated,
		},
		{
			name:    "a policy rule that is not installed is not evidence",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Rule: "no-flat-vlan"}}},
			policy:  PolicyState{Configured: true},
			want:    StatusNotEvaluated,
		},
		{
			name:    "a policy tag with no installed rule carrying it is not evidence",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Tag: "change-control"}}},
			policy:  PolicyState{Configured: true, Rules: []PolicyRuleRef{{ID: "other", Tags: []string{"unrelated"}}}},
			want:    StatusNotEvaluated,
		},
		{
			name:    "a policy tag matched by an actively-matching rule is evidence",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Tag: "change-control"}}},
			policy:  PolicyState{Configured: true, Rules: []PolicyRuleRef{{ID: "r1", Tags: []string{"change-control"}, MatchCount: 3}}},
			want:    StatusPass,
		},
		{
			name:    "no policy store configured leaves policy evidence not evaluated",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Tag: "change-control"}}},
			want:    StatusNotEvaluated,
		},
		{
			// A partially-evaluated control must not assert more than it checked.
			name: "a satisfied item plus an unevaluated one is not evaluated, not pass",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{
				{Kind: EvidenceCheck, Check: "mgmt_single_path"},
				{Kind: EvidencePolicy, Rule: "absent"},
			}},
			policy: PolicyState{Configured: true},
			want:   StatusNotEvaluated,
		},
		{
			// A real failure outranks an unevaluated item: the reader must
			// see the failure, not a shrug.
			name: "a failing item plus an unevaluated one fails",
			control: Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{
				{Kind: EvidenceCheck, Check: "mgmt_single_path"},
				{Kind: EvidencePolicy, Rule: "absent"},
			}},
			findings: []FindingRef{{ID: "health:mgmt_single_path|n", Check: "mgmt_single_path", Severity: "warning"}},
			policy:   PolicyState{Configured: true},
			want:     StatusFail,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := Evaluate(testProfile(tc.control), Inputs{
				Now: testNow, ProductVersion: "test",
				Findings: tc.findings, Posture: tc.posture, PostureOK: tc.postOK, Policy: tc.policy,
			})
			got := controlByID(t, rep, tc.control.ID)
			if got.Stat != tc.want {
				t.Errorf("control %s status = %q, want %q (evidence: %+v)", tc.control.ID, got.Stat, tc.want, got.Evidence)
			}
			if tc.want != StatusPass && got.Stat.IsPassing() {
				t.Errorf("control %s reported %q, which IsPassing() accepted as a pass", tc.control.ID, got.Stat)
			}
			for _, want := range tc.wantEvidenceNamed {
				if !slices.Contains(got.EvidenceKeys(), want) {
					t.Errorf("control %s evidence %v does not name %q", tc.control.ID, got.EvidenceKeys(), want)
				}
			}
			for _, want := range tc.wantFailingNamed {
				if !slices.Contains(got.FailingEvidenceKeys(), want) {
					t.Errorf("control %s failing evidence %v does not name %q", tc.control.ID, got.FailingEvidenceKeys(), want)
				}
			}
		})
	}
}

// TestEvaluate_UnmappedControlIsNeverPassing is AC2's standing assertion at
// the model, independent of any evidence the daemon happens to have: no
// combination of inputs can make a control with no mapping pass.
//
// BREAK IT TO SEE IT FIRE: in evaluateControl, return StatusPass instead of
// StatusUnmapped for the len(c.Evidence) == 0 branch.
func TestEvaluate_UnmappedControlIsNeverPassing(t *testing.T) {
	unmapped := Control{ID: "U1", Title: "t", Statement: "s", UnmappedReason: "vnprox observes none of this"}

	inputSets := []Inputs{
		{Now: testNow},
		{Now: testNow, PostureOK: true, Posture: posture.Posture{Overall: 100, Factors: []posture.Factor{
			{Name: posture.FactorSegmentation, ScorePct: 100, Evaluated: true},
		}}},
		{Now: testNow, Policy: PolicyState{Configured: true, Rules: []PolicyRuleRef{{ID: "r", MatchCount: 99}}}},
		{Now: testNow, AsOf: testNow.Add(-time.Hour)},
		{Now: testNow, Findings: []FindingRef{{ID: "health:x|y", Check: "x", Severity: "error"}}},
	}
	for i, in := range inputSets {
		rep := Evaluate(testProfile(unmapped), in)
		got := controlByID(t, rep, "U1")
		if got.Stat != StatusUnmapped {
			t.Errorf("input set %d: unmapped control reported %q, want %q", i, got.Stat, StatusUnmapped)
		}
		if got.Stat.IsPassing() {
			t.Errorf("input set %d: unmapped control reported a passing status (%q)", i, got.Stat)
		}
		if rep.Summary.Pass != 0 {
			t.Errorf("input set %d: summary counted %d passing controls; the only control is unmapped", i, rep.Summary.Pass)
		}
	}
}

func TestStatus_IsPassing(t *testing.T) {
	for _, st := range AllStatuses {
		want := st == StatusPass
		if st.IsPassing() != want {
			t.Errorf("Status(%q).IsPassing() = %v, want %v", st, st.IsPassing(), want)
		}
	}
	if StatusUnmapped.IsPassing() {
		t.Error("StatusUnmapped.IsPassing() is true; the whole safety property rests on it being false")
	}
	if StatusNotEvaluated.IsPassing() {
		t.Error("StatusNotEvaluated.IsPassing() is true; absence of evidence is not evidence of compliance")
	}
}

// TestEvaluate_UnmappedCheckIsReportedAndDegradesNothing is AC6: adding a
// check to the codebase without mapping it must not silently degrade any
// control, and the unmapped-check list must be reported rather than ignored.
//
// BREAK IT TO SEE IT FIRE: make unmappedChecks return nil.
func TestEvaluate_UnmappedCheckIsReportedAndDegradesNothing(t *testing.T) {
	profile := testProfile(
		checkControl("C1", "mgmt_single_path"),
		Control{ID: "C2", Title: "t", Statement: "s", UnmappedReason: "not observed"},
	)
	base := Inputs{Now: testNow, ProductVersion: "test", KnownChecks: []string{"mgmt_single_path"},
		CheckUniverse: "test catalog"}

	before := Evaluate(profile, base)
	if len(before.UnmappedChecks) != 0 {
		t.Fatalf("baseline reported unmapped checks %v; the catalog holds only the mapped one", before.UnmappedChecks)
	}

	// Someone adds a check to the codebase. The catalog learns about it
	// (internal/findings' catalog guard makes that mandatory); the profile
	// does not.
	after := base
	after.KnownChecks = []string{"mgmt_single_path", "newly_added_check"}
	got := Evaluate(profile, after)

	if !slices.Contains(got.UnmappedChecks, "newly_added_check") {
		t.Errorf("unmapped checks %v do not report the newly added check", got.UnmappedChecks)
	}
	if got.Summary != before.Summary {
		t.Errorf("adding an unmapped check changed the control summary: %+v -> %+v", before.Summary, got.Summary)
	}
	for i := range got.Controls {
		if got.Controls[i].Stat != before.Controls[i].Stat {
			t.Errorf("control %s changed status (%q -> %q) because an unmapped check was added",
				got.Controls[i].ID, before.Controls[i].Stat, got.Controls[i].Stat)
		}
	}
	if !containsSubstring(got.Caveats, "mapped by no control") {
		t.Errorf("report caveats %v do not mention the unmapped checks", got.Caveats)
	}

	// A check that fires but is in no catalog also cannot hide.
	firing := base
	firing.Findings = []FindingRef{{ID: "health:surprise_check|x", Check: "surprise_check", Severity: "error"}}
	if !slices.Contains(Evaluate(profile, firing).UnmappedChecks, "surprise_check") {
		t.Error("a check observed in the evidence but absent from both the catalog and the profile was not reported as unmapped")
	}
}

// TestEvaluate_HistoricalReportIsCaveatedAndStricter asserts the historical
// path states what it could not establish, and grades an ungraded finding as
// failing rather than clean.
func TestEvaluate_HistoricalReportIsCaveatedAndStricter(t *testing.T) {
	profile := testProfile(
		checkControl("C1", "bond_slave_down"),
		Control{ID: "C2", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Tag: "change-control"}}},
	)
	rep := Evaluate(profile, Inputs{
		Now: testNow, AsOf: testNow.Add(-24 * time.Hour), ProductVersion: "test",
		Findings: []FindingRef{{ID: "health:bond_slave_down|b", Check: "bond_slave_down"}},
		Policy:   PolicyState{Configured: true, Rules: []PolicyRuleRef{{ID: "r", Tags: []string{"change-control"}, MatchCount: 9}}},
	})

	if rep.AsOf != testNow.Add(-24*time.Hour).Unix() {
		t.Errorf("report AsOf = %d, want %d", rep.AsOf, testNow.Add(-24*time.Hour).Unix())
	}
	if got := controlByID(t, rep, "C1").Stat; got != StatusFail {
		t.Errorf("C1 = %q, want %q: an open finding whose severity history does not retain must fail, not pass", got, StatusFail)
	}
	if got := controlByID(t, rep, "C2").Stat; got != StatusNotEvaluated {
		t.Errorf("C2 = %q, want %q: policy bookkeeping is current-state only", got, StatusNotEvaluated)
	}
	if !containsSubstring(rep.Caveats, "reconstructed from retained finding-transition history") {
		t.Errorf("historical report caveats %v do not state that the evidence was reconstructed", rep.Caveats)
	}
	if !containsSubstring(rep.Caveats, "Policy evidence is not evaluated in a historical report") {
		t.Errorf("historical report caveats %v do not state that policy evidence was not evaluated", rep.Caveats)
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
