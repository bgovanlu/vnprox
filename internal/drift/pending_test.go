package drift_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
)

// TestPendingInterfaces_Changed: a staged-but-unapplied edit is flagged,
// detection only.
func TestPendingInterfaces_Changed(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePendingPhysNic(g, "pve1", "eno1", "changed")

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckPendingInterfaces)
	if len(found) != 1 {
		t.Fatalf("got %d pending_interfaces findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("pending interfaces should not be fixable")
	}
	if !strings.Contains(f.Detail, "eno1") || !strings.Contains(f.Detail, "changed") {
		t.Errorf("detail = %q, want mention of eno1 and changed", f.Detail)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("nodes = %v, want [pve1]", f.Nodes)
	}
}

// TestPendingInterfaces_Clean: no pending marker, no finding.
func TestPendingInterfaces_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePhysNic(g, "pve1", "eno1", 1500)

	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckPendingInterfaces); len(found) != 0 {
		t.Errorf("got %d pending_interfaces findings with no staged edits, want 0: %+v", len(found), found)
	}
}
