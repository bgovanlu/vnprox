package perfbudget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validBudget is the shape every table case below mutates one field of.
func validBudget() Budget {
	return Budget{
		ID:          "example.thing_ms",
		Title:       "an example",
		Site:        "internal/example/example_test.go",
		Metric:      "wall clock of the example",
		Unit:        "ms",
		Direction:   Max,
		Scaling:     Calibrated,
		Enforcement: Gate,
		Why:         "measured on the reference host on 2026-08-12, twice",
		Limit:       100,
		Samples:     5,
	}
}

func validFile(budgets ...Budget) File {
	if len(budgets) == 0 {
		budgets = []Budget{validBudget()}
	}
	return File{
		Comment:       "test fixture",
		ReferenceHost: ReferenceHost{Name: "test host", CPU: "test", Memory: "1 GB", OS: "test", Measured: "2026-08-12", Notes: "n/a", Cores: 32},
		Calibration:   Calibration{Workload: CalibrationWorkload, Notes: "n/a", ReferenceNS: 42e6, Samples: 5, MaxFactor: 8},
		Budgets:       budgets,
	}
}

// TestValidate_RejectsBudgetsThatCouldNotBeGatedHonestly covers the two
// structural rules T-2506 exists to make unforgettable, plus the ordinary
// completeness checks. Every case is one mutation away from a valid file, so a
// failure names exactly which rule stopped firing.
func TestValidate_RejectsBudgetsThatCouldNotBeGatedHonestly(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*File)
		wantErr string
	}{
		{
			name:    "an absolute budget may not gate",
			mutate:  func(f *File) { f.Budgets[0].Scaling = Absolute },
			wantErr: "an absolute budget may not gate",
		},
		{
			name:    "an absolute budget may be report-only",
			mutate:  func(f *File) { f.Budgets[0].Scaling = Absolute; f.Budgets[0].Enforcement = ReportOnly },
			wantErr: "",
		},
		{
			name:    "a gate needs at least three samples",
			mutate:  func(f *File) { f.Budgets[0].Samples = 2 },
			wantErr: "needs at least 3 samples",
		},
		{
			name:    "a single-sample budget may be report-only",
			mutate:  func(f *File) { f.Budgets[0].Samples = 1; f.Budgets[0].Enforcement = ReportOnly },
			wantErr: "",
		},
		{
			name:    "a browser-side budget cannot use the Go calibration kernel",
			mutate:  func(f *File) { f.Budgets[0].Site = "web/e2e/scale.spec.ts" },
			wantErr: "cannot run the Go calibration kernel",
		},
		{
			name:    "a browser-side budget may use the cores ladder",
			mutate:  func(f *File) { f.Budgets[0].Site = "web/e2e/scale.spec.ts"; f.Budgets[0].Scaling = Cores },
			wantErr: "",
		},
		{
			name:    "duplicate ids",
			mutate:  func(f *File) { f.Budgets = append(f.Budgets, validBudget()) },
			wantErr: "duplicate id",
		},
		{
			name:    "no site",
			mutate:  func(f *File) { f.Budgets[0].Site = "" },
			wantErr: "no site",
		},
		{
			name:    "an unexplained number",
			mutate:  func(f *File) { f.Budgets[0].Why = "because" },
			wantErr: "at least 20 characters",
		},
		{
			name:    "a reference host with no name",
			mutate:  func(f *File) { f.ReferenceHost.Name = "" },
			wantErr: "is not a budget",
		},
		{
			name:    "an unknown scaling mode",
			mutate:  func(f *File) { f.Budgets[0].Scaling = "fast-machine" },
			wantErr: "scaling must be",
		},
		{
			name:    "a negative limit",
			mutate:  func(f *File) { f.Budgets[0].Limit = -1 },
			wantErr: "limit must be positive",
		},
		{
			name:    "no calibration reference",
			mutate:  func(f *File) { f.Calibration.ReferenceNS = 0 },
			wantErr: "reference_ns must be positive",
		},
		{
			name:    "a max_factor below 1 would let normalisation tighten a budget",
			mutate:  func(f *File) { f.Calibration.MaxFactor = 0.5 },
			wantErr: "max_factor must be at least 1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validFile()
			tc.mutate(&f)
			err := f.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tc.wantErr == "":
			case err == nil:
				t.Fatalf("want an error containing %q, got none", tc.wantErr)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("want an error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLoad_RejectsAnUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budgets.json")
	// "limits" is a plausible misspelling of "limit"; without
	// DisallowUnknownFields the budget would load with limit 0 and every
	// measurement would fail against it for a reason nobody could see.
	body := `{"comment":"x","reference_host":{"name":"h","cores":1},"calibration":{"workload":"w","reference_ns":1,"samples":1,"max_factor":1},"budgets":[{"id":"a","limits":5}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "limits") {
		t.Fatalf("want an unknown-field error naming \"limits\", got %v", err)
	}
}

func TestByID_NamesTheAlternatives(t *testing.T) {
	f := validFile()
	_, err := f.ByID("nope")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "example.thing_ms") {
		t.Fatalf("the error should list the budgets that do exist, got %v", err)
	}
}

// TestRepoBudgetsAreValid is the shipped file, checked on every `make check`.
func TestRepoBudgetsAreValid(t *testing.T) {
	f, err := LoadRepo()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(f.Budgets) == 0 {
		t.Fatal("no budgets")
	}
	// Both measurement sites the card names must still be measuring something.
	for _, site := range []string{"internal/collect/sim_bench_test.go", "web/e2e/scale.spec.ts"} {
		if len(f.ForSite(site)) == 0 {
			t.Errorf("no budget names %s as its site; T-2506's requirement is that both measurement sites share one source", site)
		}
	}
	// A site that names a file which does not exist is a budget nothing can
	// measure.
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, b := range f.Budgets {
		if _, err := os.Stat(filepath.Join(root, b.Site)); err != nil {
			t.Errorf("%s: site %s does not exist: %v", b.ID, b.Site, err)
		}
	}
}
