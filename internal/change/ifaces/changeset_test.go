// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

func loadThreeNodeVlanReader(t *testing.T) *host.FixtureReader {
	t.Helper()
	fx, err := pvemock.LoadFixture("../../../testdata/clusters/three-node-vlan.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)
	return host.NewFixtureReader(pvemock.NewFixtureHostReader(srv))
}

// TestDiffChangeset_ThreeNodeVlan is task card T-204 AC4: "Diff endpoint
// returns correct unified diffs for a multi-node draft (three-node
// fixture)." It builds a draft touching all three nodes of
// testdata/clusters/three-node-vlan.yaml (each already has bond0/vmbr0/
// vmbr0.20 per that fixture — see its network: section) and checks that
// DiffChangeset produces one changed FileDiff per node, each rendering the
// expected edit, plus one OpSummary per op in input order.
func TestDiffChangeset_ThreeNodeVlan(t *testing.T) {
	reader := loadThreeNodeVlanReader(t)
	ctx := context.Background()

	mtu := 9000
	ops := []Op{
		// pve1: bump the trunk bridge's MTU.
		IfaceUpdate{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), MTU: &mtu},
		// pve2: add a new VLAN sub-interface for the app tier.
		VlanCreate{
			Target: ref(inventory.KindVlan, "pve2", "vmbr0.100"),
			Parent: "vmbr0", VID: 100, Addresses: []string{"10.100.0.12/24"}, Autostart: true,
		},
		// pve3: add a second bond slave... modeled instead as adding a new
		// bridge port (bond0.20 does not exist as a bridge yet in the
		// fixture, so exercise bridge.port.add against the existing
		// vmbr0.20 -> vmbr0 relationship isn't valid; use a plain iface
		// rename-free MTU bump on eno2 to keep this a pure file op).
		IfaceUpdate{Target: ref(inventory.KindPhysNic, "pve3", "eno2"), Comments: strPtr("spare uplink")},
	}

	diff, err := DiffChangeset(ctx, reader, ops, "CS-3NODE")
	if err != nil {
		t.Fatalf("DiffChangeset: %v", err)
	}

	if len(diff.Ops) != len(ops) {
		t.Fatalf("len(diff.Ops) = %d, want %d", len(diff.Ops), len(ops))
	}
	for i, want := range []string{"iface.update", "vlan.create", "iface.update"} {
		if diff.Ops[i].Op != want {
			t.Errorf("diff.Ops[%d].Op = %q, want %q", i, diff.Ops[i].Op, want)
		}
	}

	if len(diff.Files) != 3 {
		t.Fatalf("len(diff.Files) = %d, want 3 (one per node); got %+v", len(diff.Files), diff.Files)
	}
	byNode := make(map[string]FileDiff, 3)
	for _, fd := range diff.Files {
		byNode[fd.Node] = fd
	}
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		fd, ok := byNode[node]
		if !ok {
			t.Fatalf("no FileDiff for node %s; got %+v", node, diff.Files)
		}
		if !fd.Changed || fd.Unified == "" {
			t.Errorf("node %s: expected a non-empty changed diff, got %+v", node, fd)
		}
		if fd.Path != "/etc/network/interfaces" {
			t.Errorf("node %s: Path = %q, want /etc/network/interfaces", node, fd.Path)
		}
	}

	if !strings.Contains(byNode["pve1"].Unified, "+\tmtu 9000") {
		t.Errorf("pve1 diff missing expected mtu bump:\n%s", byNode["pve1"].Unified)
	}
	if !strings.Contains(byNode["pve2"].Unified, "+auto vmbr0.100") {
		t.Errorf("pve2 diff missing new VLAN stanza:\n%s", byNode["pve2"].Unified)
	}
	if !strings.Contains(byNode["pve2"].Unified, "managed by vnprox (changeset CS-3NODE)") {
		t.Errorf("pve2 diff missing managed-by-vnprox marker:\n%s", byNode["pve2"].Unified)
	}
	if !strings.Contains(byNode["pve3"].Unified, "+\t#spare uplink") {
		t.Errorf("pve3 diff missing expected comment addition:\n%s", byNode["pve3"].Unified)
	}

	// Node order in Files must be first-appearance order across ops
	// (pve1, pve2, pve3), not e.g. alphabetical or map-iteration order.
	gotOrder := []string{diff.Files[0].Node, diff.Files[1].Node, diff.Files[2].Node}
	wantOrder := []string{"pve1", "pve2", "pve3"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Errorf("Files node order = %v, want %v", gotOrder, wantOrder)
			break
		}
	}
}

// TestDiffChangeset_NoOpsNoFiles checks the degenerate case: a changeset
// with no node-file-affecting ops touches no files (guards against a
// nil/empty ops slice producing a spurious node entry).
func TestDiffChangeset_NoOpsNoFiles(t *testing.T) {
	reader := loadThreeNodeVlanReader(t)
	diff, err := DiffChangeset(context.Background(), reader, nil, "CS-EMPTY")
	if err != nil {
		t.Fatalf("DiffChangeset: %v", err)
	}
	if len(diff.Files) != 0 || len(diff.Ops) != 0 {
		t.Errorf("expected empty diff, got %+v", diff)
	}
}

func strPtr(s string) *string { return &s }
