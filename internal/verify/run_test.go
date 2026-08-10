package verify

// run_test.go covers AC3 (a skip is never a pass) and AC6 (an unknown --only
// id is an error naming it), plus Report.Validate's structural guarantees.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestARunOfNothingButSkipsIsNotASuccess is AC3.
//
// This is the failure mode the whole card is written against. A cluster with
// no probes wired up produces a report with zero failures, and a `!Failed()`
// exit code would call that success — so a CI job on a machine with no
// cluster attached would go green forever, and the hardware-validated count
// in the matrix would be built on it. Success has to mean something was
// observed.
func TestARunOfNothingButSkipsIsNotASuccess(t *testing.T) {
	bare := Deps{
		Now:   func() time.Time { return fixtureNow() },
		Wait:  func(context.Context, time.Duration) error { return nil },
		Nodes: fixtureNodes(),
	}
	report, err := Run(context.Background(), Options{Suite: SuiteHardware, Version: "test", Logger: discardLog()}, bare)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.Summary.Passed != 0 {
		t.Fatalf("summary says %d passed on a run with no probes at all", report.Summary.Passed)
	}
	if report.Summary.Failed != 0 {
		t.Fatalf("summary says %d failed; this fixture is supposed to produce only skips", report.Summary.Failed)
	}
	if report.Summary.Skipped != len(report.Results) {
		t.Fatalf("%d of %d results skipped; expected all of them", report.Summary.Skipped, len(report.Results))
	}
	if report.OK() {
		t.Error("a run in which every check skipped reported OK, so the command would exit 0 having validated nothing (AC3)")
	}
	if !strings.Contains(report.Render(), "0 passed") {
		t.Errorf("the rendered summary does not say `0 passed`:\n%s", report.Render())
	}
	if !strings.Contains(report.Render(), "Nothing was validated") {
		t.Errorf("the rendered report reads as success despite validating nothing:\n%s", report.Render())
	}
}

// TestAFailingRunIsNotOK is the other half of the exit-code contract.
func TestAFailingRunIsNotOK(t *testing.T) {
	deps := healthyDeps()
	daemonOf(&deps).set("/captures", `{"items":[{"id":"c","sessions":[{"node":"pve1","iface":"vmbr0","state":"finished","packets":0,"bytes":0}]}]}`)

	report, err := Run(context.Background(), Options{Suite: SuiteHardware, Version: "test", Logger: discardLog()}, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.Failed == 0 {
		t.Fatal("the broken fixture produced no failures")
	}
	if report.OK() {
		t.Error("a run with failures reported OK")
	}
}

// TestUnknownOnlyIDIsAnErrorNamingIt is AC6.
//
// The failure this prevents is quiet: a typo'd id in a CI pipeline that
// selects nothing, runs nothing, and exits 0 — a gate that has been green for
// months because it has never run.
func TestUnknownOnlyIDIsAnErrorNamingIt(t *testing.T) {
	_, err := Run(context.Background(),
		Options{Only: []string{"lldp.neighbours_match_pve_interfaces"}, Version: "test", Logger: discardLog()},
		healthyDeps())
	if err == nil {
		t.Fatal("--only with an unknown id produced no error (AC6): a typo would silently select nothing")
	}

	var unknown *UnknownCheckError
	if !errors.As(err, &unknown) {
		t.Fatalf("the error is not an *UnknownCheckError: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "lldp.neighbours_match_pve_interfaces") {
		t.Errorf("the error does not name the unknown id: %v", err)
	}
	// The British spelling above is one letter from a real id; the suggestion
	// is what turns the error into a fix rather than a hunt.
	if !strings.Contains(err.Error(), "lldp.neighbors_match_pve_interfaces") {
		t.Errorf("the error does not suggest the near-miss id it could have meant: %v", err)
	}
}

// TestOnlySelectsAcrossSuites: an operator chasing one failure should not have
// to remember which suite it lives in.
func TestOnlySelectsAcrossSuites(t *testing.T) {
	report, err := Run(context.Background(),
		Options{Only: []string{"lldp.neighbors_match_pve_interfaces", "drift.node_vs_node"}, Version: "test", Logger: discardLog()},
		healthyDeps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("--only selected %d checks, want 2", len(report.Results))
	}
}

// TestSuiteFilterSelectsOnlyItsOwnChecks.
func TestSuiteFilterSelectsOnlyItsOwnChecks(t *testing.T) {
	for _, suite := range AllSuites {
		deps := healthyDeps()
		if suite == SuiteDestructive {
			deps = destructiveDeps()
		}
		report, err := Run(context.Background(), Options{Suite: suite, Version: "test", Logger: discardLog()}, deps)
		if err != nil {
			t.Fatalf("Run(%s): %v", suite, err)
		}
		if len(report.Results) == 0 {
			t.Fatalf("suite %s selected nothing", suite)
		}
		for _, res := range report.Results {
			if res.Suite != suite {
				t.Errorf("suite %s ran %s, which belongs to %s", suite, res.ID, res.Suite)
			}
		}
	}
}

// TestUnknownSuiteIsAnError.
func TestUnknownSuiteIsAnError(t *testing.T) {
	_, err := Run(context.Background(), Options{Suite: "everything", Version: "test", Logger: discardLog()}, healthyDeps())
	if err == nil || !strings.Contains(err.Error(), "everything") {
		t.Fatalf("an unknown suite was accepted: %v", err)
	}
}

// TestRunRefusesToReturnAMalformedReport: Validate's guarantees are only real
// if the assembly path enforces them. A check that returns a pass with no
// evidence must break the run loudly, not produce an authoritative-looking
// line nobody reads twice.
func TestRunRefusesToReturnAMalformedReport(t *testing.T) {
	rep := Report{
		ReportVersion: CurrentReportVersion,
		GeneratedAt:   fixtureNow(),
		Suite:         SuiteHardware,
		Environment:   validEnvironment(),
		Results: []Result{{
			ID: "x.y", MatrixRow: 1, Area: "a", Suite: SuiteHardware,
			Precondition: "p", Status: StatusPass, Detail: "d",
		}},
	}
	rep.Summary = Summarize(rep.Results)
	if err := rep.Validate(); err == nil {
		t.Fatal("Report.Validate accepted a pass with no evidence")
	} else if !strings.Contains(err.Error(), "opinion") {
		t.Errorf("the error does not explain what is wrong with an evidence-free verdict: %v", err)
	}
}

func validEnvironment() Environment {
	return Environment{
		VnproxVersion: "3.0.4",
		PVEVersion:    "pve-manager/9.2.4",
		Kernel:        "6.12.0-1-pve",
		PVEEndpoint:   "https://pve1:8006",
		Nodes:         []string{"pve1"},
	}
}

// TestReportValidate is the table of everything a malformed report can be.
func TestReportValidate(t *testing.T) {
	good := func() Report {
		rep := Report{
			ReportVersion: CurrentReportVersion,
			GeneratedAt:   fixtureNow(),
			Suite:         SuiteHardware,
			Environment:   validEnvironment(),
			Results: []Result{{
				ID: "a.b", MatrixRow: 3, Area: "Area", Suite: SuiteHardware, Precondition: "some hardware",
				Status: StatusPass, Detail: "ok", Evidence: []Evidence{NewEvidence(SourceState, "ref", "out")},
			}},
		}
		rep.Summary = Summarize(rep.Results)
		return rep
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("the baseline report does not validate: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Report)
		wantErr string
	}{
		{
			name: "a skip with no reason",
			mutate: func(r *Report) {
				r.Results[0].Status = StatusSkip
				r.Results[0].Evidence = nil
				r.Summary = Summarize(r.Results)
			},
			wantErr: "skipped with no reason",
		},
		{
			name: "a fail with no evidence",
			mutate: func(r *Report) {
				r.Results[0].Status = StatusFail
				r.Results[0].Evidence = nil
				r.Summary = Summarize(r.Results)
			},
			wantErr: "no evidence",
		},
		{
			name:    "evidence with no output",
			mutate:  func(r *Report) { r.Results[0].Evidence = []Evidence{{Source: "s", Ref: "r"}} },
			wantErr: "carries no output",
		},
		{
			name:    "evidence naming nothing it observed",
			mutate:  func(r *Report) { r.Results[0].Evidence = []Evidence{{Source: "s", Output: "o"}} },
			wantErr: "names nothing it observed",
		},
		{
			name:    "a check with no stated precondition",
			mutate:  func(r *Report) { r.Results[0].Precondition = "" },
			wantErr: "states no hardware precondition",
		},
		{
			name:    "a check naming no matrix row",
			mutate:  func(r *Report) { r.Results[0].MatrixRow = 0 },
			wantErr: "names no status-matrix.md row",
		},
		{
			name:    "an unknown status",
			mutate:  func(r *Report) { r.Results[0].Status = "probably-fine" },
			wantErr: "unknown status",
		},
		{
			name: "the same check reported twice",
			mutate: func(r *Report) {
				r.Results = append(r.Results, r.Results[0])
				r.Summary = Summarize(r.Results)
			},
			wantErr: "reported twice",
		},
		{
			// The one that matters for AC3: a summary claiming passes its own
			// results do not support would let a report lie about itself.
			name:    "a summary that disagrees with the results",
			mutate:  func(r *Report) { r.Summary.Passed = 42 },
			wantErr: "does not match the results",
		},
		{
			name:    "an environment that cannot say which PVE produced it",
			mutate:  func(r *Report) { r.Environment.PVEVersion = "" },
			wantErr: "pveVersion is empty",
		},
		{
			name:    "an environment with no kernel",
			mutate:  func(r *Report) { r.Environment.Kernel = "  " },
			wantErr: "kernel is empty",
		},
		{
			name:    "a mock run with no reason recorded",
			mutate:  func(r *Report) { r.Environment.Mock = true },
			wantErr: "no mockReason",
		},
		{
			name:    "a mock reason on a run that was not a mock",
			mutate:  func(r *Report) { r.Environment.MockReason = "leftover" },
			wantErr: "is not flagged as a mock",
		},
		{
			name:    "a report with no schema version",
			mutate:  func(r *Report) { r.ReportVersion = 0 },
			wantErr: "no reportVersion",
		},
		{
			name:    "a report naming no suite",
			mutate:  func(r *Report) { r.Suite = "" },
			wantErr: "unknown suite",
		},
		{
			name:    "a skip that also carries a skipReason on a pass",
			mutate:  func(r *Report) { r.Results[0].SkipReason = "why" },
			wantErr: "carries a skipReason",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := good()
			tt.mutate(&rep)
			err := rep.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate said %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestEnvironmentIsAttributableOnEveryRun: a report that cannot say which PVE,
// which kernel and which NICs produced it is not evidence.
func TestEnvironmentIsAttributableOnEveryRun(t *testing.T) {
	report, err := Run(context.Background(), Options{Suite: SuiteHardware, Version: "3.0.4", Logger: discardLog()}, healthyDeps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	env := report.Environment
	if env.VnproxVersion != "3.0.4" {
		t.Errorf("vnproxVersion = %q", env.VnproxVersion)
	}
	if env.Kernel != "6.12.0-1-pve" {
		t.Errorf("kernel = %q; the report cannot attribute a driver-specific result", env.Kernel)
	}
	if len(env.NICModels) == 0 {
		t.Error("no NIC models recorded, so an SR-IOV or LACP result cannot be read against the hardware that produced it")
	}
	if len(env.Nodes) != 2 {
		t.Errorf("nodes = %v", env.Nodes)
	}

	// And the honest-fallback half: with no host and no cluster, the fields
	// say "unknown" rather than going blank (which Validate would reject and
	// a reader would fill in optimistically).
	bare, err := Run(context.Background(), Options{Suite: SuiteHardware, Version: "", Logger: discardLog()},
		Deps{Now: func() time.Time { return fixtureNow() }, Wait: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatalf("Run(bare): %v", err)
	}
	for name, value := range map[string]string{
		"vnproxVersion": bare.Environment.VnproxVersion,
		"pveVersion":    bare.Environment.PVEVersion,
		"kernel":        bare.Environment.Kernel,
	} {
		if value != "unknown" {
			t.Errorf("with nothing to read, %s = %q, want \"unknown\"", name, value)
		}
	}
}
