package findings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/findings"
)

var errBaselineProvider = errors.New("baseline provider boom")

// fakeBaselineProvider is a minimal findings.BaselineProvider stand-in.
type fakeBaselineProvider struct {
	err       error
	anomalies []baseline.Anomaly
}

func (f fakeBaselineProvider) RecentAnomalies() ([]baseline.Anomaly, error) {
	return f.anomalies, f.err
}

// TestBaselineAnomalies_Rendering is T-1601 AC2's findings half: each anomaly
// class renders as a source "baseline" finding whose check matches the class
// and whose detail names the baseline value, observed value, and deviation —
// never a bare "anomalous" string. It also proves the 2-cycle firing
// hysteresis (nothing on the first observation).
func TestBaselineAnomalies_Rendering(t *testing.T) {
	win := baseline.Window{
		Start: 1704067200, // 2024-01-01T00:00Z
		End:   1705276800, // 2024-01-15T00:00Z
	}
	prov := fakeBaselineProvider{anomalies: []baseline.Anomaly{
		{Ref: "guest:pve1:100", Class: baseline.ClassNewPort, Subject: "tcp/6667", BaselineWindow: win, BaselineValue: 0, ObservedValue: 1, DeviationFactor: 1},
		{Ref: "guest:pve1:100", Class: baseline.ClassNewSubnet, Subject: "10.9.9.0/24", BaselineWindow: win, BaselineValue: 0, ObservedValue: 1, DeviationFactor: 1},
		{Ref: "guest:pve1:100", Class: baseline.ClassVolumeSpike, Subject: "2024-01-16T14:00Z", BaselineWindow: win, BaselineValue: 1520, ObservedValue: 45000, DeviationFactor: 29.6},
	}}
	eng := findings.New(findings.Config{Baseline: prov})

	// First cycle: hysteresis suppresses everything.
	if first := baselineFindings(eng.Findings()); len(first) != 0 {
		t.Fatalf("baseline findings fired on the very first observation (no debounce): %+v", first)
	}
	got := baselineFindings(eng.Findings())
	if len(got) != 3 {
		t.Fatalf("got %d baseline findings after 2 cycles, want 3: %+v", len(got), got)
	}

	byCheck := map[string]findings.Finding{}
	for _, f := range got {
		if f.Source != findings.SourceBaseline {
			t.Errorf("%s source = %q, want baseline", f.Check, f.Source)
		}
		if f.Severity != findings.SeverityWarning {
			t.Errorf("%s severity = %q, want warning", f.Check, f.Severity)
		}
		if f.Fixable {
			t.Errorf("%s must never be fixable", f.Check)
		}
		if f.DocsLink == "" {
			t.Errorf("%s must carry a DocsLink", f.Check)
		}
		if len(f.Refs) != 1 || f.Refs[0] != "guest:pve1:100" {
			t.Errorf("%s Refs = %v, want [guest:pve1:100]", f.Check, f.Refs)
		}
		byCheck[f.Check] = f
	}

	for _, want := range []string{findings.CheckNewPort, findings.CheckVolumeSpike, findings.CheckNewSubnet} {
		if _, ok := byCheck[want]; !ok {
			t.Errorf("missing baseline finding for check %q", want)
		}
	}

	// new_port detail must name the port, the never-observed baseline, and the
	// window.
	if d := byCheck[findings.CheckNewPort].Detail; !strings.Contains(d, "tcp/6667") ||
		!strings.Contains(d, "never observed") || !strings.Contains(d, "2024-01-01") {
		t.Errorf("new_port detail is not explainable: %q", d)
	}
	// new_subnet detail must name the subnet.
	if d := byCheck[findings.CheckNewSubnet].Detail; !strings.Contains(d, "10.9.9.0/24") || !strings.Contains(d, "never observed") {
		t.Errorf("new_subnet detail is not explainable: %q", d)
	}
	// volume_spike detail must name the observed value, the baseline value, and
	// the deviation factor — the "names its baseline and the deviation" contract.
	if d := byCheck[findings.CheckVolumeSpike].Detail; !strings.Contains(d, "45000") ||
		!strings.Contains(d, "1520") || !strings.Contains(d, "29.6×") {
		t.Errorf("volume_spike detail is not explainable: %q", d)
	}

	// Stable, content-derived id.
	if id := byCheck[findings.CheckNewPort].ID; id != "baseline:new_port|guest:pve1:100|tcp/6667" {
		t.Errorf("new_port id = %q, unexpected", id)
	}
}

// TestBaselineAnomalies_NilProviderSilent asserts the nil-safe degradation
// every optional producer has.
func TestBaselineAnomalies_NilProviderSilent(t *testing.T) {
	eng := findings.New(findings.Config{})
	eng.Findings()
	if got := baselineFindings(eng.Findings()); len(got) != 0 {
		t.Errorf("nil Baseline provider produced findings: %+v", got)
	}
}

// TestBaselineAnomalies_ErrorSilent asserts a provider error contributes zero
// findings rather than breaking the stream.
func TestBaselineAnomalies_ErrorSilent(t *testing.T) {
	prov := fakeBaselineProvider{err: errBaselineProvider}
	eng := findings.New(findings.Config{Baseline: prov})
	eng.Findings()
	if got := baselineFindings(eng.Findings()); len(got) != 0 {
		t.Errorf("erroring Baseline provider produced findings: %+v", got)
	}
}

func baselineFindings(fs []findings.Finding) []findings.Finding {
	var out []findings.Finding
	for _, f := range fs {
		if f.Source == findings.SourceBaseline {
			out = append(out, f)
		}
	}
	return out
}
