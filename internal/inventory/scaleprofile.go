// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"fmt"
	"strconv"
)

// scaleprofile.go (T-4107) generalizes scale_test.go's buildScaleModel (the
// topology.md §4 target: 8 nodes x 6 NICs, 4 bridges/node, 300 guests, 40
// VNets) into a parameterized, exported generator, so packages other than
// internal/inventory's own tests (internal/topology's envelope benchmark,
// specifically) can build a graph at an arbitrary size without duplicating
// this entity-construction logic or going through a slower path (pvemock +
// internal/collect) than the thing being measured needs.
//
// It builds entities directly as source-tagged poll batches, the same way
// buildScaleModel does and for the same reason its own doc comment gives:
// this measures Graph.ApplyPoll/Snapshot in isolation from parsing/collector
// cost, which is a different, separately-measured concern (see
// internal/pvemock/scaleprofile.go for the fixture-level generator that
// feeds the collector path instead).
//
// scale_test.go's buildScaleModel is intentionally left untouched — it is
// the existing, working T-607-target benchmark, and this file is additive.

// ScaleProfileConfig sizes a synthetic cluster for BuildScaleGraph.
type ScaleProfileConfig struct {
	Nodes          int
	NicsPerNode    int
	BridgesPerNode int
	GuestsPerNode  int // total guests = Nodes * GuestsPerNode
	VNets          int
	Zones          int
}

// EnvelopeProfile is T-4107's documented scale envelope: 50 nodes, 5,000
// guests (100/node), 100 VNets across 4 zones — see
// docs/development.md's "Scale envelope" section.
var EnvelopeProfile = ScaleProfileConfig{
	Nodes: 50, NicsPerNode: 6, BridgesPerNode: 4, GuestsPerNode: 100, VNets: 100, Zones: 4,
}

// BuildScaleGraph builds a Graph populated at cfg's size, applying the same
// per-source poll batches (host netlink, PVE-declared network, PVE cluster,
// PVE SDN, PVE guest) buildScaleModel does, generalized to cfg's counts.
func BuildScaleGraph(cfg ScaleProfileConfig) *Graph {
	g := NewGraph()

	nodes := make([]string, cfg.Nodes)
	netlink := make(map[string][]Entity, cfg.Nodes)
	pveNet := make(map[string][]Entity, cfg.Nodes)
	var sdn []Entity
	var guests []Entity
	var clusterN []Entity

	for i := 1; i <= cfg.Nodes; i++ {
		node := "pve" + strconv.Itoa(i)
		nodes[i-1] = node
		clusterN = append(clusterN, &Node{
			Ref: Ref{Kind: KindNode, Node: node, ID: node}, Name: node, Status: "online", Quorate: true,
		})
		var nl, pv []Entity
		for j := 1; j <= cfg.NicsPerNode; j++ {
			name := "eno" + strconv.Itoa(j)
			nl = append(nl, &PhysNic{
				Ref: Ref{Kind: KindPhysNic, Node: node, ID: name}, Name: name,
				Mac: fmt.Sprintf("aa:bb:cc:%02d:%02d:01", i%100, j), Driver: "ixgbe",
				SpeedMbps: 10000, Duplex: "full", MTU: 1500, LinkUp: true, LinkUpSet: true, OperState: "up",
			})
			pv = append(pv, &PhysNic{Ref: Ref{Kind: KindPhysNic, Node: node, ID: name}, Name: name, MTUDeclared: 1500})
		}
		nl = append(nl, &Bond{
			Ref: Ref{Kind: KindBond, Node: node, ID: "bond0"}, Name: "bond0",
			Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, MIIStatus: "up",
			ActiveSlave: "eno1", MTU: 1500,
		})
		pv = append(pv, &Bond{
			Ref: Ref{Kind: KindBond, Node: node, ID: "bond0"}, Name: "bond0",
			Mode: "802.3ad", DeclaredSlaves: []string{"eno1", "eno2"}, MTUDeclared: 1500,
		})
		for b := 0; b < cfg.BridgesPerNode; b++ {
			name := "vmbr" + strconv.Itoa(b)
			var ports []string
			if b == 0 {
				ports = []string{"bond0"}
			}
			nl = append(nl, &Bridge{
				Ref: Ref{Kind: KindBridge, Node: node, ID: name}, Name: name, Virt: BridgeLinux,
				PortNames: ports, VlanAware: true, VlanAwareSet: true, MTU: 1500,
				Vids: []VidRange{{Low: 2, High: 4094}},
			})
			pv = append(pv, &Bridge{
				Ref: Ref{Kind: KindBridge, Node: node, ID: name}, Name: name, Virt: BridgeLinux,
				DeclaredPortNames: ports, VlanAware: true, VlanAwareSet: true, MTUDeclared: 1500, Comments: "managed",
			})
		}
		nl = append(nl, &VlanIface{
			Ref: Ref{Kind: KindVlan, Node: node, ID: "vmbr0.100"}, Name: "vmbr0.100",
			ParentName: "vmbr0", Vid: 100, MTU: 1500,
		})
		netlink[node] = nl
		pveNet[node] = pv
	}

	for z := 0; z < cfg.Zones; z++ {
		zid := "zone" + strconv.Itoa(z)
		sdn = append(sdn, &SdnZone{
			Ref: Ref{Kind: KindSDNZone, ID: zid}, ID: zid, Type: "vlan", Bridge: "vmbr1",
			Nodes: nodes,
		})
	}
	for v := 0; v < cfg.VNets; v++ {
		zid := "zone" + strconv.Itoa(v%cfg.Zones)
		vname := "vnet" + strconv.Itoa(v)
		sdn = append(sdn, &SdnVnet{
			Ref: Ref{Kind: KindSDNVnet, ID: zid + "/" + vname}, ID: vname, Zone: zid, Tag: 10 + v,
		})
		cidr := fmt.Sprintf("10.%d.%d.0/24", v/250, v%250)
		sdn = append(sdn, &SdnSubnet{
			Ref: Ref{Kind: KindSDNSubnet, ID: cidr}, ID: cidr, Vnet: vname,
			Gateway: fmt.Sprintf("10.%d.%d.1", v/250, v%250),
		})
	}

	totalGuests := cfg.Nodes * cfg.GuestsPerNode
	for gi := 0; gi < totalGuests; gi++ {
		node := nodes[gi%cfg.Nodes]
		vmid := strconv.Itoa(100 + gi)
		guests = append(guests, &Guest{
			Ref: Ref{Kind: KindGuest, Node: node, ID: vmid}, VMID: 100 + gi,
			Name: "vm" + vmid, Type: "qemu", Node: node, Status: "running",
		})
		var target string
		var tag int
		if gi%3 == 0 {
			target = "vnet" + strconv.Itoa(gi%cfg.VNets)
		} else {
			target = "vmbr" + strconv.Itoa(gi%max(cfg.BridgesPerNode, 1))
			tag = 10 + gi%20
		}
		guests = append(guests, &GuestNic{
			Ref:   Ref{Kind: KindGuestNic, Node: node, ID: vmid + "/net0"},
			Guest: Ref{Kind: KindGuest, Node: node, ID: vmid}, Key: "net0",
			TargetName: target, Vid: tag, Model: "virtio",
			Mac: fmt.Sprintf("de:ad:be:ef:%02x:%02x", (gi/256)%256, gi%256),
		})
	}

	for _, node := range nodes {
		g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, netlink[node])
		g.ApplyPoll(SourcePVENetwork, Scope{Node: node}, pveNet[node])
	}
	g.ApplyPoll(SourcePVECluster, Scope{}, clusterN)
	g.ApplyPoll(SourcePVESDN, Scope{}, sdn)
	g.ApplyPoll(SourcePVEGuest, Scope{Kinds: []Kind{KindGuest, KindGuestNic}}, guests)

	return g
}
