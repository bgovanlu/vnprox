package topology

import (
	"regexp"
	"strconv"
)

// T-3504: fold Proxmox's per-guest-NIC firewall bridges into the guest NIC
// they belong to, instead of rendering them as switches of their own.
//
// What an `fwbr` is, from the live node rather than from documentation
// (planning/reports/evidence/pve-9.2.4-firewall-bridges.txt): when a guest
// NIC carries `firewall=1` — which is the PVE GUI's *default* when adding a
// NIC — `pve-firewall` creates `fwbr<vmid>i<netid>` at guest start with
// exactly two members, `fwln<vmid>i<netid>` (the leg toward the real bridge)
// and the guest's own `veth<vmid>i<netid>`/`tap<vmid>i<netid>`. It is a
// per-NIC bump in the wire for hanging iptables rules on, not a bridge in the
// sense an operator means when they make one, and `/etc/network/interfaces`
// on the reference node mentions `fwbr` zero times.
//
// Why folding, and not "collapse behind a disclosure" or "hide behind a
// toggle" — the other two dispositions T-3504 offered: both of those keep
// drawing a device with nothing in it. An `fwbr`'s two members are the
// runtime-owned interfaces vnprox deliberately does not model as entities
// (the same class T-3502 taught internal/drift to ignore), and the guest
// NIC's own `attached-to` edge points at the *logical* bridge the guest is
// configured for (`vmbr0`), never at the `fwbr`. So these chassis are
// structurally empty and cannot be fixed by populating them — the only
// rendering that is true is the one that says "this guest NIC is firewalled",
// which is exactly what the bridge's name already encodes.
//
// Nothing is hidden that could not otherwise be found: the entity stays in
// the inventory (GET /inventory/{ref} still answers for it), and the guest
// NIC gains a `firewall=<name>` badge naming the bridge, so the relationship
// is discoverable from the map (T-3504 AC2).
//
// The fold is conditional on the guest NIC actually being in the projection.
// An `fwbr` with no matching guest NIC is a real failure mode — a bridge left
// behind after a guest stopped, or one whose guest is filtered out of this
// view — and in that state it is not "chrome the operator didn't create", it
// is an orphan worth looking at. Those keep rendering as their own node.

// fwbrName matches `fwbr<vmid>i<netid>`, the only shape pve-firewall creates
// (PVE::Network's $compute_fwbr_names). A hand-made bridge that merely starts
// with "fwbr" — `fwbr-dmz`, say — does not match and is left alone: this must
// never swallow a bridge an operator built.
var fwbrName = regexp.MustCompile(`^fwbr(\d+)i(\d+)$`)

// firewallBridgeOwner returns the Ref string of the guest NIC an `fwbr`
// bridge node belongs to, and whether the node is such a bridge at all.
//
// The guest NIC key is reconstructed as `net<netid>`, which is PVE's own
// naming for both QEMU and LXC NICs — an LXC NIC's *interface* name inside
// the container may be anything (`net0: name=eth0,...` on the reference
// node), but its config key, and therefore its inventory Ref, is `netN`.
func firewallBridgeOwner(n Node) (string, bool) {
	if n.Kind != "bridge" || n.NodeGroup == "" {
		return "", false
	}
	m := fwbrName.FindStringSubmatch(n.Label)
	if m == nil {
		return "", false
	}
	// Both submatches are `\d+`, so ParseInt cannot fail; the round trip
	// through int is what normalises a hypothetical `fwbr0103i0`.
	vmid, err := strconv.Atoi(m[1])
	if err != nil {
		return "", false
	}
	netid, err := strconv.Atoi(m[2])
	if err != nil {
		return "", false
	}
	return "guest-nic:" + n.NodeGroup + ":" + strconv.Itoa(vmid) + "/net" + strconv.Itoa(netid), true
}

// foldFirewallBridges removes every foldable `fwbr` node, badges its guest
// NIC with `firewall=<bridge name>`, and drops any edge that touched a
// removed node. Returns the surviving nodes and edges.
func foldFirewallBridges(nodes []Node, edges []Edge) ([]Node, []Edge) {
	// Index the guest NICs present in this projection, so the "orphan stays
	// visible" rule above can be decided in one pass.
	nicPresent := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.Kind == "guest-nic" {
			nicPresent[n.ID] = true
		}
	}

	// Decide everything before rewriting anything: which bridges fold, and
	// which badge each owning NIC gains. Doing it in one pass would mean
	// mutating a node that may already have been copied into the output,
	// which is the kind of ordering bug that only shows up when an fwbr
	// happens to sort after its guest NIC.
	folded := make(map[string]bool)
	badgeFor := make(map[string]string)
	for _, n := range nodes {
		owner, ok := firewallBridgeOwner(n)
		if !ok {
			continue
		}
		if !nicPresent[owner] {
			// Orphaned fwbr — keep it drawn. See this file's doc comment.
			continue
		}
		folded[n.ID] = true
		badgeFor[owner] = "firewall=" + n.Label
	}
	if len(folded) == 0 {
		return nodes, edges
	}

	out := nodes[:0:0] // fresh backing array: nodes is the caller's
	for _, n := range nodes {
		if folded[n.ID] {
			continue
		}
		if badge, ok := badgeFor[n.ID]; ok {
			n.Badges = append(append([]string{}, n.Badges...), badge)
		}
		out = append(out, n)
	}

	keptEdges := edges[:0:0]
	for _, e := range edges {
		if folded[e.From] || folded[e.To] {
			continue
		}
		keptEdges = append(keptEdges, e)
	}
	return out, keptEdges
}
