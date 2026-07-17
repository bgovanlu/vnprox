package topology_test

// T-804 AC2: "Inventory golden test against three-node-vlan's bonded
// interface: actor/partner fields appear in GET /inventory/{ref}'s
// fields." three-node-vlan.yaml's pve1 node declares bond0 in 802.3ad mode
// (see that fixture's own comment on the "LACP to access switch" bond),
// but pvemock/FixtureReader deliberately does not model per-slave LACP PDU
// detail (host/fixture.go's own doc comment: fixtures express declared
// intent and simple runtime facts, not full kernel state) — the same
// reason TestIngestNetlink (ingest_test.go) and this package's own
// ports_test.go/fdb_test.go "ApplyPoll entities directly" pattern exist for
// runtime-only fields fixtures can't express. This test runs the real
// pvemock -> collect -> inventory.Graph pipeline against three-node-vlan
// first (proving bond0's ref/base shape is the fixture's own), then
// overlays a host-netlink poll carrying LACP actor/partner detail for that
// same bond0 ref — exactly what a real internal/host.Real reader would
// additionally report on real hardware (needs-hardware-validation.md).

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

func TestDetail_BondLACPFields(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)

	bondRef := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}
	if _, ok := graph.Snapshot().Get(bondRef); !ok {
		t.Fatalf("fixture setup: %s not present after the initial poll", bondRef)
	}

	graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1", Kinds: []inventory.Kind{inventory.KindBond}},
		inventory.FromNetlinkLinks("pve1", []host.LinkState{
			{
				Kind:    "bond",
				Name:    "bond0",
				Members: []string{"eno1", "eno2"},
				MTU:     1500,
				Bond: &host.BondDetail{
					Mode:      "802.3ad",
					MIIStatus: "up",
					Slaves: []host.BondSlave{
						{
							Name: "eno1", MIIStatus: "up", Active: true,
							LACPDetailSet: true,
							ActorSystemID: "bc:24:11:01:00:0a", ActorSystemPriority: 65535, ActorKey: 15,
							ActorSynchronized: true, ActorCollecting: true, ActorDistributing: true,
							PartnerSystemID: "3c:8c:40:aa:bb:cc", PartnerSystemPriority: 32768, PartnerKey: 15,
						},
						{
							Name: "eno2", MIIStatus: "up", Active: true,
							LACPDetailSet: true,
							ActorSystemID: "bc:24:11:01:00:0a", ActorSystemPriority: 65535, ActorKey: 15,
							ActorSynchronized: true, ActorCollecting: true, ActorDistributing: true,
							PartnerSystemID: "3c:8c:40:aa:bb:cc", PartnerSystemPriority: 32768, PartnerKey: 15,
						},
					},
				},
			},
		}))

	d, ok := topology.Detail(graph.Snapshot(), bondRef)
	if !ok {
		t.Fatalf("Detail(%s) ok=false", bondRef)
	}

	slaveDetail, ok := d.Fields["SlaveDetail"].([]any)
	if !ok || len(slaveDetail) != 2 {
		t.Fatalf("Fields[SlaveDetail] = %#v, want a 2-element slice", d.Fields["SlaveDetail"])
	}

	for _, raw := range slaveDetail {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("slave row = %#v, want an object", raw)
		}
		if row["ActorSystemID"] != "bc:24:11:01:00:0a" {
			t.Errorf("row[ActorSystemID] = %v, want bc:24:11:01:00:0a", row["ActorSystemID"])
		}
		if row["ActorKey"] != float64(15) {
			t.Errorf("row[ActorKey] = %v, want 15", row["ActorKey"])
		}
		if row["PartnerSystemID"] != "3c:8c:40:aa:bb:cc" {
			t.Errorf("row[PartnerSystemID] = %v, want 3c:8c:40:aa:bb:cc", row["PartnerSystemID"])
		}
		if row["PartnerKey"] != float64(15) {
			t.Errorf("row[PartnerKey] = %v, want 15", row["PartnerKey"])
		}
		if row["ActorSynchronized"] != true || row["ActorCollecting"] != true || row["ActorDistributing"] != true {
			t.Errorf("row actor state = %v/%v/%v, want all true",
				row["ActorSynchronized"], row["ActorCollecting"], row["ActorDistributing"])
		}
		if row["LACPDetailSet"] != true {
			t.Errorf("row[LACPDetailSet] = %v, want true", row["LACPDetailSet"])
		}
	}
}
