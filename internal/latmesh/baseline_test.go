package latmesh_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// fixtureReading mirrors testdata/latmesh/*.json's per-tick record shape.
type fixtureReading struct {
	At      int64   `json:"at"`
	RttMs   float64 `json:"rttMs"`
	LossPct float64 `json:"lossPct"`
}

func loadLatmeshFixture(t *testing.T, name string) []fixtureReading {
	t.Helper()
	data, err := os.ReadFile("../../testdata/latmesh/" + name)
	if err != nil {
		t.Fatalf("reading testdata/latmesh/%s: %v", name, err)
	}
	var out []fixtureReading
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parsing testdata/latmesh/%s: %v", name, err)
	}
	return out
}

func readingsFromFixture(rows []fixtureReading) []latmesh.Reading {
	out := make([]latmesh.Reading, len(rows))
	for i, r := range rows {
		out[i] = latmesh.Reading{RttMs: r.RttMs, LossPct: r.LossPct}
	}
	return out
}

// TestBaseline_Golden: AC5 — a fixed synthetic series (testdata/latmesh/
// baseline.json: rttMs sorted [10,10,11,11,12,12,12,13,13,50], one sample
// with 20% loss, the rest 0%) produces stable, hand-verifiable p50/p95/
// mean-loss values under Baseline's documented nearest-rank percentile
// method (n=10: p50 idx=ceil(5)=5th smallest=12, p95 idx=ceil(9.5)=10th
// smallest=50; mean loss = 20/10 = 2.0).
func TestBaseline_Golden(t *testing.T) {
	rows := loadLatmeshFixture(t, "baseline.json")
	readings := readingsFromFixture(rows)

	p50, p95, lossPct, ok := latmesh.Baseline(readings)
	if !ok {
		t.Fatal("Baseline reported ok=false for a non-empty series")
	}
	if p50 != 12 {
		t.Errorf("p50 = %v, want 12", p50)
	}
	if p95 != 50 {
		t.Errorf("p95 = %v, want 50", p95)
	}
	if lossPct != 2.0 {
		t.Errorf("lossPct = %v, want 2.0", lossPct)
	}
}

func TestBaseline_Empty(t *testing.T) {
	p50, p95, lossPct, ok := latmesh.Baseline(nil)
	if ok {
		t.Fatal("Baseline reported ok=true for an empty series")
	}
	if p50 != 0 || p95 != 0 || lossPct != 0 {
		t.Errorf("Baseline(nil) = (%v,%v,%v), want all zero", p50, p95, lossPct)
	}
}

// TestBaseline_Stable: calling Baseline twice against the same input
// (independent copies) produces byte-identical results — the "stable"
// half of AC5, and a guard against Baseline mutating its input slice
// (sort.Float64s operating on a shared backing array would corrupt a
// caller's own copy otherwise).
func TestBaseline_Stable(t *testing.T) {
	rows := loadLatmeshFixture(t, "baseline.json")
	first := readingsFromFixture(rows)
	second := readingsFromFixture(rows)

	p50a, p95a, lossA, _ := latmesh.Baseline(first)
	p50b, p95b, lossB, _ := latmesh.Baseline(second)
	if p50a != p50b || p95a != p95b || lossA != lossB {
		t.Fatalf("Baseline not stable across calls: (%v,%v,%v) vs (%v,%v,%v)", p50a, p95a, lossA, p50b, p95b, lossB)
	}
}
