// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// latmeshFixtureRow mirrors testdata/latmesh/*.json's per-tick record
// shape — this task's card-required "synthetic latency/loss series ...
// driving hysteresis-finding tests without real probes" fixture.
type latmeshFixtureRow struct {
	At      int64   `json:"at"`
	RttMs   float64 `json:"rttMs"`
	LossPct float64 `json:"lossPct"`
}

func loadLatmeshFixture(t *testing.T, name string) []latmeshFixtureRow {
	t.Helper()
	data, err := os.ReadFile("../../testdata/latmesh/" + name)
	if err != nil {
		t.Fatalf("reading testdata/latmesh/%s: %v", name, err)
	}
	var out []latmeshFixtureRow
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parsing testdata/latmesh/%s: %v", name, err)
	}
	return out
}

// stepLatMeshProvider replays one fixture row per logical "cycle", advanced
// explicitly by the test loop's call to advance() rather than by call
// count: Engine.Findings() reads LatMeshHeatmap twice per cycle (once from
// checkPathLatencyDegraded, once from checkPathLoss), so advancing on every
// call would silently skip every other fixture row — advance() makes the
// step boundary explicit and independent of how many checks happen to
// consume the same provider within one Engine cycle.
type stepLatMeshProvider struct {
	fromNode, toNode string
	fabric           latmesh.Fabric
	rows             []latmeshFixtureRow
	i                int
}

func (p *stepLatMeshProvider) LatMeshHeatmap() ([]latmesh.LinkHeat, error) {
	if len(p.rows) == 0 {
		return nil, nil
	}
	idx := p.i
	if idx >= len(p.rows) {
		idx = len(p.rows) - 1
	}
	row := p.rows[idx]
	linkID := latmesh.ComputeLinkID(p.fabric, "", p.fromNode, p.toNode)
	return []latmesh.LinkHeat{{
		LinkID: linkID, Fabric: p.fabric, FromNode: p.fromNode, ToNode: p.toNode,
		At: row.At, RttMs: row.RttMs, LossPct: row.LossPct,
		RollingRttMs: row.RttMs, RollingLossPct: row.LossPct, SampleCount: 1,
	}}, nil
}

func (p *stepLatMeshProvider) advance() {
	if p.i < len(p.rows)-1 {
		p.i++
	}
}

// TestPathLatencyDegraded_DegradingFixture: AC3 — a synthetic degrading-
// link fixture (testdata/latmesh/degrading.json: 3 clean cycles, 4
// above-threshold cycles, then 2 clean cycles again) crosses the RTT
// threshold and holds -> exactly one path_latency_degraded finding after
// the hysteresis window (not one per raw sample), clearing again after the
// symmetric fall window — see degrading.json's own layout comment in
// internal/latmesh/service_test.go-adjacent fixtures for the exact
// breach-flag sequence this asserts against (F,F,F,T,T,T,T,F,F with
// rise=3/fall=2: activates at cycle 6, clears at cycle 9).
func TestPathLatencyDegraded_DegradingFixture(t *testing.T) {
	rows := loadLatmeshFixture(t, "degrading.json")
	prov := &stepLatMeshProvider{fromNode: "pve1", toNode: "pve2", fabric: latmesh.FabricCorosync, rows: rows}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1", "pve2"), LatMesh: prov})

	var seenAt = map[int]int{} // cycle index -> len(findings)
	for cycle := 1; cycle <= len(rows); cycle++ {
		found := findByCheck(t, eng.Findings(), findings.CheckPathLatencyDegraded)
		seenAt[cycle] = len(found)
		prov.advance()
	}

	for cycle := 1; cycle <= 5; cycle++ {
		if seenAt[cycle] != 0 {
			t.Errorf("cycle %d: got %d path_latency_degraded findings, want 0 (still within rise window)", cycle, seenAt[cycle])
		}
	}
	for _, cycle := range []int{6, 7} {
		if seenAt[cycle] != 1 {
			t.Errorf("cycle %d: got %d path_latency_degraded findings, want 1 (active)", cycle, seenAt[cycle])
		}
	}
	if seenAt[9] != 0 {
		t.Errorf("cycle 9: got %d path_latency_degraded findings, want 0 (cleared after the fall window)", seenAt[9])
	}
}

// TestPathLoss_LossyFixture: the same hysteresis shape as
// TestPathLatencyDegraded_DegradingFixture, driven by testdata/latmesh/
// lossy.json's loss% series instead of RTT.
func TestPathLoss_LossyFixture(t *testing.T) {
	rows := loadLatmeshFixture(t, "lossy.json")
	prov := &stepLatMeshProvider{fromNode: "pve1", toNode: "pve3", fabric: latmesh.FabricGuest, rows: rows}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1", "pve3"), LatMesh: prov})

	var seenAt = map[int]int{}
	for cycle := 1; cycle <= len(rows); cycle++ {
		found := findByCheck(t, eng.Findings(), findings.CheckPathLoss)
		seenAt[cycle] = len(found)
		prov.advance()
	}

	for cycle := 1; cycle <= 5; cycle++ {
		if seenAt[cycle] != 0 {
			t.Errorf("cycle %d: got %d path_loss findings, want 0", cycle, seenAt[cycle])
		}
	}
	for _, cycle := range []int{6, 7} {
		if seenAt[cycle] != 1 {
			t.Errorf("cycle %d: got %d path_loss findings, want 1", cycle, seenAt[cycle])
		}
	}
	if seenAt[9] != 0 {
		t.Errorf("cycle 9: got %d path_loss findings, want 0 (cleared)", seenAt[9])
	}

	// Sanity on the finding's own shape once more, using a fresh provider
	// pinned at the breaching row so we can inspect a live finding.
	pinned := &stepLatMeshProvider{fromNode: "pve1", toNode: "pve3", fabric: latmesh.FabricGuest, rows: []latmeshFixtureRow{rows[3], rows[3], rows[3]}}
	eng2 := findings.New(findings.Config{Graph: newGraphWithNodes("pve1", "pve3"), LatMesh: pinned})
	eng2.Findings()
	eng2.Findings()
	found := findByCheck(t, eng2.Findings(), findings.CheckPathLoss)
	if len(found) != 1 {
		t.Fatalf("got %d path_loss findings, want 1", len(found))
	}
	f := found[0]
	if f.Fixable {
		t.Error("path_loss should never be fixable")
	}
	if f.DocsLink == "" {
		t.Error("path_loss must carry a DocsLink")
	}
	if len(f.Nodes) != 2 {
		t.Errorf("Nodes = %v, want [pve1 pve3]", f.Nodes)
	}
}

// TestPathLatencyDegraded_CleanFixture_NeverFires: testdata/latmesh/
// clean.json never crosses the threshold, so it must never produce a
// finding across its full cycle count.
func TestPathLatencyDegraded_CleanFixture_NeverFires(t *testing.T) {
	rows := loadLatmeshFixture(t, "clean.json")
	prov := &stepLatMeshProvider{fromNode: "pve1", toNode: "pve2", fabric: latmesh.FabricCorosync, rows: rows}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1", "pve2"), LatMesh: prov})

	for cycle := 0; cycle < len(rows); cycle++ {
		found := findByCheck(t, eng.Findings(), findings.CheckPathLatencyDegraded)
		if len(found) != 0 {
			t.Fatalf("cycle %d: got %d findings on a clean fixture, want 0: %+v", cycle, len(found), found)
		}
		prov.advance()
	}
}

// TestPathLatencyDegraded_NilProvider: a nil LatMesh Config field produces
// no findings and no panic — the same optional-Config-field degradation
// every other producer in this package follows.
func TestPathLatencyDegraded_NilProvider(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1")})
	if got := findByCheck(t, eng.Findings(), findings.CheckPathLatencyDegraded); len(got) != 0 {
		t.Fatalf("got %d findings with nil LatMesh, want 0", len(got))
	}
	if got := findByCheck(t, eng.Findings(), findings.CheckPathLoss); len(got) != 0 {
		t.Fatalf("got %d findings with nil LatMesh, want 0", len(got))
	}
}
