// SPDX-License-Identifier: Apache-2.0

// seed_test.go proves T-2104 AC3's "applies cleanly and produces the
// documented topology" the same way internal/blueprint's own starters_test.go
// proves it for the five bundled starters: Validate + Instantiate against a
// bare inventory.Graph (a golden, non-empty changeset) and against an
// already-conforming one (zero ops — idempotent re-instantiation,
// docs/features/blueprints.md §1). seed_pvemock_test.go additionally drives
// the flagship Ceph seed's produced ops through a real internal/pve.Client
// against a running internal/pvemock server, so at least one seed is proven
// against pvemock literally, not only against a hand-built graph.
package seed_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/hub/seed"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestSeeds_AllValid mirrors blueprint_test.TestStarters_AllValid: a typo in
// seed.go fails the build's tests, not a registry submission.
func TestSeeds_AllValid(t *testing.T) {
	for _, bp := range seed.Seeds() {
		bp := bp
		t.Run(bp.ID, func(t *testing.T) {
			if bp.ReadOnly {
				t.Errorf("seed %s: ReadOnly = true, want false (a seed is publishable/editable content, not a bundled starter)", bp.ID)
			}
			if err := blueprint.Validate(bp); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

// TestSeeds_AllDistinctIDs guards against a copy-paste ID collision, same
// reasoning as blueprint_test.TestStarters_AllDistinctIDs: seed.ByID's
// lookup silently returns the first match.
func TestSeeds_AllDistinctIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, bp := range seed.Seeds() {
		if seen[bp.ID] {
			t.Fatalf("duplicate seed id %q", bp.ID)
		}
		seen[bp.ID] = true
	}
}

// TestSeeds_BareAndConforming is T-2104 AC3 at the Instantiate layer: each
// seed instantiates against a bare fixture to the documented, non-empty
// changeset, and against an already-conforming fixture to a zero-op
// changeset (idempotent both ways) — the same two-sided proof
// blueprint_test.TestStarters_BareAndConforming uses for the bundled
// starters.
func TestSeeds_BareAndConforming(t *testing.T) {
	cases := []struct {
		seed      func(g *inventory.Graph, nodes []string)
		id        string
		nodes     []string
		wantTypes []change.OpType
		// wantConformingTypes overrides the "already conforming" leg's
		// expectation from the default (zero ops) — needed only by
		// SeedDMZWireGuardSiteToSite: its wg-tunnel entity's diffWgTunnel
		// always proposes a create (inventory.Snapshot never contains a
		// wg-tunnel to diff against — diffWgTunnel's own doc comment), so
		// "already conforming" still produces a wg.tunnel.create, every
		// time. Nil means the ordinary zero-ops expectation.
		wantConformingTypes []change.OpType
	}{
		{
			id: seed.SeedHomelabSingleNode, nodes: []string{"pve1"},
			wantTypes: []change.OpType{change.OpBridgeCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applyBridge(g, "pve1", "vmbr0", bridgeOpts{
					ports: []string{"eno1"}, vlanAware: true, vids: []int{10, 20, 30},
					addresses: []string{"192.168.1.10/24"}, gateway: "192.168.1.1",
					comments: "vnprox seed: homelab-single-node",
				})
			},
		},
		{
			id: seed.SeedCeph3NodeStorage, nodes: []string{"pve1", "pve2", "pve3"},
			wantTypes: []change.OpType{change.OpSdnZoneCreate, change.OpSdnVnetCreate, change.OpSdnSubnetCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applySdnZone(g, "cephstorage", sdnZoneOpts{typ: "vxlan", nodes: nodes, vrfVxlan: 10500})
				applySdnVnet(g, "cephstorage", "cephnet", 0, false)
				applySdnSubnet(g, "cephstorage/cephnet", "10.50.0.0/24", "10.50.0.1", false)
			},
		},
		{
			id: seed.SeedSMBVLANSegmented, nodes: []string{"pve1"},
			wantTypes: []change.OpType{change.OpBridgeCreate, change.OpBridgeCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applyBridge(g, "pve1", "vmbr0", bridgeOpts{
					ports: []string{"eno1"}, addresses: []string{"192.168.1.10/24"},
					comments: "vnprox seed: smb-vlan-segmented (management)",
				})
				applyBridge(g, "pve1", "vmbr1", bridgeOpts{
					ports: []string{"eno2"}, vlanAware: true, vids: []int{10, 20, 30, 40},
					comments: "vnprox seed: smb-vlan-segmented (department VLAN trunk: staff/guest-wifi/servers/voice)",
				})
			},
		},
		{
			id:                  seed.SeedDMZWireGuardSiteToSite,
			nodes:               []string{"pve1"},
			wantTypes:           []change.OpType{change.OpBridgeCreate, change.OpWgTunnelCreate},
			wantConformingTypes: []change.OpType{change.OpWgTunnelCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applyBridge(g, "pve1", "vmbr-dmz", bridgeOpts{
					ports: []string{"eno2"}, addresses: []string{"172.16.99.1/28"},
					comments: "vnprox seed: dmz-wireguard-site-to-site (DMZ segment; the WireGuard tunnel interface rides on this bridge)",
				})
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			bp, ok := seed.ByID(tc.id)
			if !ok {
				t.Fatalf("no such seed %q", tc.id)
			}

			t.Run("bare", func(t *testing.T) {
				g := newGraphWithNodes(tc.nodes...)
				ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: tc.nodes}, g.Snapshot())
				if err != nil {
					t.Fatalf("Instantiate: %v", err)
				}
				if got := opTypes(ops); !equalOpTypes(got, tc.wantTypes) {
					t.Fatalf("got %v, want %v", got, tc.wantTypes)
				}
			})

			t.Run("conforming", func(t *testing.T) {
				g := newGraphWithNodes(tc.nodes...)
				tc.seed(g, tc.nodes)
				ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: tc.nodes}, g.Snapshot())
				if err != nil {
					t.Fatalf("Instantiate: %v", err)
				}
				if tc.wantConformingTypes != nil {
					if got := opTypes(ops); !equalOpTypes(got, tc.wantConformingTypes) {
						t.Fatalf("got %v, want %v", got, tc.wantConformingTypes)
					}
					return
				}
				if len(ops) != 0 {
					t.Fatalf("got %v, want zero ops (already conforming)", opTypes(ops))
				}
			})
		})
	}
}

// TestSeeds_Divergent_UpdateOnlyDivergentField is the same one-field-diff
// spot check blueprint_test.TestStarters_Divergent_UpdateOnlyDivergentField
// runs for its starter: a fixture diverging in exactly one field (here, the
// homelab seed's gateway) produces an update op naming only that field, never
// a destructive recreate.
func TestSeeds_Divergent_UpdateOnlyDivergentField(t *testing.T) {
	bp, _ := seed.ByID(seed.SeedHomelabSingleNode)
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{
		ports: []string{"eno1"}, vlanAware: true, vids: []int{10, 20, 30},
		addresses: []string{"192.168.1.10/24"}, gateway: "192.168.1.254", // divergent: seed wants .1
		comments: "vnprox seed: homelab-single-node",
	})

	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeUpdate {
		t.Fatalf("got %v, want a single bridge.update", opTypes(ops))
	}
	upd := ops[0].Params.(*change.BridgeUpdateParams)
	if upd.Gateway == nil || *upd.Gateway != "192.168.1.1" {
		t.Fatalf("Gateway = %v, want pointer-to-192.168.1.1", upd.Gateway)
	}
	if upd.VlanAware != nil || upd.Vids != nil || upd.Addresses != nil || upd.Comments != nil || upd.MTU != nil || upd.STP != nil {
		t.Fatalf("expected only Gateway set, got %+v", upd)
	}
}
