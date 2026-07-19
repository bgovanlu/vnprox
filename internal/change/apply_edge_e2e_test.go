package change_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestApply_NatAndRouteOps_EndToEndAndRollback is T-1403 acceptance
// criterion 2's full-stack half: nat.masquerade.create/nat.portforward.create/
// route.static.create apply as post-up/post-down stanzas through the
// ordinary stage->validate->diff->apply->confirm pipeline (no second
// mutation path), and a manual rollback of the committed changeset restores
// the pre-apply file byte-for-byte — reversible on rollback, not just
// reversible via a hand-paired delete op (edgeop_test.go already covers
// that half at the file-mutator level).
//
// The rules attach to a *new* bridge (vmbr9) created in the same changeset,
// deliberately avoiding the fixture's management bridge (vmbr0) — touching
// an existing mgmt-path carrier's stanza is a distinct, T-703-owned concern
// this card does not extend nat.*/route.static.* to cover (see this task's
// completion report for the noted gap), and mixing that ceremony into this
// test would obscure what it's actually proving.
func TestApply_NatAndRouteOps_EndToEndAndRollback(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	bridgeOp := change.Op{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"},
		Params: &change.BridgeCreateParams{Addresses: []string{"10.50.0.1/24"}, Comments: "lan bridge"},
	}
	masqOp := change.Op{
		Type:   change.OpNatMasqueradeCreate,
		Target: inventory.Ref{Kind: inventory.KindNatRule, Node: "pve1", ID: "masq1"},
		Params: &change.NatMasqueradeCreateParams{Iface: "vmbr9", SourceCIDR: "10.50.0.0/24"},
	}
	pfOp := change.Op{
		Type:   change.OpNatPortForwardCreate,
		Target: inventory.Ref{Kind: inventory.KindNatRule, Node: "pve1", ID: "pf-web"},
		Params: &change.NatPortForwardCreateParams{Iface: "vmbr9", Proto: "tcp", ExtPort: 8080, IntIP: "10.50.0.50", IntPort: 80},
	}
	routeOp := change.Op{
		Type:   change.OpRouteStaticCreate,
		Target: inventory.Ref{Kind: inventory.KindStaticRoute, Node: "pve1", ID: "lab-route"},
		Params: &change.RouteStaticCreateParams{Iface: "vmbr9", DestCIDR: "10.60.0.0/24", Gateway: "10.50.0.1"},
	}

	preApplyFile := mustRead(t, h, "pve1")

	cs := h.mustCreate(t, "root@pam", "add edge/NAT rules", []change.Op{bridgeOp, masqOp, pfOp, routeOp})

	validated, err := h.svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != change.StatusValidated {
		t.Fatalf("status after validate = %s, want validated (findings: %+v)", validated.Status, validated.Findings)
	}

	diff, err := h.svc.Diff(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.Files) != 1 || !diff.Files[0].Changed {
		t.Fatalf("diff did not show a changed file: %+v", diff.Files)
	}
	for _, want := range []string{
		"post-up iptables -t nat -A POSTROUTING -s 10.50.0.0/24 -o vmbr9 -j MASQUERADE",
		"post-up iptables -t nat -A PREROUTING -i vmbr9 -p tcp --dport 8080 -j DNAT --to-destination 10.50.0.50:80",
		"post-up ip route add 10.60.0.0/24 via 10.50.0.1 dev vmbr9",
	} {
		if !strings.Contains(diff.Files[0].Unified, want) {
			t.Errorf("diff missing %q:\n%s", want, diff.Files[0].Unified)
		}
	}

	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}

	committedFile := h.agent.committedFile("pve1")
	for _, want := range []string{
		"iface vmbr9 inet static",
		"post-down iptables -t nat -D POSTROUTING -s 10.50.0.0/24 -o vmbr9 -j MASQUERADE",
		"post-down iptables -t nat -D PREROUTING -i vmbr9 -p tcp --dport 8080 -j DNAT --to-destination 10.50.0.50:80",
		"post-down ip route del 10.60.0.0/24 via 10.50.0.1 dev vmbr9",
	} {
		if !strings.Contains(committedFile, want) {
			t.Fatalf("committed file missing %q:\n%s", want, committedFile)
		}
	}

	// Reversible on rollback: a manual rollback while still awaiting
	// confirmation restores the pre-apply file byte-for-byte — no orphaned
	// post-up/post-down lines, no partial revert (mirrors
	// TestRollback_AwaitingConfirm_Manual's established pattern; rolling
	// back an *already-committed* changeset instead stages a new restoring
	// draft the caller applies separately — a different, already-tested
	// code path this test isn't targeting).
	rolled, err := h.svc.Rollback(ctx, cs.ID, "root@pam", nil)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status after rollback = %s, want rolled_back", rolled.Status)
	}
	rolledBackFile := mustRead(t, h, "pve1")
	if rolledBackFile != preApplyFile {
		t.Fatalf("rollback did not restore the original file byte-for-byte:\n--- got ---\n%s\n--- want ---\n%s", rolledBackFile, preApplyFile)
	}
}
