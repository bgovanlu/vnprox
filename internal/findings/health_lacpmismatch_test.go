// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// netlinkBondLACP applies one node's bond, including bond-level MII status
// (checkLACPPartnerMismatch's "netlink reports as up" gate) and per-slave
// LACP actor/partner detail — this file's own local helper rather than
// extending testhelpers_test.go's shared netlinkBond (which deliberately
// leaves bond-level MIIStatus unset, and is shared with
// health_bondslave_test.go).
func netlinkBondLACP(g *inventory.Graph, node, name, bondMII string, slaves []inventory.BondSlaveState) {
	names := make([]string, len(slaves))
	for i, s := range slaves {
		names[i] = s.Name
	}
	b := &inventory.Bond{
		Ref:         inventory.Ref{Kind: inventory.KindBond, Node: node, ID: name},
		Name:        name,
		MIIStatus:   bondMII,
		Slaves:      names,
		SlaveDetail: slaves,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBond}}, []inventory.Entity{b})
}

func negotiatedSlave(name, partnerSystemID string, partnerKey int) inventory.BondSlaveState {
	return inventory.BondSlaveState{
		Name: name, MIIStatus: "up", Active: true,
		LACPDetailSet:         true,
		ActorSystemID:         "bc:24:11:01:00:0a",
		ActorSystemPriority:   65535,
		ActorKey:              15,
		ActorSynchronized:     true,
		ActorCollecting:       true,
		ActorDistributing:     true,
		PartnerSystemID:       partnerSystemID,
		PartnerSystemPriority: 32768,
		PartnerKey:            partnerKey,
	}
}

// TestLACPPartnerMismatch_SplitBrain: AC3's triggering fixture — a bond
// whose two slaves report different partner system IDs (each is really
// aggregating with a different upstream switch/LAG) fires exactly one
// hysteresis-debounced finding.
func TestLACPPartnerMismatch_SplitBrain(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBondLACP(g, "pve1", "bond0", "up", []inventory.BondSlaveState{
		negotiatedSlave("eno1", "3c:8c:40:aa:bb:cc", 15),
		negotiatedSlave("eno2", "aa:bb:cc:dd:ee:ff", 99),
	})

	eng := findings.New(findings.Config{Graph: g})

	first := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch)
	if len(first) != 0 {
		t.Fatalf("lacp_partner_mismatch fired on the very first observation (no debounce), got %+v", first)
	}

	found := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch)
	if len(found) != 1 {
		t.Fatalf("got %d lacp_partner_mismatch findings after 2 cycles, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Source != findings.SourceHealth {
		t.Errorf("Source = %q, want %q", f.Source, findings.SourceHealth)
	}
	if f.Fixable {
		t.Errorf("lacp_partner_mismatch should never be fixable, got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Errorf("lacp_partner_mismatch with no computable fix must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "bond0") || !strings.Contains(f.Detail, "pve1") || !strings.Contains(f.Detail, "split-brain") {
		t.Errorf("detail = %q, want mention of bond0, pve1, and split-brain (plain-English bar)", f.Detail)
	}

	if _, _, ok := eng.FixOps(f.ID); ok {
		t.Error("FixOps succeeded for a lacp_partner_mismatch finding, want ok=false")
	}
}

// TestLACPPartnerMismatch_NotNegotiated: a bond whose single detailed slave
// hasn't reached synchronized+collecting+distributing also fires.
func TestLACPPartnerMismatch_NotNegotiated(t *testing.T) {
	g := newGraphWithNodes("pve1")
	stuck := negotiatedSlave("eno1", "3c:8c:40:aa:bb:cc", 15)
	stuck.ActorDistributing = false
	netlinkBondLACP(g, "pve1", "bond0", "up", []inventory.BondSlaveState{stuck})

	eng := findings.New(findings.Config{Graph: g})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch)
	if len(found) != 1 {
		t.Fatalf("got %d lacp_partner_mismatch findings after 2 cycles, want 1: %+v", len(found), found)
	}
	if !strings.Contains(found[0].Detail, "not fully negotiated") {
		t.Errorf("detail = %q, want mention of not fully negotiated", found[0].Detail)
	}
}

// TestLACPPartnerMismatch_ClearsAfterRecovery: sustained recovery (2
// consecutive matched/negotiated cycles) clears the finding — the other
// half of the hysteresis contract, mirroring
// TestBondSlaveDown_ClearsAfterRecovery.
func TestLACPPartnerMismatch_ClearsAfterRecovery(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBondLACP(g, "pve1", "bond0", "up", []inventory.BondSlaveState{
		negotiatedSlave("eno1", "3c:8c:40:aa:bb:cc", 15),
		negotiatedSlave("eno2", "aa:bb:cc:dd:ee:ff", 99),
	})
	eng := findings.New(findings.Config{Graph: g})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch)
	if len(found) != 1 {
		t.Fatalf("setup: expected the finding active before testing recovery, got %d", len(found))
	}

	netlinkBondLACP(g, "pve1", "bond0", "up", []inventory.BondSlaveState{
		negotiatedSlave("eno1", "3c:8c:40:aa:bb:cc", 15),
		negotiatedSlave("eno2", "3c:8c:40:aa:bb:cc", 15),
	})
	stillActive := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch)
	if len(stillActive) != 1 {
		t.Fatalf("finding cleared after a single recovered sample, want it to persist one more cycle: %+v", stillActive)
	}
	cleared := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch)
	if len(cleared) != 0 {
		t.Fatalf("finding did not clear after 2 consecutive recovered samples: %+v", cleared)
	}
}

// TestLACPPartnerMismatch_MatchedState_NeverFires: AC3's clean-fixture
// half — a healthy, fully-negotiated bond never produces a finding.
func TestLACPPartnerMismatch_MatchedState_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBondLACP(g, "pve1", "bond0", "up", []inventory.BondSlaveState{
		negotiatedSlave("eno1", "3c:8c:40:aa:bb:cc", 15),
		negotiatedSlave("eno2", "3c:8c:40:aa:bb:cc", 15),
	})
	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 5; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch); len(found) != 0 {
			t.Fatalf("cycle %d: healthy bond produced a finding: %+v", i, found)
		}
	}
}

// TestLACPPartnerMismatch_NoDetail_NeverFires: a bond netlink reports as up
// but with no LACP PDU detail at all (not running 802.3ad, or an older
// kernel/driver) is silently skipped — this check has nothing to say about
// a bond it never observed negotiating.
func TestLACPPartnerMismatch_NoDetail_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBondLACP(g, "pve1", "bond0", "up", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "up", Active: true},
	})
	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 3; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch); len(found) != 0 {
			t.Fatalf("cycle %d: bond with no LACP detail produced a finding: %+v", i, found)
		}
	}
}

// TestLACPPartnerMismatch_BondDown_NeverFires: a bond netlink reports as
// down never fires, even carrying stale mismatched LACP detail — this
// check's job is "is a currently-up bond negotiated correctly", not a
// second bond_slave_down.
func TestLACPPartnerMismatch_BondDown_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBondLACP(g, "pve1", "bond0", "down", []inventory.BondSlaveState{
		negotiatedSlave("eno1", "3c:8c:40:aa:bb:cc", 15),
		negotiatedSlave("eno2", "aa:bb:cc:dd:ee:ff", 99),
	})
	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 3; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckLACPPartnerMismatch); len(found) != 0 {
			t.Fatalf("cycle %d: down bond produced a finding: %+v", i, found)
		}
	}
}
