// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// fixtureEdgeNAT is T-1403's checked-in Edge/NAT cluster-shape fixture
// (testdata/clusters/edge-nat.yaml's own doc comment explains why it seeds
// only the shape — WAN bridge, SDN simple-zone-SNAT, one running + one
// stopped guest — and not any nat.*/route.static.* rule itself).
const fixtureEdgeNAT = "../../testdata/clusters/edge-nat.yaml"

// TestEdgeNATFixture_UsableForDemoChangeset is a load-bearing regression
// guard on testdata/clusters/edge-nat.yaml: it stays loadable and a
// nat.portforward.create staged against its private-LAN bridge (vmbr1, the
// simplez zone's own realized bridge — deliberately not vmbr0, the fixture's
// WAN/management bridge, to keep this smoke test independent of T-703's
// mgmt-path ceremony) validates clean. This is exactly the first step of
// the demo flow this fixture is meant to support: load the cluster shape,
// then stage the nat.*/route.static.* ops a real operator would.
//
// newHarness itself wires no InventorySource (most of this package's tests
// don't need referential existence checks against pre-existing entities —
// see apply_helpers_test.go's own doc comment), so this test seeds a
// minimal graph with just the one Bridge entity the referential class
// needs, following newIpamHarness's identical precedent (apply_ipam_test.go).
func TestEdgeNATFixture_UsableForDemoChangeset(t *testing.T) {
	h := newHarness(t, fixtureEdgeNAT)
	ctx := context.Background()

	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}, Name: "vmbr1"},
	})
	h.svc = newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Inventory: inventorySource{g},
	})

	pfOp := change.Op{
		Type:   change.OpNatPortForwardCreate,
		Target: inventory.Ref{Kind: inventory.KindNatRule, Node: "pve1", ID: "pf-ssh"},
		Params: &change.NatPortForwardCreateParams{Iface: "vmbr1", Proto: "tcp", ExtPort: 2222, IntIP: "192.168.1.99", IntPort: 22},
	}
	cs := h.mustCreate(t, "root@pam", "expose sshbox", []change.Op{pfOp})
	validated, err := h.svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != change.StatusValidated {
		t.Fatalf("status after validate = %s, want validated (findings: %+v)", validated.Status, validated.Findings)
	}
}
