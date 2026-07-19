package change_test

// T-1506 acceptance criterion 4: vf.provision stages, validates, diffs,
// applies, and rolls back against the fixture host.Reader (via the same
// applyHarness/pvemock-backed NodeAgent every other node-file op family's
// golden e2e test in this package uses), exercised only against the
// fixture — no real SR-IOV hardware involved.

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// vfPFRef is the spare, unconfigured "eno2" NIC in single-node.yaml (T-702's
// own doc comment: "not a bridge port, not enslaved to anything") — using
// it as the vf.provision target keeps this test independent of vmbr0's
// bridge membership/policy.
func vfPFRef() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno2"}
}

func vfBoolPtr(v bool) *bool { return &v }

func vfProvisionOp(count int, vlan int, spoofCheck *bool) change.Op {
	return change.Op{
		Type:   change.OpVFProvision,
		Target: vfPFRef(),
		Params: &change.VFProvisionParams{Count: count, VLAN: vlan, SpoofCheck: spoofCheck},
	}
}

// withVFPF seeds a bare PhysNic "eno2" into the harness's inventory so
// vf.provision's referential check (the PF must exist) passes — the v1 op
// vocabulary has no "physnic.create" (physnics are hardware, never
// op-created), mirroring withInventory's own doc comment on why tests
// referencing an existing entity by name need this seam.
func withVFPF() func(*change.Config) {
	return withInventory(&inventory.PhysNic{Ref: vfPFRef(), Name: "eno2"})
}

func TestApply_VFProvision_EndToEnd(t *testing.T) {
	h := newHarness(t, fixtureSingleNode, withVFPF())
	ctx := context.Background()

	before := mustRead(t, h, "pve1")
	if strings.Contains(before, "sriov_numvfs") {
		t.Fatal("precondition: sriov_numvfs already present before apply")
	}

	cs := h.mustCreate(t, "root@pam", "provision 2 VFs on eno2", []change.Op{
		vfProvisionOp(2, 100, vfBoolPtr(true)),
	})

	validated, err := h.svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != change.StatusValidated {
		t.Fatalf("status after validate = %s, want validated (findings: %s)", validated.Status, validated.Findings)
	}

	diff, err := h.svc.Diff(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.Files) != 1 || !diff.Files[0].Changed ||
		!strings.Contains(diff.Files[0].Unified, "sriov_numvfs") ||
		!strings.Contains(diff.Files[0].Unified, "eno2") {
		t.Fatalf("diff did not show the vf.provision commands: %+v", diff.Files)
	}

	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}

	log := h.applyLog(t, cs.ID)
	for _, s := range log.Steps {
		if s.Status != change.StepOK {
			t.Fatalf("step %d status = %s, want ok", s.Index, s.Status)
		}
	}

	committed := h.agent.committedFile("pve1")
	for _, want := range []string{
		"echo 2 > /sys/class/net/eno2/device/sriov_numvfs",
		"ip link set eno2 vf 0 vlan 100 spoofchk on trust off",
		"ip link set eno2 vf 1 vlan 100 spoofchk on trust off",
	} {
		if !strings.Contains(committed, want) {
			t.Errorf("committed file missing %q:\n%s", want, committed)
		}
	}

	final, err := h.svc.Confirm(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if final.Status != change.StatusCommitted {
		t.Fatalf("status after confirm = %s, want committed", final.Status)
	}
}

// TestApply_VFProvision_NoConfirm_AutoRollback proves rollback works for
// free through the ordinary node-file pre-snapshot restore mechanism every
// other node-file op family already relies on — no vf.*-specific rollback
// code was needed.
func TestApply_VFProvision_NoConfirm_AutoRollback(t *testing.T) {
	h := newHarness(t, fixtureSingleNode, withVFPF())
	ctx := context.Background()

	before := mustRead(t, h, "pve1")

	cs := h.mustCreate(t, "alice@pve", "provision VFs on eno2", []change.Op{
		vfProvisionOp(1, 0, nil),
	})
	if _, err := h.svc.Apply(ctx, cs.ID, "alice@pve", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(h.agent.committedFile("pve1"), "sriov_numvfs") {
		t.Fatal("vf.provision commands not applied")
	}

	h.timers.fireLatest(t)

	rolled := h.get(t, cs.ID)
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status after deadline = %s, want rolled_back", rolled.Status)
	}
	after := h.agent.committedFile("pve1")
	if after != before {
		t.Fatalf("file not restored byte-identically:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}
