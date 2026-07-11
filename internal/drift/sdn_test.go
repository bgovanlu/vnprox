package drift_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
)

// TestSDNRealization_Missing: a zone lists a node as a member, but that
// node has no matching bridge — detection only, no computable fix.
func TestSDNRealization_Missing(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr99", 1500, false, nil, nil)
	pveBridge(g, "pve2", "vmbr99", 1500, false, nil, nil)
	pveSDNZone(g, "zone-legacy", "vmbr99", []string{"pve1", "pve2", "pve3"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckSDNRealization)
	if len(found) != 1 {
		t.Fatalf("got %d sdn_realization findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("sdn realization gap should not be fixable")
	}
	if f.Severity != drift.SeverityError {
		t.Errorf("severity = %s, want error", f.Severity)
	}
	if !strings.Contains(f.Detail, "zone-legacy") || !strings.Contains(f.Detail, "pve3") {
		t.Errorf("detail = %q, want mention of zone-legacy and pve3", f.Detail)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve3" {
		t.Errorf("nodes = %v, want [pve3]", f.Nodes)
	}
}

// TestSDNRealization_Clean: every member node realizes the zone's bridge.
func TestSDNRealization_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	pveBridge(g, "pve1", "vmbr99", 1500, false, nil, nil)
	pveBridge(g, "pve2", "vmbr99", 1500, false, nil, nil)
	pveSDNZone(g, "zone-a", "vmbr99", []string{"pve1", "pve2"})

	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckSDNRealization); len(found) != 0 {
		t.Errorf("got %d sdn_realization findings on a fully-realized zone, want 0: %+v", len(found), found)
	}
}
