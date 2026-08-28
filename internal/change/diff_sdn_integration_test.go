// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// TestDiff_NodeFileAndSDN_RendersBothConfigDiffs is T-2003 acceptance
// criterion 4: "the config diff matches what apply would actually write, for
// a fixture changeset spanning node-file and SDN ops." Builds one changeset
// with a bridge.create (node-file) op alongside a full zone/vnet/subnet SDN
// lifecycle against the three-node fixture (T-402's own fixture, reused via
// newSDNHarness so the SDN validators see real cluster/bridge state), and
// asserts GET /changesets/{id}/diff's rendered Files cover BOTH halves —
// before this task a changeset like this silently only showed the
// interfaces-file portion.
func TestDiff_NodeFileAndSDN_RendersBothConfigDiffs(t *testing.T) {
	h := newSDNHarness(t)
	ctx := context.Background()

	ops := append(
		[]change.Op{bridgeCreateOp("pve1", "vmbr9", nil)},
		sdnLifecycleOps("zoneT2003", "vnetT2003", "10.61.0.0/24", 61)...,
	)
	cs := h.mustCreate(t, "root@pam", "node-file + sdn", ops)

	diff, err := h.svc.Diff(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	var ifaceFile, zonesFile, vnetsFile, subnetsFile *struct {
		unified string
		changed bool
	}
	for _, f := range diff.Files {
		f := f
		entry := &struct {
			unified string
			changed bool
		}{unified: f.Unified, changed: f.Changed}
		switch {
		case f.Path == "/etc/network/interfaces" && f.Node == "pve1":
			ifaceFile = entry
		case f.Path == "/etc/pve/sdn/zones.cfg":
			zonesFile = entry
		case f.Path == "/etc/pve/sdn/vnets.cfg":
			vnetsFile = entry
		case f.Path == "/etc/pve/sdn/subnets.cfg":
			subnetsFile = entry
		}
	}

	if ifaceFile == nil || !ifaceFile.changed || !strings.Contains(ifaceFile.unified, "vmbr9") {
		t.Fatalf("interfaces file diff missing/incomplete: %+v", ifaceFile)
	}
	if zonesFile == nil || !zonesFile.changed || !strings.Contains(zonesFile.unified, "zoneT2003") {
		t.Fatalf("zones.cfg diff missing/incomplete: %+v", zonesFile)
	}
	if vnetsFile == nil || !vnetsFile.changed || !strings.Contains(vnetsFile.unified, "vnetT2003") {
		t.Fatalf("vnets.cfg diff missing/incomplete: %+v", vnetsFile)
	}
	if subnetsFile == nil || !subnetsFile.changed || !strings.Contains(subnetsFile.unified, "10.61.0.0/24") {
		t.Fatalf("subnets.cfg diff missing/incomplete: %+v", subnetsFile)
	}

	// diff.Ops (the Summary tab) is unaffected by this task — it has only
	// ever covered node-file-affecting ops (ifaces.DiffChangeset's own
	// pre-existing scope); this extension touches diff.Files only.
	if len(diff.Ops) != 1 {
		t.Errorf("len(diff.Ops) = %d, want 1 (only the node-file op — diff.Ops' pre-existing scope, untouched by this task)", len(diff.Ops))
	}
}

// TestDiff_SDNOnly_NoNodeFileEntry proves the extension doesn't fabricate an
// interfaces-file entry for a changeset that never touches one.
func TestDiff_SDNOnly_NoNodeFileEntry(t *testing.T) {
	h := newSDNHarness(t)
	ctx := context.Background()

	ops := sdnLifecycleOps("zoneOnly", "vnetOnly", "10.62.0.0/24", 62)
	cs := h.mustCreate(t, "root@pam", "sdn only", ops)

	diff, err := h.svc.Diff(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.Files) != 3 {
		t.Fatalf("len(diff.Files) = %d, want exactly 3 (zones/vnets/subnets, no interfaces entry): %+v", len(diff.Files), diff.Files)
	}
	for _, f := range diff.Files {
		if f.Path == "/etc/network/interfaces" {
			t.Errorf("unexpected interfaces-file entry for a changeset with no node-file ops: %+v", f)
		}
	}
}
