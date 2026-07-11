package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/metrics"
)

// fakeMetrics is a MetricsProvider stand-in whose Live() result can be
// swapped between Findings() calls, simulating successive sample cycles
// without needing a real Sampler.
type fakeMetrics struct {
	byRef map[string]metrics.LiveMetric
}

func (m *fakeMetrics) Live(refs []string) []metrics.LiveMetric {
	out := make([]metrics.LiveMetric, 0, len(refs))
	for _, r := range refs {
		if lm, ok := m.byRef[r]; ok {
			out = append(out, lm)
		}
	}
	return out
}

const testNicRef = "physnic:pve1:eno1"

// TestErrorDropRate_Hysteresis: a sustained error-rate breach only fires
// after RiseCycles consecutive breaching samples (AC3), and only clears
// after FallCycles consecutive clean samples.
func TestErrorDropRate_Hysteresis(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkPhysNicUp(g, "pve1", "eno1", true)

	fm := &fakeMetrics{byRef: map[string]metrics.LiveMetric{
		testNicRef: {Ref: testNicRef, Rates: metrics.Rates{RxErrsPerSec: 5}},
	}}
	eng := findings.New(findings.Config{Graph: g, Metrics: fm})

	// DefaultThresholds.RiseCycles == 3: two breaching cycles must not fire yet.
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckErrorDropRate)
	if len(found) != 0 {
		t.Fatalf("error_drop_rate fired after only 2 breaching cycles (want RiseCycles=3): %+v", found)
	}

	found = findByCheck(t, eng.Findings(), findings.CheckErrorDropRate)
	if len(found) != 1 {
		t.Fatalf("got %d error_drop_rate findings after 3 breaching cycles, want 1", len(found))
	}
	f := found[0]
	if f.Severity != findings.SeverityError {
		t.Errorf("severity = %q, want %q for an error-rate breach", f.Severity, findings.SeverityError)
	}
	if f.Fixable {
		t.Error("error_drop_rate should not be fixable")
	}
	if !strings.Contains(f.Detail, testNicRef) {
		t.Errorf("detail = %q, want mention of %s", f.Detail, testNicRef)
	}

	// A single clean sample must not clear it yet (FallCycles == 2).
	fm.byRef[testNicRef] = metrics.LiveMetric{Ref: testNicRef, Rates: metrics.Rates{}}
	stillActive := findByCheck(t, eng.Findings(), findings.CheckErrorDropRate)
	if len(stillActive) != 1 {
		t.Fatalf("finding cleared after a single clean sample, want it to persist one more cycle")
	}
	cleared := findByCheck(t, eng.Findings(), findings.CheckErrorDropRate)
	if len(cleared) != 0 {
		t.Fatalf("finding did not clear after 2 consecutive clean samples: %+v", cleared)
	}
}

// TestErrorDropRate_NoisyBelowThreshold_NeverFires: rates that hover under
// threshold, even with sample-to-sample jitter, never produce a finding.
func TestErrorDropRate_NoisyBelowThreshold_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkPhysNicUp(g, "pve1", "eno1", true)

	fm := &fakeMetrics{byRef: map[string]metrics.LiveMetric{}}
	eng := findings.New(findings.Config{Graph: g, Metrics: fm})

	jitter := []float64{0.2, 0.9, 0.1, 0.95, 0.3}
	for i, rate := range jitter {
		fm.byRef[testNicRef] = metrics.LiveMetric{Ref: testNicRef, Rates: metrics.Rates{RxDropPerSec: rate}}
		if found := findByCheck(t, eng.Findings(), findings.CheckErrorDropRate); len(found) != 0 {
			t.Fatalf("cycle %d: sub-threshold jitter (rate=%.2f) fired a finding: %+v", i, rate, found)
		}
	}
}

// TestErrorDropRate_DropOnly_Warning: a drop-only breach (no errors) is
// reported at warning, not error, severity.
func TestErrorDropRate_DropOnly_Warning(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkPhysNicUp(g, "pve1", "eno1", true)

	fm := &fakeMetrics{byRef: map[string]metrics.LiveMetric{
		testNicRef: {Ref: testNicRef, Rates: metrics.Rates{RxDropPerSec: 10}},
	}}
	eng := findings.New(findings.Config{Graph: g, Metrics: fm})
	eng.Findings()
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckErrorDropRate)
	if len(found) != 1 {
		t.Fatalf("got %d findings, want 1", len(found))
	}
	if found[0].Severity != findings.SeverityWarning {
		t.Errorf("severity = %q, want %q for a drop-only breach", found[0].Severity, findings.SeverityWarning)
	}
}
