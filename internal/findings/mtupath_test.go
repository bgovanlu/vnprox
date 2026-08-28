// SPDX-License-Identifier: Apache-2.0

package findings_test

// TestMTUPathMismatch_FlowsThroughUnifiedStream is AC1's check for "MTU
// path mismatch" specifically at the unified-findings layer: the
// underlying check itself is internal/drift's own CheckMTUConsistency
// (mtuPathFindings — already fixture-tested with a golden finding in
// internal/drift/mtu_test.go's TestMTUConsistency_Path, unchanged by this
// task), so this test doesn't re-derive that logic — it proves the
// adapter/engine composition faithfully carries the golden finding's
// plain-English detail, severity, and (lack of a) fix through into the
// unified stream with the right Source tag.

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestMTUPathMismatch_FlowsThroughUnifiedStream(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePhysNicDeclared(g, "pve1", "eno1", 1500)
	pveBridgeDeclared(g, "pve1", "vmbr0", 9000, []string{"eno1"})

	driftSvc := drift.New(drift.Config{Graph: g})
	eng := findings.New(findings.Config{Graph: g, Drift: driftSvc})

	found := findByCheck(t, eng.Findings(), drift.CheckMTUConsistency)
	if len(found) != 1 {
		t.Fatalf("got %d mtu_consistency findings in the unified stream, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Source != findings.SourceDrift {
		t.Errorf("Source = %q, want %q", f.Source, findings.SourceDrift)
	}
	if f.Fixable {
		t.Error("MTU path mismatch should not be fixable (which side is correct is ambiguous)")
	}
	if f.DocsLink == "" {
		t.Error("a non-fixable finding must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "vmbr0") || !strings.Contains(f.Detail, "eno1") || !strings.Contains(f.Detail, "9000") || !strings.Contains(f.Detail, "1500") {
		t.Errorf("detail = %q, want mention of vmbr0/eno1 and both MTU values (plain-English bar)", f.Detail)
	}

	if _, _, ok := eng.FixOps(f.ID); ok {
		t.Error("FixOps succeeded for an MTU path-mismatch finding, want ok=false (no computable fix)")
	}
}

func pvePhysNicDeclared(g *inventory.Graph, node, name string, mtu int) {
	n := &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, MTUDeclared: mtu,
	}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{n})
}

func pveBridgeDeclared(g *inventory.Graph, node, name string, mtu int, declaredPorts []string) {
	br := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name: name, Virt: inventory.BridgeLinux,
		MTUDeclared: mtu, DeclaredPortNames: declaredPorts,
	}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
}
