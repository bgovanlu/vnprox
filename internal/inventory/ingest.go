package inventory

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// This file adapts the raw types produced by T-101 (internal/pve) and T-102
// (internal/host) into source-tagged inventory partials suitable for
// ApplyPoll. Each adapter returns entities carrying only the fields its
// source is entitled to set (per the ownership table in merge.go); the merge
// then reconciles overlapping observations of the same real object.
//
// Every adapter also attaches the raw source text each entity was derived
// from (setRawSource): the interfaces(5) stanza for FromInterfaces, the
// pretty-printed JSON of the PVE API object for the FromPVE* adapters, and
// a compact JSON rendering for the netlink/LLDP host adapters. The graph
// retains it per (Ref, Source) and exposes it via Snapshot.RawSource.
//
// A collector (T-104) wires readers to these adapters, e.g.:
//
//	links, _ := reader.Links(ctx, node)
//	g.ApplyPoll(inventory.SourceHostNetlink,
//	    inventory.Scope{Node: node}, inventory.FromNetlinkLinks(node, links))

// prettyJSON renders a PVE API object as indented JSON for raw-source
// retention; "" on the (never expected for these plain data types) marshal
// error.
func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// compactJSON is prettyJSON without indentation, used for the host-side
// runtime observations (netlink link state, LLDP rows) where the "raw
// source" is our own struct rendering rather than upstream text.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// FromNetlinkLinks maps live netlink link state (host.Reader.Links) to
// SourceHostNetlink partials: PhysNics, Bonds, Bridges, and VlanIfaces with
// their runtime fields populated. veth/vxlan/dummy/unknown links are not
// modeled as top-level entities and are skipped.
func FromNetlinkLinks(node string, links []host.LinkState) []Entity {
	out := make([]Entity, 0, len(links))
	for _, l := range links {
		var ent Entity
		switch l.Kind {
		case "physical":
			ent = &PhysNic{
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
				// The kernel always knows a link's carrier state, so a
				// netlink observation genuinely reports linkUp even when
				// its value is false.
				LinkUpSet: true,
			}
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
			ent = b
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
				// Bridge detail present: the kernel reported the running
				// vlan_filtering/stp state, so both flagged bools are
				// genuinely reported (even when false). Without detail we
				// leave them "not reported" rather than implying false.
				br.VlanAware, br.VlanAwareSet = l.Bridge.VlanAware, true
				br.STP, br.STPSet = l.Bridge.STP, true
				br.Vids = convertVids(l.Bridge.VLANs)
				br.FDB = convertFDB(l.Bridge.FDB)
			}
			ent = br
		case "vlan":
			ent = &VlanIface{
				Ref:        Ref{Kind: KindVlan, Node: node, ID: l.Name},
				Name:       l.Name,
				ParentName: l.VlanParent,
				Vid:        l.VlanID,
				MTU:        l.MTU,
				Addresses:  append([]string(nil), l.Addresses...),
			}
		default:
			continue
		}
		setRaw(ent, compactJSON(l))
		out = append(out, ent)
	}
	return out
}

// setRaw attaches raw source text to an entity contribution. Every concrete
// entity type embeds rawSrc, whose pointer-receiver setter this reaches.
func setRaw(e Entity, raw string) {
	if s, ok := e.(interface{ setRawSource(string) }); ok {
		s.setRawSource(raw)
	}
}

func convertVids(vs []host.VidRange) []VidRange {
	out := make([]VidRange, len(vs))
	for i, v := range vs {
		out[i] = VidRange{Low: v.Low, High: v.High}
	}
	return out
}

// convertFDB converts host.FDBEntry (T-306's netlink FDB reader,
// internal/host.BridgeDetail.FDB) to this package's own FDBEntry, nil for
// an empty/nil input so a bridge with no learned entries carries a nil
// slice rather than an allocated-but-empty one (matching every other
// optional-slice field's zero-value convention on this type).
func convertFDB(fdb []host.FDBEntry) []FDBEntry {
	if len(fdb) == 0 {
		return nil
	}
	out := make([]FDBEntry, len(fdb))
	for i, e := range fdb {
		out[i] = FDBEntry{
			Mac: e.Mac, Port: e.Port, Vlan: e.Vlan,
			Master: e.Master, Permanent: e.Permanent, Stale: e.Stale,
		}
	}
	return out
}

// FromPVENetwork maps PVE's node network view (GET /nodes/{node}/network) to
// SourcePVENetwork partials — the declared config as PVE parses it, a
// cross-check on the host interfaces file.
func FromPVENetwork(node string, ifaces []pve.NetworkInterface) []Entity {
	out := make([]Entity, 0, len(ifaces))
	for _, n := range ifaces {
		var ent Entity
		switch n.Type {
		case "eth":
			ent = &PhysNic{
				Ref:         Ref{Kind: KindPhysNic, Node: node, ID: n.Iface},
				Name:        n.Iface,
				MTUDeclared: n.MTU,
			}
		case "bond":
			ent = &Bond{
				Ref:            Ref{Kind: KindBond, Node: node, ID: n.Iface},
				Name:           n.Iface,
				Mode:           n.BondMode,
				DeclaredSlaves: fields(n.Slaves),
				MTUDeclared:    n.MTU,
			}
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
				// PVE's parse of a bridge stanza always conveys the
				// declared vlan-aware state (absent bridge_vlan_aware in
				// the API payload means declared off), so this counts as a
				// genuine report either way. PVE carries no STP field, so
				// stp stays "not reported" (STPSet false).
				VlanAwareSet: true,
				MTUDeclared:  n.MTU,
				Gateway:      n.Gateway,
				Comments:     strings.TrimSpace(n.Comments),
			}
			if n.Address != "" {
				br.Addresses = []string{n.Address}
			}
			ent = br
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
			ent = v
		default:
			continue
		}
		setRaw(ent, prettyJSON(n))
		out = append(out, ent)
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
		n := &Node{
			Ref:     Ref{Kind: KindNode, Node: e.Name, ID: e.Name},
			Name:    e.Name,
			IP:      e.IP,
			Status:  status,
			Quorate: e.Quorate,
			Local:   e.Local,
		}
		setRaw(n, prettyJSON(e))
		out = append(out, n)
	}
	return out
}

// FromLLDP parses raw lldpctl-style JSON (host.Reader.LLDP) into
// LldpNeighbor partials for node, observed at now (LastSeen). Parsing
// itself is host.ParseLLDP's job (the defensive lldpctl-schema reader,
// T-302); this adapter only maps host.LLDPNeighbor onto the inventory
// entity shape and assigns each neighbor's stable Ref id, unchanged from
// pre-T-302 behavior: "<local-iface>/<chassis-id>/<port-id>".
func FromLLDP(node string, raw []byte, now time.Time) ([]Entity, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	rows, err := host.ParseLLDP(raw)
	if err != nil {
		return nil, fmt.Errorf("inventory: parsing LLDP JSON for node %s: %w", node, err)
	}
	out := make([]Entity, 0, len(rows))
	for _, r := range rows {
		id := r.LocalIface + "/" + r.ChassisID + "/" + r.PortID
		n := &LldpNeighbor{
			Ref:           Ref{Kind: KindLldpNeighbor, Node: node, ID: id},
			LocalIface:    r.LocalIface,
			Node:          node,
			Protocol:      r.Protocol,
			ChassisName:   r.ChassisName,
			ChassisID:     r.ChassisID,
			ChassisIDType: r.ChassisIDType,
			ChassisDescr:  r.ChassisDescr,
			PortID:        r.PortID,
			PortIDType:    r.PortIDType,
			PortDescr:     r.PortDescr,
			MgmtIPs:       append([]string(nil), r.MgmtIPs...),
			VLAN:          r.PVID,
			TaggedVLANs:   append([]int(nil), r.TaggedVLANs...),
			SpeedMbps:     r.SpeedMbps,
			SpeedDescr:    r.SpeedDescr,
			TTL:           r.TTL,
			LastSeen:      now.Unix(),
		}
		if len(r.MgmtIPs) > 0 {
			n.MgmtIP = r.MgmtIPs[0]
		}
		setRaw(n, compactJSON(r))
		out = append(out, n)
	}
	return out, nil
}

// RetainStaleLLDP folds forward any of node's previously-resolved
// LldpNeighbor entities in snap that fresh (this poll's just-parsed
// FromLLDP result) omits but that have not yet crossed spec §3's 10-minute
// drop threshold, so a neighbor that momentarily stops being reported (a
// failed poll, or lldpd itself dropping it once its own TTL expires) lingers
// — greying, per internal/topology's staleness computation — instead of
// disappearing from the graph the instant one poll misses it. ApplyPoll's
// normal per-Source scope reconciliation (graph.go) otherwise removes any
// previously-contributed Ref a poll's entity list omits, which is exactly
// right for netlink/interfaces state (always a live, complete snapshot) but
// wrong for LLDP: the whole point of a TTL-based staleness lifecycle is
// that a link's last-known neighbor should keep being shown, greyed, for a
// while after we stop hearing about it.
//
// The returned slice is fresh plus the retained stale entries, ready to
// pass straight to Graph.ApplyPoll(SourceHostLLDP, Scope{Node: node}, ...).
func RetainStaleLLDP(snap Snapshot, node string, fresh []Entity, now time.Time) []Entity {
	const dropAfter = 10 * time.Minute

	freshRefs := make(map[Ref]bool, len(fresh))
	for _, e := range fresh {
		freshRefs[e.GetRef()] = true
	}

	out := append([]Entity(nil), fresh...)
	for _, e := range snap.All() {
		ref := e.GetRef()
		if ref.Kind != KindLldpNeighbor || ref.Node != node || freshRefs[ref] {
			continue
		}
		n, ok := e.(*LldpNeighbor)
		if !ok || n.LastSeen == 0 {
			continue
		}
		if now.Sub(time.Unix(n.LastSeen, 0)) > dropAfter {
			continue // past the drop threshold: let ApplyPoll retire it normally.
		}
		out = append(out, n.clone())
	}
	return out
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
		g := &Guest{
			Ref:    Ref{Kind: KindGuest, Node: r.Node, ID: vmid},
			VMID:   r.VMID,
			Name:   r.Name,
			Type:   r.Type,
			Node:   r.Node,
			Status: r.Status,
		}
		setRaw(g, prettyJSON(r))
		out = append(out, g)
		for key, val := range configs[r.VMID] {
			if !isNetKey(key) {
				continue
			}
			nic := parseGuestNic(r.Node, vmid, key, val)
			// The NIC's raw PVE object is its single guest-config entry.
			setRaw(nic, prettyJSON(map[string]string{key: val}))
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
		// The zone entity merges two PVE responses (the zone object and its
		// per-node status), so its raw source carries both.
		setRaw(zone, prettyJSON(struct {
			Status []pve.SDNZoneStatus `json:"status,omitempty"`
			Zone   pve.SDNZone         `json:"zone"`
		}{zoneStatus[z.ID], z}))
		out = append(out, zone)
	}
	for _, n := range vnets {
		// Ref.ID is the documented "zone/vnet" composite (see docs/api.md's
		// sdn-vnet::zone1/vnet1 example); the bare VNet name is kept in the
		// ID field for guest-attachment lookups.
		vnet := &SdnVnet{
			Ref:       Ref{Kind: KindSDNVnet, ID: n.Zone + "/" + n.ID},
			ID:        n.ID,
			Zone:      n.Zone,
			Alias:     n.Alias,
			Tag:       n.Tag,
			VlanAware: n.VlanAware,
		}
		setRaw(vnet, prettyJSON(n))
		out = append(out, vnet)
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
			setRaw(sub, prettyJSON(s))
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
	// The ruleset entity merges two PVE responses (options + rules), so its
	// raw source carries both.
	setRaw(rs, prettyJSON(struct {
		Options pve.FirewallOptions `json:"options"`
		Rules   []pve.FirewallRule  `json:"rules"`
	}{opts, rules}))
	return []Entity{rs}
}

// --- interfaces(5) file (SourceHostInterfaces) -----------------------------

// FromInterfaces maps a parsed /etc/network/interfaces(5) file
// (host.ParseInterfaces) to SourceHostInterfaces partials — the node's
// DECLARED network config, the top-precedence source for every declared
// field in merge.go's ownership table (mtuDeclared, addresses, gateway,
// comments, declared bridge ports / bond slaves, bond mode, vlan-aware /
// stp / vids, vlan id and parent).
//
// Stanza classification, in order:
//
//   - ovs_type OVSBridge / OVSBond / OVSIntPort map to KindOVSBridge /
//     KindOVSBond / KindVlan (mirroring FromPVENetwork's type mapping);
//     any other ovs_type (e.g. OVSPort, a role marker on a device whose
//     true kind netlink/PVE report directly) contributes nothing.
//   - any bridge-* option (bridge-ports, bridge-vlan-aware, ...) -> Bridge.
//   - any bond-* option, or the legacy ifenslave "slaves" option -> Bond.
//   - a vlan-raw-device / vlan-id option, or a "parent.VID" name -> VlanIface.
//   - loopback stanzas contribute nothing.
//   - anything else is treated as a physical NIC declaration (only its
//     name and declared MTU are modeled, matching FromPVENetwork's "eth"
//     handling). On a PVE node the interfaces file overwhelmingly declares
//     exactly the four modeled kinds, so this default is right in
//     practice; a misdeclared stanza at worst yields a declared-only
//     physnic entity, never a clobbered one (merge keeps sources apart).
//
// interfaces(5) allows several stanzas per logical interface (one per
// address family); they are folded into one entity — addresses accumulate,
// scalar options take the first occurrence. Option names match with '_'
// and '-' treated as equivalent (ifupdown2 accepts both spellings).
//
// Each entity's raw source is its stanza text, byte-identical to the file:
// the concatenation of every "iface <name> ..." entry (header + body) for
// that interface, in file order, straight from the lossless AST.
func FromInterfaces(node string, f *host.File) []Entity {
	if f == nil {
		return nil
	}
	var order []string
	byName := map[string][]*host.Entry{}
	for _, e := range f.Ifaces() {
		if _, seen := byName[e.Name]; !seen {
			order = append(order, e.Name)
		}
		byName[e.Name] = append(byName[e.Name], e)
	}
	out := make([]Entity, 0, len(order))
	for _, name := range order {
		if ent := interfacesEntity(node, name, byName[name]); ent != nil {
			out = append(out, ent)
		}
	}
	return out
}

// interfacesEntity classifies and builds the entity for one interface's
// stanzas, or nil if the stanzas describe nothing this model covers.
func interfacesEntity(node, name string, entries []*host.Entry) Entity {
	for _, e := range entries {
		if e.Method == "loopback" {
			return nil
		}
	}
	ovsType, _ := ifaceOpt(entries, "ovs-type")
	var ent Entity
	switch ovsType {
	case "OVSBridge":
		ent = interfacesBridge(node, name, entries, KindOVSBridge, BridgeOVS)
	case "OVSBond":
		ent = interfacesOVSBond(node, name, entries)
	case "OVSIntPort":
		ent = interfacesOVSIntPort(node, name, entries)
	case "":
		switch {
		case ifaceOptPrefix(entries, "bridge-"):
			ent = interfacesBridge(node, name, entries, KindBridge, BridgeLinux)
		case ifaceOptPrefix(entries, "bond-") || ifaceOptExists(entries, "slaves"):
			ent = interfacesBond(node, name, entries)
		case interfacesIsVlan(name, entries):
			ent = interfacesVlan(node, name, entries)
		default:
			ent = &PhysNic{
				Ref:         Ref{Kind: KindPhysNic, Node: node, ID: name},
				Name:        name,
				MTUDeclared: ifaceOptInt(entries, "mtu"),
			}
		}
	default:
		return nil
	}
	if ent != nil {
		setRaw(ent, stanzaText(entries))
	}
	return ent
}

func interfacesBridge(node, name string, entries []*host.Entry, kind Kind, virt BridgeVirt) Entity {
	br := &Bridge{
		Ref:         Ref{Kind: kind, Node: node, ID: name},
		Name:        name,
		Virt:        virt,
		Addresses:   ifaceAddresses(entries),
		MTUDeclared: ifaceOptInt(entries, "mtu"),
		Comments:    ifaceComments(entries),
	}
	br.Gateway, _ = ifaceOpt(entries, "gateway")
	portsKey := "bridge-ports"
	if virt == BridgeOVS {
		portsKey = "ovs-ports"
	}
	// "bridge-ports none" declares an explicitly port-less bridge; the
	// slice merge cannot distinguish empty-set from not-reported, so both
	// map to "no contribution" here.
	if v, ok := ifaceOpt(entries, portsKey); ok && v != "none" {
		br.DeclaredPortNames = fields(v)
	}
	if v, ok := ifaceOpt(entries, "bridge-vlan-aware"); ok {
		br.VlanAware, br.VlanAwareSet = ifaceBool(v), true
	}
	if v, ok := ifaceOpt(entries, "bridge-stp"); ok {
		br.STP, br.STPSet = ifaceBool(v), true
	}
	if v, ok := ifaceOpt(entries, "bridge-vids"); ok {
		br.Vids = parseVidRangeList(v)
	}
	return br
}

func interfacesBond(node, name string, entries []*host.Entry) Entity {
	b := &Bond{
		Ref:         Ref{Kind: KindBond, Node: node, ID: name},
		Name:        name,
		MTUDeclared: ifaceOptInt(entries, "mtu"),
	}
	slaves, ok := ifaceOpt(entries, "bond-slaves")
	if !ok {
		slaves, _ = ifaceOpt(entries, "slaves")
	}
	if slaves != "" && slaves != "none" {
		b.DeclaredSlaves = fields(slaves)
	}
	b.Mode, _ = ifaceOpt(entries, "bond-mode")
	if v, ok := ifaceOpt(entries, "bond-lacp-rate"); ok {
		b.LACPRate = normalizeLACPRate(v)
	}
	b.XmitHashPolicy, _ = ifaceOpt(entries, "bond-xmit-hash-policy")
	return b
}

func interfacesOVSBond(node, name string, entries []*host.Entry) Entity {
	b := &Bond{
		Ref:         Ref{Kind: KindOVSBond, Node: node, ID: name},
		Name:        name,
		MTUDeclared: ifaceOptInt(entries, "mtu"),
	}
	if v, ok := ifaceOpt(entries, "ovs-bonds"); ok {
		b.DeclaredSlaves = fields(v)
	}
	// OVS declares the bond mode inside ovs_options ("bond_mode=..."),
	// not as a dedicated option line.
	b.Mode = ovsOption(entries, "bond_mode")
	return b
}

func interfacesOVSIntPort(node, name string, entries []*host.Entry) Entity {
	v := &VlanIface{
		Ref:         Ref{Kind: KindVlan, Node: node, ID: name},
		Name:        name,
		Addresses:   ifaceAddresses(entries),
		MTUDeclared: ifaceOptInt(entries, "mtu"),
	}
	v.ParentName, _ = ifaceOpt(entries, "ovs-bridge")
	if tag := ovsOption(entries, "tag"); tag != "" {
		v.Vid, _ = strconv.Atoi(tag)
	}
	return v
}

func interfacesVlan(node, name string, entries []*host.Entry) Entity {
	v := &VlanIface{
		Ref:         Ref{Kind: KindVlan, Node: node, ID: name},
		Name:        name,
		Addresses:   ifaceAddresses(entries),
		MTUDeclared: ifaceOptInt(entries, "mtu"),
	}
	v.ParentName, _ = ifaceOpt(entries, "vlan-raw-device")
	if raw, ok := ifaceOpt(entries, "vlan-id"); ok {
		v.Vid, _ = strconv.Atoi(raw)
	}
	// Debian-standard "parent.VID" naming fills whichever of the two the
	// options left blank (PVE itself never writes a vlan-id option).
	if idx := strings.LastIndexByte(name, '.'); idx > 0 {
		if vid, err := strconv.Atoi(name[idx+1:]); err == nil {
			if v.Vid == 0 {
				v.Vid = vid
			}
			if v.ParentName == "" {
				v.ParentName = name[:idx]
			}
		}
	}
	return v
}

// interfacesIsVlan reports whether the stanzas declare a VLAN
// sub-interface: an explicit vlan-raw-device / vlan-id option, or the
// Debian-standard "parent.VID" name.
func interfacesIsVlan(name string, entries []*host.Entry) bool {
	if ifaceOptExists(entries, "vlan-raw-device") || ifaceOptExists(entries, "vlan-id") {
		return true
	}
	idx := strings.LastIndexByte(name, '.')
	if idx <= 0 {
		return false
	}
	vid, err := strconv.Atoi(name[idx+1:])
	return err == nil && vid > 0
}

// --- interfaces(5) option helpers -----------------------------------------

// canonOptKey folds ifupdown2's accepted '_' spelling of option names onto
// the hyphenated form, so lookups match either.
func canonOptKey(k string) string { return strings.ReplaceAll(k, "_", "-") }

// ifaceOpt returns the first value of the named option (hyphen-canonical
// key) across an interface's stanzas.
func ifaceOpt(entries []*host.Entry, key string) (string, bool) {
	for _, e := range entries {
		for _, item := range e.Options() {
			if canonOptKey(item.Key) == key {
				return item.Value, true
			}
		}
	}
	return "", false
}

func ifaceOptExists(entries []*host.Entry, key string) bool {
	_, ok := ifaceOpt(entries, key)
	return ok
}

// ifaceOptPrefix reports whether any option's (hyphen-canonical) name
// starts with prefix.
func ifaceOptPrefix(entries []*host.Entry, prefix string) bool {
	for _, e := range entries {
		for _, item := range e.Options() {
			if strings.HasPrefix(canonOptKey(item.Key), prefix) {
				return true
			}
		}
	}
	return false
}

// ifaceOptInt is ifaceOpt parsed as an int, 0 when absent or malformed.
func ifaceOptInt(entries []*host.Entry, key string) int {
	v, ok := ifaceOpt(entries, key)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// ifaceBool parses an interfaces(5) boolean option value ("yes"/"on"/
// "true"/"1" are true; everything else, including "no"/"off", is false).
func ifaceBool(v string) bool {
	switch strings.ToLower(v) {
	case "yes", "on", "true", "1":
		return true
	}
	return false
}

// ifaceAddresses collects every address option across an interface's
// stanzas, canonicalized to CIDR form: a bare IPv4 address is combined
// with the same stanza's netmask option (dotted-quad or prefix length)
// when present, matching the CIDR form PVE's network API reports so the
// two declared sources agree on equal config.
func ifaceAddresses(entries []*host.Entry) []string {
	var out []string
	for _, e := range entries {
		netmask, _ := entryOpt(e, "netmask")
		for _, item := range e.Options() {
			if canonOptKey(item.Key) == "address" && item.Value != "" {
				out = append(out, cidrAddress(item.Value, netmask))
			}
		}
	}
	return out
}

// entryOpt is ifaceOpt over a single stanza.
func entryOpt(e *host.Entry, key string) (string, bool) {
	for _, item := range e.Options() {
		if canonOptKey(item.Key) == key {
			return item.Value, true
		}
	}
	return "", false
}

// cidrAddress joins a bare address with its netmask as "addr/prefix";
// addresses already in CIDR form (or with no usable netmask) pass through.
func cidrAddress(addr, netmask string) string {
	if addr == "" || strings.Contains(addr, "/") || netmask == "" {
		return addr
	}
	if p, err := strconv.Atoi(netmask); err == nil && p >= 0 && p <= 128 {
		return addr + "/" + strconv.Itoa(p)
	}
	if ip := net.ParseIP(netmask); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			if ones, bits := net.IPMask(ip4).Size(); bits == 32 {
				return addr + "/" + strconv.Itoa(ones)
			}
		}
	}
	return addr
}

// ifaceComments gathers a stanza body's comment lines into the same
// newline-joined, '#'-stripped form PVE's network API reports in its
// "comments" field, so the two declared sources agree on equal config.
func ifaceComments(entries []*host.Entry) string {
	var lines []string
	for _, e := range entries {
		for _, item := range e.Body {
			if item.Kind != host.BodyComment {
				continue
			}
			line := strings.TrimSpace(item.Raw)
			line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// normalizeLACPRate folds ifupdown2's numeric bond-lacp-rate spellings
// onto the names the kernel reports in /proc/net/bonding, so the declared
// and runtime sources agree on equal config instead of flagging a
// spurious "1" vs "fast" conflict.
func normalizeLACPRate(v string) string {
	switch v {
	case "1":
		return "fast"
	case "0":
		return "slow"
	}
	return v
}

// ovsOption extracts one key=value token from the ovs_options option line.
func ovsOption(entries []*host.Entry, key string) string {
	raw, ok := ifaceOpt(entries, "ovs-options")
	if !ok {
		return ""
	}
	for _, tok := range strings.Fields(raw) {
		if k, v, found := strings.Cut(tok, "="); found && k == key {
			return v
		}
	}
	return ""
}

// parseVidRangeList parses a bridge-vids value ("2-4094", "10 20 30",
// "2-100,200") into VidRanges.
func parseVidRangeList(s string) []VidRange {
	var out []VidRange
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if lo, hi, found := strings.Cut(part, "-"); found {
			l, errL := strconv.Atoi(lo)
			h, errH := strconv.Atoi(hi)
			if errL == nil && errH == nil {
				out = append(out, VidRange{Low: l, High: h})
			}
			continue
		}
		if v, err := strconv.Atoi(part); err == nil {
			out = append(out, VidRange{Low: v, High: v})
		}
	}
	return out
}

// stanzaText renders an interface's stanzas losslessly from the AST: every
// Entry/BodyItem carries its exact source bytes in Raw (see host.File's
// Render contract), so this is the file's own text for those stanzas.
func stanzaText(entries []*host.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Raw)
		for _, item := range e.Body {
			b.WriteString(item.Raw)
		}
	}
	return b.String()
}
