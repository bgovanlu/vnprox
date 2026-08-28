// SPDX-License-Identifier: Apache-2.0

package posture

import (
	"math"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// curatedInputs is T-1607 AC1's curated fixture: a snapshot with a known
// segmented/unsegmented guest split and a known exposed-port count, plus a
// known SPOF list, anomaly finding count, and open-drift count — every factor's
// value/contribution is arithmetic a reader can reproduce.
//
//	4 guests total, 2 nodes:
//	  - guest 100: applied microseg policy (marker rule)      → segmented
//	  - guest 101: one any-source inbound ACCEPT on :80       → 1 exposed port
//	  - guest 102: enabled ruleset, no rules                  → unsegmented
//	  - guest 103: no ruleset at all                          → unsegmented
//	SPOF: 1 warning (-8) + 1 info (-2)                        → failsim.Score 90
//	anomalies: 2 baseline findings (+ 1 non-baseline, ignored) over 4 guests
//	drift: 3 open findings over 2 nodes
func curatedInputs() Inputs {
	snap := newWorld().
		node("pve1").node("pve2").
		guest("pve1", "100").guest("pve1", "101").
		guest("pve2", "102").guest("pve2", "103").
		clusterFw().
		guestFw("pve1", "100", microsegRule(0, "10.0.0.0/24", "22")).
		guestFw("pve1", "101", anyInAccept(0, "80")).
		guestFw("pve2", "102").
		build()

	return Inputs{
		Snapshot: snap,
		SPOF:     SPOFInput{Entries: spofEntries(failsim.SeverityWarning, failsim.SeverityInfo)},
		Findings: []AnomalyFinding{
			{Source: "baseline", Check: "new_port"},
			{Source: "baseline", Check: "volume_spike"},
			{Source: "health", Check: "carrier_down"}, // not baseline — must be ignored
		},
		DriftOpenCount:  3,
		BaselineLearned: true,
		Now:             time.Unix(1_700_000_000, 0),
	}
}

func TestScore_GoldenCuratedFixture(t *testing.T) {
	p := Score(curatedInputs())

	if p.Qualified {
		t.Errorf("Qualified = true, want false (every factor evaluated in the golden fixture)")
	}
	if p.ComputedAt != 1_700_000_000 {
		t.Errorf("ComputedAt = %d, want 1700000000", p.ComputedAt)
	}
	if p.Overall != 70 {
		t.Errorf("Overall = %d, want 70", p.Overall)
	}

	// Every factor's value/scorePct/contribution matches expectation exactly
	// (AC1). Contributions are asserted with a small epsilon (float).
	want := []struct {
		name         string
		value        float64
		contribution float64
		scorePct     int
	}{
		{FactorSPOF, 2, 27.0, 90},
		{FactorSegmentation, 0.75, 6.25, 25},
		{FactorExposedPorts, 1, 18.0, 90},
		{FactorAnomalyRate, 0.5, 11.25, 75},
		{FactorDriftHygiene, 1.5, 7.7, 77},
	}
	for _, wf := range want {
		f, ok := factorByName(p, wf.name)
		if !ok {
			t.Errorf("factor %q missing", wf.name)
			continue
		}
		if !f.Evaluated {
			t.Errorf("factor %q Evaluated = false, want true", wf.name)
		}
		if f.ScorePct != wf.scorePct {
			t.Errorf("factor %q ScorePct = %d, want %d", wf.name, f.ScorePct, wf.scorePct)
		}
		if math.Abs(f.Value-wf.value) > 1e-9 {
			t.Errorf("factor %q Value = %v, want %v", wf.name, f.Value, wf.value)
		}
		if math.Abs(f.Contribution-wf.contribution) > 1e-6 {
			t.Errorf("factor %q Contribution = %v, want %v", wf.name, f.Contribution, wf.contribution)
		}
	}

	// Contributions of the evaluated factors sum (pre-rounding) to the overall.
	var sum float64
	for _, f := range p.Factors {
		sum += f.Contribution
	}
	if math.Abs(sum-70.2) > 1e-6 {
		t.Errorf("sum of contributions = %v, want 70.2", sum)
	}
}

// TestScore_NeverOpaque is T-1607 AC2's standing regression: Factors is never
// empty and every factor has a non-empty Name, across a range of inputs
// including a wholly-empty one.
func TestScore_NeverOpaque(t *testing.T) {
	cases := map[string]Inputs{
		"curated": curatedInputs(),
		"empty":   {Snapshot: newWorld().build(), Now: time.Unix(1, 0)},
		"cold-start-no-baseline": {
			Snapshot: newWorld().node("pve1").guest("pve1", "100").build(),
			Now:      time.Unix(1, 0),
		},
	}
	for name, in := range cases {
		p := Score(in)
		if len(p.Factors) != 5 {
			t.Errorf("%s: len(Factors) = %d, want 5", name, len(p.Factors))
		}
		seen := map[string]bool{}
		for _, f := range p.Factors {
			if f.Name == "" {
				t.Errorf("%s: a factor has an empty Name", name)
			}
			seen[f.Name] = true
		}
		for _, want := range []string{FactorSPOF, FactorSegmentation, FactorExposedPorts, FactorAnomalyRate, FactorDriftHygiene} {
			if !seen[want] {
				t.Errorf("%s: factor %q absent", name, want)
			}
		}
	}
}

// TestSegmentationFactor_OnlyAppliedCounts is T-1607 AC3: a guest whose only
// microseg trace would be a proposal (never staged/applied, so nothing in the
// inventory) counts unsegmented; a guest carrying an applied marker rule counts
// segmented.
func TestSegmentationFactor_OnlyAppliedCounts(t *testing.T) {
	snap := newWorld().
		node("pve1").
		guest("pve1", "100").guest("pve1", "101").
		clusterFw().
		// guest 100: an applied microseg policy — marker rule present.
		guestFw("pve1", "100", microsegRule(0, "10.0.0.0/24", "22")).
		// guest 101: a plain rule with no marker. A proposal that was only
		// dry-run would leave exactly this: no microseg trace in inventory.
		guestFw("pve1", "101", inventoryAccept()).
		build()

	total, segmented := segmentationCounts(snap)
	if total != 2 {
		t.Fatalf("total guests = %d, want 2", total)
	}
	if segmented != 1 {
		t.Errorf("segmented = %d, want 1 (only the applied policy counts)", segmented)
	}

	f := segmentationFactor(snap)
	if f.ScorePct != 50 { // 1 of 2 segmented
		t.Errorf("segmentation ScorePct = %d, want 50", f.ScorePct)
	}
	if math.Abs(f.Value-0.5) > 1e-9 { // unsegmented fraction
		t.Errorf("segmentation Value = %v, want 0.5", f.Value)
	}
}

// TestScore_ColdStartAnomalyNotEvaluated is the core honesty case: with no
// learned baselines, the anomaly-rate factor must be reported not-evaluated and
// EXCLUDED from the overall (never a phantom perfect 100), and the report must
// flag itself Qualified.
func TestScore_ColdStartAnomalyNotEvaluated(t *testing.T) {
	in := curatedInputs()
	in.BaselineLearned = false
	// Even with baseline anomaly findings present, cold start means we cannot
	// interpret them — the factor is not evaluated, not scored bad.
	p := Score(in)

	f, _ := factorByName(p, FactorAnomalyRate)
	if f.Evaluated {
		t.Errorf("anomaly factor Evaluated = true, want false (cold start)")
	}
	if f.ScorePct != NotEvaluatedScore {
		t.Errorf("anomaly ScorePct = %d, want NotEvaluatedScore(%d)", f.ScorePct, NotEvaluatedScore)
	}
	if f.Contribution != 0 {
		t.Errorf("anomaly Contribution = %v, want 0 (excluded from overall)", f.Contribution)
	}
	if f.Caveat == "" {
		t.Errorf("anomaly factor Caveat empty, want a not-evaluated explanation")
	}
	if !p.Qualified {
		t.Errorf("Qualified = false, want true (a dimension is unknown)")
	}

	// Overall must be the weighted mean of the OTHER four factors only —
	// weight 15 (anomaly) dropped from a 100 denominator ⇒ 85.
	// weightedSum = 30*90+25*25+20*90+10*77 = 2700+625+1800+770 = 5895; /85 = 69.35 → 69.
	if p.Overall != 69 {
		t.Errorf("Overall = %d, want 69 (anomaly excluded, renormalized over weight 85)", p.Overall)
	}
}

// TestScore_SPOFNotEvaluatedIsQualifiedCeiling covers the second honesty
// mechanism: failsim reporting NotEvaluated dimensions does not void the SPOF
// factor (it still counts), but flags the report Qualified and carries a caveat
// marking the score a ceiling.
func TestScore_SPOFNotEvaluatedIsQualifiedCeiling(t *testing.T) {
	in := curatedInputs()
	in.SPOF.NotEvaluated = []string{failsim.DimQuorum, failsim.DimCeph}
	p := Score(in)

	f, _ := factorByName(p, FactorSPOF)
	if !f.Evaluated {
		t.Errorf("SPOF factor Evaluated = false, want true (known SPOFs still count)")
	}
	if f.Caveat == "" {
		t.Errorf("SPOF factor Caveat empty, want a not-evaluated-dimensions ceiling note")
	}
	if f.Contribution == 0 {
		t.Errorf("SPOF Contribution = 0, want it still counted toward overall")
	}
	if !p.Qualified {
		t.Errorf("Qualified = false, want true (SPOF picture incomplete)")
	}
	// The overall is unchanged from the golden fixture (the SPOF score itself is
	// unchanged; only the caveat/qualified flag differ).
	if p.Overall != 70 {
		t.Errorf("Overall = %d, want 70", p.Overall)
	}
}

// TestScore_AllUnevaluatedIsHonestZero: when no dimension is assessable the
// overall is an honest 0/Qualified, never a default 100.
func TestScore_AllUnevaluatedIsHonestZero(t *testing.T) {
	// Force every factor unevaluated is not possible (segmentation/exposed/
	// drift always evaluate), so this checks the degenerate combine() guard
	// directly with a hand-built all-unevaluated factor set.
	overall, qualified := combine([]Factor{
		{Name: "a", Evaluated: false, Caveat: "x"},
		{Name: "b", Evaluated: false, Caveat: "y"},
	})
	if overall != 0 {
		t.Errorf("overall = %d, want 0", overall)
	}
	if !qualified {
		t.Errorf("qualified = false, want true")
	}
}

// inventoryAccept is a plain (non-marker) inbound ACCEPT from a specific source
// — a rule an operator wrote by hand, with no microseg trace.
func inventoryAccept() inventory.FwRule {
	return inventory.FwRule{
		Direction: "in", Action: "ACCEPT", Proto: "tcp",
		Source: "10.0.0.0/24", Dport: "443", Enabled: true, Pos: 0,
	}
}
