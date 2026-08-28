// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func findByCheck(t *testing.T, fs []findings.Finding, check string) []findings.Finding {
	t.Helper()
	var out []findings.Finding
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// TestBondSlaveDown: a bond with one slave reporting MII status "down"
// fires CheckBondSlaveDown after enough consecutive cycles to clear
// hysteresis (AC1: triggering fixture + golden finding; AC4: this check has
// no computable fix).
func TestBondSlaveDown(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "down", Active: false},
	})

	eng := findings.New(findings.Config{Graph: g})

	// The check debounces (2 consecutive cycles) — a single Findings() call
	// must not fire it yet.
	first := findByCheck(t, eng.Findings(), findings.CheckBondSlaveDown)
	if len(first) != 0 {
		t.Fatalf("bond_slave_down fired on the very first observation (no debounce), got %+v", first)
	}

	found := findByCheck(t, eng.Findings(), findings.CheckBondSlaveDown)
	if len(found) != 1 {
		t.Fatalf("got %d bond_slave_down findings after 2 cycles, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Source != findings.SourceHealth {
		t.Errorf("Source = %q, want %q", f.Source, findings.SourceHealth)
	}
	if f.Fixable {
		t.Errorf("bond_slave_down should never be fixable (AC4), got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Errorf("bond_slave_down with no computable fix must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "bond0") || !strings.Contains(f.Detail, "eno2") || !strings.Contains(f.Detail, "pve1") {
		t.Errorf("detail = %q, want mention of bond0, eno2, and pve1 (plain-English bar)", f.Detail)
	}

	if _, _, ok := eng.FixOps(f.ID); ok {
		t.Error("FixOps succeeded for a bond_slave_down finding, want ok=false (no computable fix)")
	}
}

// TestBondSlaveDown_ClearsAfterRecovery: once the slave reports up again for
// enough consecutive cycles, the finding clears (the other half of AC3's
// hysteresis: not just "doesn't fire on one blip" but "clears on
// sustained recovery, not on one good sample").
func TestBondSlaveDown_ClearsAfterRecovery(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "down", Active: false},
	})
	eng := findings.New(findings.Config{Graph: g})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckBondSlaveDown)
	if len(found) != 1 {
		t.Fatalf("setup: expected the finding active before testing recovery, got %d", len(found))
	}

	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "up", Active: true},
	})
	// One good sample: still active (fall hysteresis requires 2 consecutive).
	stillActive := findByCheck(t, eng.Findings(), findings.CheckBondSlaveDown)
	if len(stillActive) != 1 {
		t.Fatalf("finding cleared after a single recovered sample, want it to persist one more cycle: %+v", stillActive)
	}
	cleared := findByCheck(t, eng.Findings(), findings.CheckBondSlaveDown)
	if len(cleared) != 0 {
		t.Fatalf("finding did not clear after 2 consecutive recovered samples: %+v", cleared)
	}
}

// TestBondSlave_AllUp_NeverFires: a healthy bond never produces a finding.
func TestBondSlave_AllUp_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "up", Active: false},
	})
	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 5; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckBondSlaveDown); len(found) != 0 {
			t.Fatalf("cycle %d: healthy bond produced a finding: %+v", i, found)
		}
	}
}
