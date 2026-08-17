package sim

import (
	"strconv"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// world is a fluent builder for a synthetic inventory snapshot, letting the
// verdict matrix construct precise L2/L3/firewall scenarios without the mock
// PVE server. It applies entities through the real inventory.Graph so the
// engine sees the same linked snapshot (edges, resolved GuestNic.BridgeOrVnet
// + EffectiveVid) the collector would produce — the linking logic is not
// re-implemented here.
type world struct {
	host    map[string][]inventory.Entity
	guestIP map[inventory.Ref][]GuestIP
	guests  []inventory.Entity
	sdn     []inventory.Entity
	fw      []inventory.Entity
}

func newWorld() *world {
	return &world{host: map[string][]inventory.Entity{}, guestIP: map[inventory.Ref][]GuestIP{}}
}

func vids(rs ...[2]int) []inventory.VidRange {
	out := make([]inventory.VidRange, len(rs))
	for i, r := range rs {
		out[i] = inventory.VidRange{Low: r[0], High: r[1]}
	}
	return out
}

// physnic adds a physical NIC (so bond slaves + LLDP can resolve edges).
func (w *world) physnic(node, name string) *world {
	w.host[node] = append(w.host[node], &inventory.PhysNic{
		Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name}, Name: name,
	})
	return w
}

// lldp adds an LLDP neighbor observed on a physical NIC (for the trunk
// cross-check). tagged is the switch port's advertised tagged VLANs.
func (w *world) lldp(node, physIface, chassisID, portID string, pvid int, tagged ...int) *world {
	w.host[node] = append(w.host[node], &inventory.LldpNeighbor{
		Ref:  inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: node, ID: physIface + "/" + chassisID},
		Node: node, LocalIface: physIface, ChassisID: chassisID, PortID: portID,
		VLAN: pvid, TaggedVLANs: tagged, TTL: 120, LastSeen: 1,
	})
	return w
}

// bond adds an LACP bond that a bridge can use as its uplink port.
func (w *world) bond(node, name string, slaves ...string) *world {
	w.host[node] = append(w.host[node], &inventory.Bond{
		Ref:  inventory.Ref{Kind: inventory.KindBond, Node: node, ID: name},
		Name: name, Mode: "802.3ad", Slaves: slaves,
	})
	return w
}

// bridge adds a Linux bridge. ports are runtime member interface names.
func (w *world) bridge(node, name string, vlanAware bool, vidSet []inventory.VidRange, gateway string, ports ...string) *world {
	w.host[node] = append(w.host[node], &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name: name, Virt: inventory.BridgeLinux, VlanAware: vlanAware, VlanAwareSet: true,
		Vids: vidSet, Gateway: gateway, PortNames: ports,
	})
	return w
}

// ovsBridge adds an OVS bridge (drives the OVS caveat).
func (w *world) ovsBridge(node, name string, ports ...string) *world {
	w.host[node] = append(w.host[node], &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindOVSBridge, Node: node, ID: name},
		Name: name, Virt: inventory.BridgeOVS, PortNames: ports,
	})
	return w
}

func (w *world) guest(node, vmid, name string) *world {
	id, _ := strconv.Atoi(vmid)
	w.guests = append(w.guests, &inventory.Guest{
		Ref:  inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid},
		VMID: id, Name: name, Node: node, Type: "qemu", Status: "running",
	})
	return w
}

// nic adds a guest NIC and returns its ref. target is the bridge/VNet name.
func (w *world) nic(node, vmid, key, target string, vid int, firewall bool) inventory.Ref {
	ref := inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: vmid + "/" + key}
	w.guests = append(w.guests, &inventory.GuestNic{
		Ref: ref, Guest: inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid},
		Key: key, TargetName: target, Vid: vid, Firewall: firewall,
		Mac: "BC:24:11:00:" + vmid + ":" + key[len(key)-1:],
	})
	return ref
}

// ip records a resolved IP for a guest NIC in the side-table.
func (w *world) ip(ref inventory.Ref, addr string, src IPSource) *world {
	w.guestIP[ref] = append(w.guestIP[ref], GuestIP{IP: addr, Source: src})
	return w
}

func (w *world) zone(id, ztype, bridge string, nodes []string, exitNodes []string) *world {
	w.sdn = append(w.sdn, &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: id},
		ID:  id, Type: ztype, Bridge: bridge, Nodes: nodes, ExitNodes: exitNodes,
	})
	return w
}

// vnet's Ref uses the real "<zone>/<vnet>" composite id
// (internal/inventory/ingest.go's SdnVnet.Ref.ID convention), not the bare
// vnet id — this is what makes every vnet-attached-guest test in this file
// exercise the real production lookup path (internal/sim/engine.go's
// vnetByRef), not a bug-compatible shortcut. See planning/reports/
// sim-vnet-resolution-bug.md.
func (w *world) vnet(id, zone string, tag int) *world {
	w.sdn = append(w.sdn, &inventory.SdnVnet{
		Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: zone + "/" + id},
		ID:  id, Zone: zone, Tag: tag,
	})
	return w
}

func (w *world) subnet(cidr, vnet, gateway string, snat bool) *world {
	w.sdn = append(w.sdn, &inventory.SdnSubnet{
		Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: cidr},
		ID:  cidr, Vnet: vnet, Gateway: gateway, SNAT: snat,
	})
	return w
}

// clusterFw adds the cluster ruleset.
func (w *world) clusterFw(enabled bool, policyIn, policyOut string, rules []inventory.FwRule, opts ...func(*inventory.FwRuleset)) *world {
	rs := &inventory.FwRuleset{
		Ref:   inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"},
		Scope: inventory.FwScopeCluster, Enabled: enabled,
		DefaultIn: policyIn, DefaultOut: policyOut, Rules: rules,
	}
	for _, o := range opts {
		o(rs)
	}
	w.fw = append(w.fw, rs)
	return w
}

// nodeFw adds a node-scope ruleset (host chain).
func (w *world) nodeFw(node string, enabled bool, rules []inventory.FwRule) *world {
	w.fw = append(w.fw, &inventory.FwRuleset{
		Ref:   inventory.Ref{Kind: inventory.KindFwRuleset, Node: node, ID: node},
		Scope: inventory.FwScopeNode, Enabled: enabled, Rules: rules,
	})
	return w
}

// guestFw adds a guest-scope ruleset. Its ID mirrors the collector's
// "guest/<kind>/<vmid>" convention so fw.BuildSnapshot keys it by the
// guest's own ref.
func (w *world) guestFw(node, vmid string, enabled bool, policyIn, policyOut string, rules []inventory.FwRule) *world {
	w.fw = append(w.fw, &inventory.FwRuleset{
		Ref:   inventory.Ref{Kind: inventory.KindFwRuleset, Node: node, ID: "guest/qemu/" + vmid},
		Scope: inventory.FwScopeGuest, Enabled: enabled,
		DefaultIn: policyIn, DefaultOut: policyOut, Rules: rules,
	})
	return w
}

// withAliases/withIPSets/withGroups are clusterFw option helpers.
func withAliases(as ...inventory.FwAlias) func(*inventory.FwRuleset) {
	return func(rs *inventory.FwRuleset) { rs.Aliases = append(rs.Aliases, as...) }
}
func withIPSets(ss ...inventory.FwIPSet) func(*inventory.FwRuleset) {
	return func(rs *inventory.FwRuleset) { rs.IPSets = append(rs.IPSets, ss...) }
}
func withGroups(gs ...inventory.FwGroup) func(*inventory.FwRuleset) {
	return func(rs *inventory.FwRuleset) { rs.Groups = append(rs.Groups, gs...) }
}

// build applies all entities through a real inventory.Graph and returns the
// engine input.
func (w *world) build() Input {
	g := inventory.NewGraph()
	for node, ents := range w.host {
		// Apply host entities under both the runtime (netlink) and declared
		// (pve-network) sources so every field resolves regardless of which
		// source owns it in the merge precedence (netlink owns vlanAware/vids/
		// portNames; pve-network owns gateway/addresses).
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, ents)
		g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, ents)
	}
	if len(w.sdn) > 0 {
		g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, w.sdn)
	}
	if len(w.guests) > 0 {
		g.ApplyPoll(inventory.SourcePVEGuest,
			inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}}, w.guests)
	}
	if len(w.fw) > 0 {
		g.ApplyPoll(inventory.SourcePVEFirewall,
			inventory.Scope{Kinds: []inventory.Kind{inventory.KindFwRuleset}}, w.fw)
	}
	return Input{Inventory: g.Snapshot(), GuestIPs: w.guestIP}
}

// rule is a terse FwRule constructor for tests.
func rule(pos int, dir, action string, opts ...func(*inventory.FwRule)) inventory.FwRule {
	r := inventory.FwRule{Pos: pos, Enabled: true, Direction: dir, Action: action}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func proto(p string) func(*inventory.FwRule)  { return func(r *inventory.FwRule) { r.Proto = p } }
func dport(p string) func(*inventory.FwRule)  { return func(r *inventory.FwRule) { r.Dport = p } }
func source(s string) func(*inventory.FwRule) { return func(r *inventory.FwRule) { r.Source = s } }
func dest(s string) func(*inventory.FwRule)   { return func(r *inventory.FwRule) { r.Dest = s } }
func macro(m string) func(*inventory.FwRule)  { return func(r *inventory.FwRule) { r.Macro = m } }
func disabled() func(*inventory.FwRule)       { return func(r *inventory.FwRule) { r.Enabled = false } }
func groupRef(name string) inventory.FwRule {
	return inventory.FwRule{Enabled: true, Direction: "group", Action: name}
}

// helpers for endpoints.
func guestEP(ref inventory.Ref) Endpoint { return Endpoint{Kind: EndpointGuestNic, NicRef: ref} }
func ipEP(addr string) Endpoint          { return Endpoint{Kind: EndpointIP, IP: addr} }
func extEP() Endpoint                    { return Endpoint{Kind: EndpointExternal} }
