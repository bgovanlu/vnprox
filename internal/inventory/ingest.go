package inventory

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// This file adapts the raw types produced by T-101 (internal/pve) and T-102
// (internal/host) into source-tagged inventory partials suitable for
// ApplyPoll. Each adapter returns entities carrying only the fields its
// source is entitled to set (per the ownership table in merge.go); the merge
// then reconciles overlapping observations of the same real object.
//
// A collector (T-104) wires readers to these adapters, e.g.:
//
//	links, _ := reader.Links(ctx, node)
//	g.ApplyPoll(inventory.SourceHostNetlink,
//	    inventory.Scope{Node: node}, inventory.FromNetlinkLinks(node, links))

// FromNetlinkLinks maps live netlink link state (host.Reader.Links) to
// SourceHostNetlink partials: PhysNics, Bonds, Bridges, and VlanIfaces with
// their runtime fields populated. veth/vxlan/dummy/unknown links are not
// modeled as top-level entities and are skipped.
func FromNetlinkLinks(node string, links []host.LinkState) []Entity {
	out := make([]Entity, 0, len(links))
	for _, l := range links {
		switch l.Kind {
		case "physical":
			out = append(out, &PhysNic{
				Ref:       Ref{Kind: KindPhysNic, Node: node, ID: l.Name},
				Name:      l.Name,
				Mac:       l.Mac,
				Driver:    l.Driver,
				PCIAddr:   l.PCIAddr,
				Duplex:    l.Duplex,
				OperState: l.OperState,
				SpeedMbps: l.SpeedMbps,
				MTU:       l.MTU,
				SRIOVVFs:  l.SRIOVNumVFs,
				LinkUp:    l.LinkUp,
			})
		case "bond":
			b := &Bond{
				Ref:    Ref{Kind: KindBond, Node: node, ID: l.Name},
				Name:   l.Name,
				Slaves: append([]string(nil), l.Members...),
				MTU:    l.MTU,
			}
			if l.Bond != nil {
				b.Mode = l.Bond.Mode
				b.LACPRate = l.Bond.LACPRate
				b.XmitHashPolicy = l.Bond.XmitHashPolicy
				b.MIIStatus = l.Bond.MIIStatus
				b.ActiveSlave = l.Bond.ActiveSlave
				for _, s := range l.Bond.Slaves {
					b.SlaveDetail = append(b.SlaveDetail, BondSlaveState{
						Name:             s.Name,
						MIIStatus:        s.MIIStatus,
						PermHWAddr:       s.PermHWAddr,
						LinkFailureCount: s.LinkFailureCount,
						Active:           s.Active,
					})
				}
			}
			out = append(out, b)
		case "bridge":
			br := &Bridge{
				Ref:       Ref{Kind: KindBridge, Node: node, ID: l.Name},
				Name:      l.Name,
				Virt:      BridgeLinux,
				PortNames: append([]string(nil), l.Members...),
				MTU:       l.MTU,
				Addresses: append([]string(nil), l.Addresses...),
			}
			if l.Bridge != nil {
				br.VlanAware = l.Bridge.VlanAware
				br.STP = l.Bridge.STP
				br.Vids = convertVids(l.Bridge.VLANs)
			}
			out = append(out, br)
		case "vlan":
			out = append(out, &VlanIface{
				Ref:        Ref{Kind: KindVlan, Node: node, ID: l.Name},
				Name:       l.Name,
				ParentName: l.VlanParent,
				Vid:        l.VlanID,
				MTU:        l.MTU,
				Addresses:  append([]string(nil), l.Addresses...),
			})
		}
	}
	return out
}

func convertVids(vs []host.VidRange) []VidRange {
	out := make([]VidRange, len(vs))
	for i, v := range vs {
		out[i] = VidRange{Low: v.Low, High: v.High}
	}
	return out
}

// FromPVENetwork maps PVE's node network view (GET /nodes/{node}/network) to
// SourcePVENetwork partials — the declared config as PVE parses it, a
// cross-check on the host interfaces file.
func FromPVENetwork(node string, ifaces []pve.NetworkInterface) []Entity {
	out := make([]Entity, 0, len(ifaces))
	for _, n := range ifaces {
		switch n.Type {
		case "eth":
			out = append(out, &PhysNic{
				Ref:         Ref{Kind: KindPhysNic, Node: node, ID: n.Iface},
				Name:        n.Iface,
				MTUDeclared: n.MTU,
			})
		case "bond":
			out = append(out, &Bond{
				Ref:            Ref{Kind: KindBond, Node: node, ID: n.Iface},
				Name:           n.Iface,
				Mode:           n.BondMode,
				DeclaredSlaves: fields(n.Slaves),
				MTUDeclared:    n.MTU,
			})
		case "bridge", "OVSBridge":
			kind, virt := KindBridge, BridgeLinux
			if n.Type == "OVSBridge" {
				kind, virt = KindOVSBridge, BridgeOVS
			}
			br := &Bridge{
				Ref:               Ref{Kind: kind, Node: node, ID: n.Iface},
				Name:              n.Iface,
				Virt:              virt,
				DeclaredPortNames: fields(n.BridgePorts),
				VlanAware:         n.BridgeVlanAware,
				MTUDeclared:       n.MTU,
				Gateway:           n.Gateway,
				Comments:          strings.TrimSpace(n.Comments),
			}
			if n.Address != "" {
				br.Addresses = []string{n.Address}
			}
			out = append(out, br)
		case "vlan", "OVSIntPort":
			v := &VlanIface{
				Ref:         Ref{Kind: KindVlan, Node: node, ID: n.Iface},
				Name:        n.Iface,
				ParentName:  n.VlanRawDevice,
				Vid:         n.VlanID,
				MTUDeclared: n.MTU,
			}
			if n.Address != "" {
				v.Addresses = []string{n.Address}
			}
			out = append(out, v)
		}
	}
	return out
}

// fields splits a space-separated PVE list (bridge_ports, slaves) into a
// clean slice.
func fields(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil
	}
	return f
}

// FromClusterStatus maps GET /cluster/status node rows to Node entities.
func FromClusterStatus(entries []pve.ClusterStatusEntry) []Entity {
	var out []Entity
	for _, e := range entries {
		if e.Type != "node" {
			continue
		}
		status := "offline"
		if e.Online {
			status = "online"
		}
		out = append(out, &Node{
			Ref:     Ref{Kind: KindNode, Node: e.Name, ID: e.Name},
			Name:    e.Name,
			IP:      e.IP,
			Status:  status,
			Quorate: e.Quorate,
			Local:   e.Local,
		})
	}
	return out
}

// FromLLDP parses raw lldpctl-style JSON (host.Reader.LLDP) into
// LldpNeighbor partials for a node.
func FromLLDP(node string, raw []byte) ([]Entity, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rows []struct {
		Local       string `json:"local-iface"`
		ChassisName string `json:"chassis_name"`
		ChassisID   string `json:"chassis_id"`
		PortID      string `json:"port_id"`
		PortDescr   string `json:"port_descr"`
		MgmtIP      string `json:"mgmt_ip"`
		VLAN        int    `json:"vlan"`
		TTL         int    `json:"ttl"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("inventory: parsing LLDP JSON for node %s: %w", node, err)
	}
	out := make([]Entity, 0, len(rows))
	for _, r := range rows {
		id := r.Local + "/" + r.ChassisID + "/" + r.PortID
		out = append(out, &LldpNeighbor{
			Ref:         Ref{Kind: KindLldpNeighbor, Node: node, ID: id},
			LocalIface:  r.Local,
			Node:        node,
			ChassisName: r.ChassisName,
			ChassisID:   r.ChassisID,
			PortID:      r.PortID,
			PortDescr:   r.PortDescr,
			MgmtIP:      r.MgmtIP,
			VLAN:        r.VLAN,
			TTL:         r.TTL,
		})
	}
	return out, nil
}

// FromPVEGuests maps guest summaries and their configs to Guest + GuestNic
// partials. configs is keyed by VMID; a missing config yields a Guest with
// no NICs.
func FromPVEGuests(resources []pve.ClusterResource, configs map[int]map[string]string) []Entity {
	var out []Entity
	for _, r := range resources {
		if r.Type != string(pve.GuestQemu) && r.Type != string(pve.GuestLXC) {
			continue
		}
		vmid := strconv.Itoa(r.VMID)
		out = append(out, &Guest{
			Ref:    Ref{Kind: KindGuest, Node: r.Node, ID: vmid},
			VMID:   r.VMID,
			Name:   r.Name,
			Type:   r.Type,
			Node:   r.Node,
			Status: r.Status,
		})
		for key, val := range configs[r.VMID] {
			if !isNetKey(key) {
				continue
			}
			nic := parseGuestNic(r.Node, vmid, key, val)
			out = append(out, nic)
		}
	}
	return out
}

func isNetKey(k string) bool {
	if !strings.HasPrefix(k, "net") {
		return false
	}
	_, err := strconv.Atoi(k[len("net"):])
	return err == nil
}

var nicModels = map[string]bool{
	"virtio": true, "e1000": true, "e1000e": true, "rtl8139": true,
	"vmxnet3": true, "ne2k_pci": true, "pcnet": true, "hwaddr": true,
}

// parseGuestNic parses a PVE guest NIC config value such as
// "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,tag=100,rate=10,firewall=1,link_down=1".
func parseGuestNic(node, vmid, key, val string) *GuestNic {
	nic := &GuestNic{
		Ref:   Ref{Kind: KindGuestNic, Node: node, ID: vmid + "/" + key},
		Guest: Ref{Kind: KindGuest, Node: node, ID: vmid},
		Key:   key,
	}
	for _, tok := range strings.Split(val, ",") {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch k {
		case "bridge":
			nic.TargetName = v
		case "tag":
			nic.Vid, _ = strconv.Atoi(v)
		case "rate":
			nic.RateMbps, _ = strconv.Atoi(v)
		case "firewall":
			nic.Firewall = v == "1"
		case "link_down":
			nic.LinkDown = v == "1"
		case "macaddr":
			nic.Mac = strings.ToUpper(v)
		default:
			if nicModels[k] {
				nic.Model = k
				nic.Mac = strings.ToUpper(v)
			}
		}
	}
	return nic
}

// FromPVESDN maps the SDN tree to cluster-scoped SdnZone/SdnVnet/SdnSubnet
// partials. zoneStatus is keyed by zone ID; subnets is keyed by vnet ID.
func FromPVESDN(
	zones []pve.SDNZone,
	vnets []pve.SDNVnet,
	subnets map[string][]pve.SDNSubnet,
	zoneStatus map[string][]pve.SDNZoneStatus,
) []Entity {
	var out []Entity
	for _, z := range zones {
		zone := &SdnZone{
			Ref:        Ref{Kind: KindSDNZone, ID: z.ID},
			ID:         z.ID,
			Type:       z.Type,
			Bridge:     z.Bridge,
			Controller: z.Controller,
			VrfVxlan:   z.VrfVxlan,
			MTU:        z.MTU,
			Nodes:      append([]string(nil), z.Nodes...),
			ExitNodes:  append([]string(nil), z.ExitNodes...),
			Peers:      append([]string(nil), z.Peers...),
			NodeStatus: map[string]string{},
		}
		for _, st := range zoneStatus[z.ID] {
			zone.NodeStatus[st.Node] = st.Status
		}
		out = append(out, zone)
	}
	for _, n := range vnets {
		// Ref.ID is the documented "zone/vnet" composite (see docs/api.md's
		// sdn-vnet::zone1/vnet1 example); the bare VNet name is kept in the
		// ID field for guest-attachment lookups.
		out = append(out, &SdnVnet{
			Ref:       Ref{Kind: KindSDNVnet, ID: n.Zone + "/" + n.ID},
			ID:        n.ID,
			Zone:      n.Zone,
			Alias:     n.Alias,
			Tag:       n.Tag,
			VlanAware: n.VlanAware,
		})
		for _, s := range subnets[n.ID] {
			sub := &SdnSubnet{
				Ref:     Ref{Kind: KindSDNSubnet, ID: s.CIDR},
				ID:      s.CIDR,
				Vnet:    s.Vnet,
				Gateway: s.Gateway,
				SNAT:    s.SNAT,
			}
			if s.DHCPRangeStart != "" || s.DHCPRangeEnd != "" {
				sub.DHCPRanges = []string{s.DHCPRangeStart + "-" + s.DHCPRangeEnd}
			}
			out = append(out, sub)
		}
	}
	return out
}

// FromPVEFirewall maps a resolved firewall ruleset to an FwRuleset partial.
// ref identifies the ruleset (scope-specific); rules are in PVE order.
func FromPVEFirewall(ref Ref, scope FwScope, opts pve.FirewallOptions, rules []pve.FirewallRule) []Entity {
	rs := &FwRuleset{
		Ref:        ref,
		Scope:      scope,
		Enabled:    opts.Enable,
		DefaultIn:  opts.PolicyIn,
		DefaultOut: opts.PolicyOut,
	}
	for _, r := range rules {
		rs.Rules = append(rs.Rules, FwRule{
			Pos:       r.Pos,
			Enabled:   r.Enabled,
			Direction: strings.ToLower(r.Type),
			Action:    r.Action,
			Proto:     r.Proto,
			Source:    r.Source,
			Dest:      r.Dest,
			Sport:     r.Sport,
			Dport:     r.Dport,
			Iface:     r.Iface,
			Macro:     r.Macro,
			Log:       r.Log,
			Comment:   r.Comment,
		})
	}
	return []Entity{rs}
}
