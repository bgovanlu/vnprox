package drift_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
)

// TestFileRuntimeDivergence_Membership: live (netlink) bridge port
// membership diverges from the declared interfaces file — a manual
// `ip link set <nic> master <bridge>` outside vnprox. Detection only.
func TestFileRuntimeDivergence_Membership(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveBridge(g, "pve1", "vmbr0", 1500, false, nil, []string{"eno1"})
	netlinkBridge(g, "pve1", "vmbr0", 1500, []string{"eno1", "eno3"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckFileRuntimeDivergence)
	if len(found) != 1 {
		t.Fatalf("got %d file_runtime_divergence findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("file/runtime membership divergence should not be fixable")
	}
	if !strings.Contains(f.Detail, "eno3") {
		t.Errorf("detail = %q, want mention of eno3 (the manually-attached NIC)", f.Detail)
	}
}

// TestFileRuntimeDivergence_MTU: an entity's own live MTU diverges from
// its declared MTU.
func TestFileRuntimeDivergence_MTU(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePhysNic(g, "pve1", "eno1", 1500)
	netlinkPhysNic(g, "pve1", "eno1", 9000, true)

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckFileRuntimeDivergence)
	if len(found) != 1 {
		t.Fatalf("got %d file_runtime_divergence findings, want 1: %+v", len(found), found)
	}
	if !strings.Contains(found[0].Detail, "9000") || !strings.Contains(found[0].Detail, "1500") {
		t.Errorf("detail = %q, want both MTU values", found[0].Detail)
	}
}

// TestFileRuntimeDivergence_Clean: matching live and declared state
// produces no findings.
func TestFileRuntimeDivergence_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePhysNic(g, "pve1", "eno1", 1500)
	netlinkPhysNic(g, "pve1", "eno1", 1500, true)
	pveBridge(g, "pve1", "vmbr0", 1500, false, nil, []string{"eno1"})
	netlinkBridge(g, "pve1", "vmbr0", 1500, []string{"eno1"})

	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckFileRuntimeDivergence); len(found) != 0 {
		t.Errorf("got %d file_runtime_divergence findings on matching state, want 0: %+v", len(found), found)
	}
}
