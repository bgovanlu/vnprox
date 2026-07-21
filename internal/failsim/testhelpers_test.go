package failsim

import (
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// world is a terse inventory-snapshot builder for failsim tests, applying
// entities through a real inventory.Graph (so the resolver links bridge
// ports, bond slaves, VLAN parents, guest attachments and LLDP local NICs
// exactly as production does), mirroring internal/sim's world_test.go.
type world struct {
	host          map[string][]inventory.Entity
	nodes         []inventory.Entity
	sdn           []inventory.Entity
	guests        []inventory.Entity
	lldpNeighbors []inventory.Entity
}

func newWorld() *world { return &world{host: map[string][]inventory.Entity{}} }

func (w *world) node(name, ip string) *world {
	w.nodes = append(w.nodes, &inventory.Node{
		Ref: nodeRef(name), Name: name, IP: ip, Status: "online", Quorate: true,
	})
	return w
}

func (w *world) physnic(node, name string, linkUp bool) *world {
	w.host[node] = append(w.host[node], &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, LinkUp: linkUp, LinkUpSet: true, SpeedMbps: 1000,
	})
	return w
}

func (w *world) bond(node, name string, slaves ...string) *world {
	w.host[node] = append(w.host[node], &inventory.Bond{
		Ref:  inventory.Ref{Kind: inventory.KindBond, Node: node, ID: name},
		Name: name, Mode: "802.3ad", Slaves: slaves, DeclaredSlaves: slaves,
	})
	return w
}

// bridge adds a vlan-aware Linux bridge with the given ports and declared
// addresses (CIDR strings; may be empty).
func (w *world) bridge(node, name string, addrs []string, ports ...string) *world {
	w.host[node] = append(w.host[node], &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name: name, Virt: inventory.BridgeLinux, VlanAware: true, VlanAwareSet: true,
		Vids:      []inventory.VidRange{{Low: 1, High: 100}},
		Addresses: addrs, PortNames: ports, DeclaredPortNames: ports,
	})
	return w
}

func (w *world) lldp(node, localIface, chassisID string) *world {
	w.lldpNeighbors = append(w.lldpNeighbors, &inventory.LldpNeighbor{
		Ref:  inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: node, ID: localIface + "/" + chassisID},
		Node: node, LocalIface: localIface, ChassisID: chassisID, ChassisName: chassisID,
	})
	return w
}

func (w *world) zone(id, ztype, bridge string, nodes ...string) *world {
	w.sdn = append(w.sdn, &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: id},
		ID:  id, Type: ztype, Bridge: bridge, Nodes: nodes,
	})
	return w
}

func (w *world) vnet(id, zone string, tag int) *world {
	w.sdn = append(w.sdn, &inventory.SdnVnet{
		Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: id},
		ID:  id, Zone: zone, Tag: tag,
	})
	return w
}

func (w *world) guest(node, vmid, name string) *world {
	w.guests = append(w.guests, &inventory.Guest{
		Ref:  inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid},
		VMID: atoiSafe(vmid), Name: name, Type: "qemu", Node: node, Status: "running",
	})
	return w
}

// nic attaches guest vmid's NIC key to target (a bridge name or an SDN vnet id).
func (w *world) nic(node, vmid, key, target string, vid int) *world {
	w.guests = append(w.guests, &inventory.GuestNic{
		Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: vmid + "/" + key},
		Guest: inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid},
		Key:   key, TargetName: target, Vid: vid, Model: "virtio",
	})
	return w
}

func (w *world) build() inventory.Snapshot {
	g := inventory.NewGraph()
	for node, ents := range w.host {
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, ents)
		g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, ents)
	}
	if len(w.nodes) > 0 {
		g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, w.nodes)
	}
	if len(w.lldpNeighbors) > 0 {
		g.ApplyPoll(inventory.SourceHostLLDP, inventory.Scope{}, w.lldpNeighbors)
	}
	if len(w.sdn) > 0 {
		g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, w.sdn)
	}
	if len(w.guests) > 0 {
		g.ApplyPoll(inventory.SourcePVEGuest,
			inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}}, w.guests)
	}
	return g.Snapshot()
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// --- named fixtures --------------------------------------------------------

// threeNodeVLAN is a 3-node plain-VLAN cluster: each node has a redundant
// 2-NIC bond behind vmbr0 (which carries the node's mgmt IP), and one running
// guest on VLAN 10. The corosync ring rides vmbr0 too (homelab-style).
func threeNodeVLAN() (inventory.Snapshot, *host.CorosyncConfig) {
	w := newWorld()
	mgmt := map[string]string{"pve1": "10.0.0.1", "pve2": "10.0.0.2", "pve3": "10.0.0.3"}
	ring := map[string]string{"pve1": "10.10.0.1", "pve2": "10.10.0.2", "pve3": "10.10.0.3"}
	var cor host.CorosyncConfig
	i := 0
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		w.node(n, mgmt[n])
		w.physnic(n, "eno1", true).physnic(n, "eno2", true)
		w.bond(n, "bond0", "eno1", "eno2")
		w.bridge(n, "vmbr0", []string{mgmt[n] + "/24", ring[n] + "/24"}, "bond0")
		i++
		w.guest(n, "10"+itoa(i), "vm"+itoa(i)).nic(n, "10"+itoa(i), "net0", "vmbr0", 10)
		cor.Nodes = append(cor.Nodes, host.CorosyncNode{Name: n, RingAddrs: []string{ring[n]}, NodeID: i})
	}
	return w.build(), &cor
}

// evpnLab is a 3-node cluster with an EVPN SDN zone whose vnet rides the
// per-node underlay bridge vmbr0; each node has a redundant 2-NIC bond and one
// guest attached to the vnet.
func evpnLab() (inventory.Snapshot, *host.CorosyncConfig) {
	w := newWorld()
	mgmt := map[string]string{"pve1": "10.0.0.1", "pve2": "10.0.0.2", "pve3": "10.0.0.3"}
	w.zone("evpnzone", "evpn", "vmbr0", "pve1", "pve2", "pve3")
	w.vnet("vnet10", "evpnzone", 10)
	var cor host.CorosyncConfig
	i := 0
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		w.node(n, mgmt[n])
		w.physnic(n, "eno1", true).physnic(n, "eno2", true)
		w.bond(n, "bond0", "eno1", "eno2")
		w.bridge(n, "vmbr0", []string{mgmt[n] + "/24"}, "bond0")
		i++
		w.guest(n, "20"+itoa(i), "vm"+itoa(i)).nic(n, "20"+itoa(i), "net0", "vnet10", 0)
		cor.Nodes = append(cor.Nodes, host.CorosyncNode{Name: n, NodeID: i})
	}
	return w.build(), &cor
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
