package docexport

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/annotate"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/sdn"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// Gather assembles a Data value from the live sources the export needs.
// snap is the current inventory snapshot (topology.Service/*inventory.Graph
// share this exact type); tree/treeErr is the SDN read (treeErr non-nil
// degrades that section rather than failing the whole export — see
// Data.SDNErr); ports is the LLDP wiring table (topology.Service.Ports());
// fwSnap is the firewall-relevant slice of the same snapshot
// (fw.BuildSnapshot(snap.All()), the same conversion GET /firewall/*
// already performs); topo is the full projected topology (already carries
// generatedAt, but this function's own now is what stamps the document
// itself, per docs/features/blueprints.md §4's "Timestamped").
func Gather(snap inventory.Snapshot, tree sdn.Tree, treeErr error, ports []topology.PortRow, fwSnap fw.Snapshot, topo topology.Topology, now time.Time) Data {
	d := Data{
		GeneratedAt: now.Unix(),
		Topology:    topo,
		LLDP:        ports,
		SDN:         tree,
	}
	if treeErr != nil {
		d.SDNErr = treeErr.Error()
	}

	d.Nodes, d.Interfaces = gatherInterfaces(snap)
	d.VLANs = gatherVlans(snap, tree)
	d.Firewall = gatherFirewall(fwSnap)
	return d
}

// gatherInterfaces builds the per-node interface tables from every
// PhysNic/Bond/Bridge/VlanIface entity in snap, grouped by node and sorted
// by (kind, name) within each node for deterministic output.
func gatherInterfaces(snap inventory.Snapshot) ([]string, map[string][]InterfaceRow) {
	byNode := map[string][]InterfaceRow{}
	nodeSet := map[string]bool{}

	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Node:
			nodeSet[v.Name] = true
		case *inventory.PhysNic:
			byNode[v.GetRef().Node] = append(byNode[v.GetRef().Node], InterfaceRow{
				Kind: "physnic", Name: v.Name, MTU: mtuOf(v.MTU, v.MTUDeclared), Up: v.LinkUp,
				Detail: fmt.Sprintf("mac %s, driver %s, %d Mbps", v.Mac, v.Driver, v.SpeedMbps),
			})
		case *inventory.Bond:
			slaves := v.Slaves
			if len(slaves) == 0 {
				slaves = v.DeclaredSlaves
			}
			byNode[v.GetRef().Node] = append(byNode[v.GetRef().Node], InterfaceRow{
				Kind: "bond", Name: v.Name, MTU: mtuOf(v.MTU, v.MTUDeclared),
				Detail: fmt.Sprintf("mode %s, slaves %s", v.Mode, strings.Join(sortedCopy(slaves), ",")),
			})
		case *inventory.Bridge:
			ports := v.PortNames
			if len(ports) == 0 {
				ports = v.DeclaredPortNames
			}
			detail := fmt.Sprintf("ports %s", strings.Join(sortedCopy(ports), ","))
			if v.VlanAware {
				detail = fmt.Sprintf("VLAN-aware, %svids %s", detail+"; ", vidsString(v.Vids))
			}
			byNode[v.GetRef().Node] = append(byNode[v.GetRef().Node], InterfaceRow{
				Kind: "bridge", Name: v.Name, MTU: mtuOf(v.MTU, v.MTUDeclared),
				Addresses: strings.Join(sortedCopy(v.Addresses), ","),
				Detail:    detail,
			})
		case *inventory.VlanIface:
			byNode[v.GetRef().Node] = append(byNode[v.GetRef().Node], InterfaceRow{
				Kind: "vlan", Name: v.Name, MTU: mtuOf(v.MTU, v.MTUDeclared),
				Addresses: strings.Join(sortedCopy(v.Addresses), ","),
				Detail:    fmt.Sprintf("parent %s, vid %d", v.ParentName, v.Vid),
			})
		}
	}

	for node := range byNode {
		nodeSet[node] = true
	}

	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		if n != "" {
			nodes = append(nodes, n)
		}
	}
	sort.Strings(nodes)

	for _, rows := range byNode {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Kind != rows[j].Kind {
				return rows[i].Kind < rows[j].Kind
			}
			return rows[i].Name < rows[j].Name
		})
	}
	return nodes, byNode
}

// gatherVlans builds the VLAN matrix from VlanIface sub-interfaces (a
// node's own tagged sub-interface) and SDN VNet tags (realized on every
// node of the VNet's zone) — docs/features/blueprints.md §4's "VLAN
// matrix". Bridge trunk ranges (VlanAware bridges' Vids) are reported in
// the per-node interface table's Detail column instead of expanded here:
// a wide trunk range (e.g. 10-4094) would otherwise blow the matrix up
// into thousands of near-duplicate rows for no documentation value.
func gatherVlans(snap inventory.Snapshot, tree sdn.Tree) []VlanRow {
	byVid := map[int]map[string]map[string]bool{} // vid -> node -> iface names

	add := func(vid int, node, iface string) {
		if vid <= 0 {
			return
		}
		if byVid[vid] == nil {
			byVid[vid] = map[string]map[string]bool{}
		}
		if byVid[vid][node] == nil {
			byVid[vid][node] = map[string]bool{}
		}
		byVid[vid][node][iface] = true
	}

	for _, e := range snap.All() {
		if v, ok := e.(*inventory.VlanIface); ok {
			add(v.Vid, v.GetRef().Node, v.Name)
		}
	}
	for _, zone := range tree.Zones {
		for _, vnet := range zone.Vnets {
			if vnet.Tag <= 0 {
				continue
			}
			for _, node := range zone.Nodes {
				add(vnet.Tag, node, "sdn:"+vnet.ID)
			}
		}
	}

	vids := make([]int, 0, len(byVid))
	for vid := range byVid {
		vids = append(vids, vid)
	}
	sort.Ints(vids)

	rows := make([]VlanRow, 0, len(vids))
	for _, vid := range vids {
		nodes := map[string][]string{}
		for node, ifaceSet := range byVid[vid] {
			ifaces := make([]string, 0, len(ifaceSet))
			for iface := range ifaceSet {
				ifaces = append(ifaces, iface)
			}
			sort.Strings(ifaces)
			nodes[node] = ifaces
		}
		rows = append(rows, VlanRow{VID: vid, Nodes: nodes})
	}
	return rows
}

// gatherFirewall builds one summary row per observed ruleset scope
// (docs/features/blueprints.md §4: "firewall summaries").
func gatherFirewall(snap fw.Snapshot) []FirewallRow {
	var rows []FirewallRow

	if snap.Cluster != nil {
		rows = append(rows, firewallRow(snap, inventory.FwScopeCluster, "", snap.Cluster))
	}

	nodeNames := make([]string, 0, len(snap.Nodes))
	for n := range snap.Nodes {
		nodeNames = append(nodeNames, n)
	}
	sort.Strings(nodeNames)
	for _, n := range nodeNames {
		rows = append(rows, firewallRow(snap, inventory.FwScopeNode, n, snap.Nodes[n]))
	}

	guestRefs := make([]inventory.Ref, 0, len(snap.Guests))
	for ref := range snap.Guests {
		guestRefs = append(guestRefs, ref)
	}
	sort.Slice(guestRefs, func(i, j int) bool { return guestRefs[i].String() < guestRefs[j].String() })
	for _, ref := range guestRefs {
		rows = append(rows, firewallRow(snap, inventory.FwScopeGuest, ref.Node, snap.Guests[ref]))
	}

	// T-3103: vnet-scope rulesets, sorted the same way guest refs are above
	// (fw.Snapshot.VNets is a plain map — unordered iteration).
	vnetRefs := make([]inventory.Ref, 0, len(snap.VNets))
	for ref := range snap.VNets {
		vnetRefs = append(vnetRefs, ref)
	}
	sort.Slice(vnetRefs, func(i, j int) bool { return vnetRefs[i].String() < vnetRefs[j].String() })
	for _, ref := range vnetRefs {
		rows = append(rows, firewallRow(snap, inventory.FwScopeVNet, ref.String(), snap.VNets[ref]))
	}

	return rows
}

func firewallRow(snap fw.Snapshot, scope inventory.FwScope, node string, rs *inventory.FwRuleset) FirewallRow {
	gates := fw.ScopeBanners(snap, scope, node, rs)
	banners := make([]string, len(gates))
	for i, g := range gates {
		banners[i] = g.Message
	}
	return FirewallRow{
		Scope: string(scope), Ref: rs.String(), Enabled: rs.Enabled,
		DefaultIn: rs.DefaultIn, DefaultOut: rs.DefaultOut,
		RuleCount: len(rs.Rules), Banners: banners,
	}
}

// mtuOf prefers the live (netlink-observed) MTU, falling back to the
// declared (interfaces-file/PVE-network) value when the runtime figure
// isn't known — e.g. a peer node this daemon has no host-poller reach to
// in a degraded/single-collector environment. Reporting 0 in that case
// would misdocument a perfectly good, just-not-yet-observed MTU.
func mtuOf(runtime, declared int) int {
	if runtime > 0 {
		return runtime
	}
	return declared
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// AnnotationRows converts internal/annotate's read model into export rows.
// It performs no expiry or orphan logic of its own — both are already
// decided by the read that produced notes (T-2806 AC2/AC3 live in exactly
// one place, internal/annotate) — it only renders timestamps for display.
func AnnotationRows(notes []annotate.Note) []AnnotationRow {
	if len(notes) == 0 {
		return nil
	}
	out := make([]AnnotationRow, 0, len(notes))
	for _, n := range notes {
		out = append(out, AnnotationRow{
			Ref: n.Ref, Content: n.Content, CreatedBy: n.CreatedBy,
			Created: stampOf(n.CreatedAt), Expires: stampOf(n.ExpiresAt),
			Orphaned: n.Orphaned,
		})
	}
	return out
}

// RegionRows converts internal/annotate's regions into export rows.
func RegionRows(regions []annotate.Region) []RegionRow {
	if len(regions) == 0 {
		return nil
	}
	out := make([]RegionRow, 0, len(regions))
	for _, r := range regions {
		out = append(out, RegionRow{
			Label: r.Label, CreatedBy: r.CreatedBy,
			Created: stampOf(r.CreatedAt), Expires: stampOf(r.ExpiresAt),
			X: r.X, Y: r.Y, W: r.W, H: r.H,
		})
	}
	return out
}

// stampOf renders a unix second as a UTC RFC3339 date, mapping the 0
// sentinel ("never expires", and an unset timestamp) to "".
func stampOf(unixSec int64) string {
	if unixSec == 0 {
		return ""
	}
	return time.Unix(unixSec, 0).UTC().Format(time.RFC3339)
}

func vidsString(vids []inventory.VidRange) string {
	if len(vids) == 0 {
		return "none"
	}
	parts := make([]string, len(vids))
	for i, v := range vids {
		if v.Low == v.High {
			parts[i] = fmt.Sprintf("%d", v.Low)
		} else {
			parts[i] = fmt.Sprintf("%d-%d", v.Low, v.High)
		}
	}
	return strings.Join(parts, ",")
}
