package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
)

// TestSampler_HotBondVsColdBond covers AC2's "fixture with one hot bond ->
// visibly distinct heat": bond0 (hot, near its aggregate capacity) must
// report a materially higher UtilizationPct than a cold, mostly-idle bond
// (bond1), given otherwise-identical link speeds.
func TestSampler_HotBondVsColdBond(t *testing.T) {
	links := []host.LinkState{
		{Kind: "physical", Name: "eno1", SpeedMbps: 1000, LinkUp: true},
		{Kind: "physical", Name: "eno2", SpeedMbps: 1000, LinkUp: true},
		{Kind: "physical", Name: "eno3", SpeedMbps: 1000, LinkUp: true},
		{Kind: "physical", Name: "eno4", SpeedMbps: 1000, LinkUp: true},
		{
			Kind: "bond", Name: "bond0", Members: []string{"eno1", "eno2"},
			Bond: &host.BondDetail{
				Mode: "802.3ad (4)", MIIStatus: "up",
				Slaves: []host.BondSlave{
					{Name: "eno1", MIIStatus: "up", Active: true},
					{Name: "eno2", MIIStatus: "up", Active: true},
				},
			},
		},
		{
			Kind: "bond", Name: "bond1", Members: []string{"eno3", "eno4"},
			Bond: &host.BondDetail{
				Mode: "802.3ad (4)", MIIStatus: "up",
				Slaves: []host.BondSlave{
					{Name: "eno3", MIIStatus: "up", Active: true},
					{Name: "eno4", MIIStatus: "up", Active: true},
				},
			},
		},
	}

	sampler := New(Config{Logger: testLogger()})
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)

	// Tick 1: seed counters.
	sampler.Ingest(ctx, "pve1", base, links, map[string]host.IfaceStats{
		"eno1": {RxBytes: 0}, "eno2": {RxBytes: 0},
		"eno3": {RxBytes: 0}, "eno4": {RxBytes: 0},
		"bond0": {RxBytes: 0}, "bond1": {RxBytes: 0},
	})
	// Tick 2, 5s later: bond0 (aggregate 2000Mbps capacity) pushes
	// ~1.8Gbps (near saturated); bond1 pushes ~10Mbps (idle).
	hotBytes := uint64(1_800_000_000 / 8 * 5) // ~1.8Gbps for 5s, in bytes
	coldBytes := uint64(10_000_000 / 8 * 5)   // ~10Mbps for 5s, in bytes
	sampler.Ingest(ctx, "pve1", base.Add(5*time.Second), links, map[string]host.IfaceStats{
		"eno1": {RxBytes: hotBytes / 2}, "eno2": {RxBytes: hotBytes / 2},
		"eno3": {RxBytes: coldBytes / 2}, "eno4": {RxBytes: coldBytes / 2},
		"bond0": {RxBytes: hotBytes}, "bond1": {RxBytes: coldBytes},
	})

	live := sampler.Live([]string{"bond:pve1:bond0", "bond:pve1:bond1"})
	byRef := map[string]LiveMetric{}
	for _, lm := range live {
		byRef[lm.Ref] = lm
	}
	hot, ok := byRef["bond:pve1:bond0"]
	if !ok {
		t.Fatal("bond0 missing from Live() result")
	}
	cold, ok := byRef["bond:pve1:bond1"]
	if !ok {
		t.Fatal("bond1 missing from Live() result")
	}

	if hot.UtilizationPct <= cold.UtilizationPct {
		t.Fatalf("hot bond utilization %.1f%% not visibly greater than cold bond %.1f%%", hot.UtilizationPct, cold.UtilizationPct)
	}
	if hot.UtilizationPct < 50 {
		t.Errorf("hot bond utilization = %.1f%%, want a clearly-hot value (>=50%%)", hot.UtilizationPct)
	}
	if cold.UtilizationPct > 10 {
		t.Errorf("cold bond utilization = %.1f%%, want a clearly-idle value (<=10%%)", cold.UtilizationPct)
	}
}

// TestSampler_BondSlaveImbalanceVisible covers AC2's "slave imbalance
// visible in the bond view": one slave carrying most of the bond's traffic
// (a bad LACP hash) must show a materially higher rate than its sibling in
// the Slaves breakdown.
func TestSampler_BondSlaveImbalanceVisible(t *testing.T) {
	links := bondFixtureLinks("pve1")
	sampler := New(Config{Logger: testLogger()})
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)

	sampler.Ingest(ctx, "pve1", base, links, map[string]host.IfaceStats{
		"eno1": {RxBytes: 0}, "eno2": {RxBytes: 0}, "eno3": {RxBytes: 0}, "bond0": {RxBytes: 0},
	})
	// eno1 carries 9x the traffic eno2 does over the same window — a
	// textbook LACP hash imbalance (e.g. traffic dominated by one flow).
	sampler.Ingest(ctx, "pve1", base.Add(5*time.Second), links, map[string]host.IfaceStats{
		"eno1": {RxBytes: 900_000}, "eno2": {RxBytes: 100_000}, "eno3": {RxBytes: 0},
		"bond0": {RxBytes: 1_000_000},
	})

	live := sampler.Live([]string{"bond:pve1:bond0"})
	if len(live) != 1 {
		t.Fatalf("Live() returned %d entries, want 1", len(live))
	}
	bond := live[0]
	if len(bond.Slaves) != 2 {
		t.Fatalf("bond.Slaves = %+v, want 2 entries", bond.Slaves)
	}
	rateByRef := map[string]float64{}
	for _, sl := range bond.Slaves {
		rateByRef[sl.Ref] = sl.Rates.RxBps
	}
	eno1Rate := rateByRef["physnic:pve1:eno1"]
	eno2Rate := rateByRef["physnic:pve1:eno2"]
	if eno1Rate <= eno2Rate*3 {
		t.Errorf("eno1 rate %.0f not visibly imbalanced vs eno2 rate %.0f (want eno1 clearly dominant)", eno1Rate, eno2Rate)
	}
}
