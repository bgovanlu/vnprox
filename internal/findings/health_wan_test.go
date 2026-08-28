// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// stepWanProvider replays one row of a synthetic loss% series per logical
// "cycle", advanced explicitly by the test loop's call to advance() — the
// same shape stepLatMeshProvider (health_latmesh_test.go) uses, since
// Engine.Findings() calls WanHeatmap() exactly once per cycle (unlike
// LatMeshHeatmap, which the two split latmesh checks each call once).
type stepWanProvider struct {
	node, uplink, host string
	lossPct            []float64
	i                  int
}

func (p *stepWanProvider) WanHeatmap() ([]latmesh.LinkHeat, error) {
	if len(p.lossPct) == 0 {
		return nil, nil
	}
	idx := p.i
	if idx >= len(p.lossPct) {
		idx = len(p.lossPct) - 1
	}
	loss := p.lossPct[idx]
	linkID := latmesh.ComputeLinkID("wan", p.uplink, p.node, p.host)
	return []latmesh.LinkHeat{{
		LinkID: linkID, Fabric: "wan", FromNode: p.node, ToNode: p.host,
		At: int64(idx + 1), RttMs: 20, LossPct: loss,
		RollingRttMs: 20, RollingLossPct: loss, SampleCount: 1,
	}}, nil
}

func (p *stepWanProvider) advance() {
	if p.i < len(p.lossPct)-1 {
		p.i++
	}
}

// TestWanDegraded_DegradingSeries: AC1 — a target degrading past the loss
// threshold and holding fires exactly one wan_degraded finding after the
// hysteresis window (a single missed probe/breach cycle never fires it),
// clearing again after the symmetric fall window. Series: 3 clean cycles,
// 4 above-threshold cycles (loss 60%), 2 clean cycles again — the same
// 3-rise/2-fall shape TestPathLatencyDegraded_DegradingFixture drives.
func TestWanDegraded_DegradingSeries(t *testing.T) {
	series := []float64{0, 0, 0, 60, 60, 60, 60, 0, 0}
	prov := &stepWanProvider{node: "pve1", uplink: "vmbr0", host: "1.1.1.1", lossPct: series}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Wan: prov})

	seenAt := map[int]int{}
	for cycle := 1; cycle <= len(series); cycle++ {
		found := findByCheck(t, eng.Findings(), findings.CheckWanDegraded)
		seenAt[cycle] = len(found)
		prov.advance()
	}

	for cycle := 1; cycle <= 5; cycle++ {
		if seenAt[cycle] != 0 {
			t.Errorf("cycle %d: got %d wan_degraded findings, want 0 (still within rise window)", cycle, seenAt[cycle])
		}
	}
	for _, cycle := range []int{6, 7} {
		if seenAt[cycle] != 1 {
			t.Errorf("cycle %d: got %d wan_degraded findings, want 1 (active)", cycle, seenAt[cycle])
		}
	}
	if seenAt[9] != 0 {
		t.Errorf("cycle 9: got %d wan_degraded findings, want 0 (cleared after the fall window)", seenAt[9])
	}
}

// TestWanDegraded_CleanSeries_NeverFires: a series that never crosses the
// threshold must never produce a finding.
func TestWanDegraded_CleanSeries_NeverFires(t *testing.T) {
	series := []float64{0, 1, 2, 0, 3, 5, 0, 0, 1}
	prov := &stepWanProvider{node: "pve1", uplink: "vmbr0", host: "1.1.1.1", lossPct: series}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Wan: prov})

	for cycle := 0; cycle < len(series); cycle++ {
		found := findByCheck(t, eng.Findings(), findings.CheckWanDegraded)
		if len(found) != 0 {
			t.Fatalf("cycle %d: got %d findings on a clean series, want 0: %+v", cycle, len(found), found)
		}
		prov.advance()
	}
}

// TestWanDegraded_NilProvider: a nil Wan Config field produces no findings
// and no panic — the same optional-Config-field degradation every other
// producer in this package follows.
func TestWanDegraded_NilProvider(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1")})
	if got := findByCheck(t, eng.Findings(), findings.CheckWanDegraded); len(got) != 0 {
		t.Fatalf("got %d findings with nil Wan, want 0", len(got))
	}
}

// TestWanDegraded_MultiUplink_Independent: T-1405 AC2's shape at the
// findings layer — two uplinks on the same node, one degraded, produce
// independent findings keyed by their own LinkID (which encodes the
// uplink), never merged or confused with each other.
func TestWanDegraded_MultiUplink_Independent(t *testing.T) {
	prov := twoUplinkWanProvider{
		healthyLink: latmesh.LinkHeat{
			LinkID: latmesh.ComputeLinkID("wan", "vmbr0", "pve1", "1.1.1.1"),
			Fabric: "wan", FromNode: "pve1", ToNode: "1.1.1.1",
			RollingRttMs: 15, RollingLossPct: 0,
		},
		degradedLink: latmesh.LinkHeat{
			LinkID: latmesh.ComputeLinkID("wan", "vmbr1", "pve1", "8.8.8.8"),
			Fabric: "wan", FromNode: "pve1", ToNode: "8.8.8.8",
			RollingRttMs: 40, RollingLossPct: 55,
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Wan: prov})

	var found []findings.Finding
	for i := 0; i < 3; i++ { // wanRiseCycles
		found = findByCheck(t, eng.Findings(), findings.CheckWanDegraded)
	}
	if len(found) != 1 {
		t.Fatalf("got %d wan_degraded findings, want 1 (only the degraded uplink): %+v", len(found), found)
	}
	f := found[0]
	if f.Source != findings.SourceWan {
		t.Errorf("Source = %q, want %q", f.Source, findings.SourceWan)
	}
	if f.Fixable {
		t.Error("wan_degraded should never be fixable")
	}
	if f.DocsLink == "" {
		t.Error("wan_degraded must carry a DocsLink")
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("Nodes = %v, want [pve1]", f.Nodes)
	}
}

type twoUplinkWanProvider struct {
	healthyLink  latmesh.LinkHeat
	degradedLink latmesh.LinkHeat
}

func (p twoUplinkWanProvider) WanHeatmap() ([]latmesh.LinkHeat, error) {
	return []latmesh.LinkHeat{p.healthyLink, p.degradedLink}, nil
}
