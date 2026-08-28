// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"fmt"
	"strconv"
)

// scaleModel builds a synthetic cluster at the topology.md §4 scale target
// (8 nodes × 6 NICs, 4 bridges/node, 300 guests, 40 VNets) as source-tagged
// poll batches. It is shared by the snapshot benchmark and the concurrency
// stress test.
type scaleModel struct {
	nodes    []string
	netlink  map[string][]Entity // per-node runtime L2/physical
	pveNet   map[string][]Entity // per-node declared (cross-source merge)
	sdn      []Entity            // cluster-scoped zones/vnets/subnets
	guests   []Entity            // guests + nics (cluster-wide)
	clusterN []Entity            // node entities
}

func buildScaleModel() *scaleModel {
	const (
		numNodes       = 8
		nicsPerNode    = 6
		bridgesPerNode = 4
		numGuests      = 300
		numVNets       = 40
		numZones       = 4
	)
	m := &scaleModel{
		netlink: map[string][]Entity{},
		pveNet:  map[string][]Entity{},
	}
	for i := 1; i <= numNodes; i++ {
		node := "pve" + strconv.Itoa(i)
		m.nodes = append(m.nodes, node)
		m.clusterN = append(m.clusterN, &Node{
			Ref: Ref{Kind: KindNode, Node: node, ID: node}, Name: node, Status: "online", Quorate: true,
		})
		var nl, pv []Entity
		for j := 1; j <= nicsPerNode; j++ {
			name := "eno" + strconv.Itoa(j)
			nl = append(nl, &PhysNic{
				Ref: Ref{Kind: KindPhysNic, Node: node, ID: name}, Name: name,
				Mac: fmt.Sprintf("aa:bb:cc:%02d:%02d:01", i, j), Driver: "ixgbe",
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
		for b := 0; b < bridgesPerNode; b++ {
			name := "vmbr" + strconv.Itoa(b)
			ports := []string(nil)
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
		m.netlink[node] = nl
		m.pveNet[node] = pv
	}

	for z := 0; z < numZones; z++ {
		zid := "zone" + strconv.Itoa(z)
		m.sdn = append(m.sdn, &SdnZone{
			Ref: Ref{Kind: KindSDNZone, ID: zid}, ID: zid, Type: "vlan", Bridge: "vmbr1",
			Nodes: m.nodes,
		})
	}
	for v := 0; v < numVNets; v++ {
		zid := "zone" + strconv.Itoa(v%numZones)
		vname := "vnet" + strconv.Itoa(v)
		m.sdn = append(m.sdn, &SdnVnet{
			Ref: Ref{Kind: KindSDNVnet, ID: zid + "/" + vname}, ID: vname, Zone: zid, Tag: 10 + v,
		})
		cidr := fmt.Sprintf("10.%d.0.0/24", v)
		m.sdn = append(m.sdn, &SdnSubnet{
			Ref: Ref{Kind: KindSDNSubnet, ID: cidr}, ID: cidr, Vnet: vname,
			Gateway: fmt.Sprintf("10.%d.0.1", v),
		})
	}

	for gi := 0; gi < numGuests; gi++ {
		node := m.nodes[gi%numNodes]
		vmid := strconv.Itoa(100 + gi)
		m.guests = append(m.guests, &Guest{
			Ref: Ref{Kind: KindGuest, Node: node, ID: vmid}, VMID: 100 + gi,
			Name: "vm" + vmid, Type: "qemu", Node: node, Status: "running",
		})
		var target string
		var tag int
		if gi%3 == 0 {
			target = "vnet" + strconv.Itoa(gi%numVNets) // SDN VNet
		} else {
			target = "vmbr" + strconv.Itoa(gi%4) // plain bridge
			tag = 10 + gi%20
		}
		m.guests = append(m.guests, &GuestNic{
			Ref:   Ref{Kind: KindGuestNic, Node: node, ID: vmid + "/net0"},
			Guest: Ref{Kind: KindGuest, Node: node, ID: vmid}, Key: "net0",
			TargetName: target, Vid: tag, Model: "virtio",
			Mac: fmt.Sprintf("de:ad:be:ef:%02x:%02x", gi/256, gi%256),
		})
	}
	return m
}

// applyAll ingests every batch into g and returns the total entity count.
func (m *scaleModel) applyAll(g *Graph) int {
	for _, node := range m.nodes {
		g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, m.netlink[node])
		g.ApplyPoll(SourcePVENetwork, Scope{Node: node}, m.pveNet[node])
	}
	g.ApplyPoll(SourcePVECluster, Scope{}, m.clusterN)
	g.ApplyPoll(SourcePVESDN, Scope{}, m.sdn)
	g.ApplyPoll(SourcePVEGuest, Scope{Kinds: []Kind{KindGuest, KindGuestNic}}, m.guests)
	return g.Snapshot().Len()
}
