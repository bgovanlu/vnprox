package inventory

import (
	"fmt"
	"strings"
)

// Kind names an inventory entity type. The set is the contract from
// docs/data-model.md §1; other packages (topology, api, change) key off
// these exact string values, so they must not change.
type Kind string

const (
	KindNode      Kind = "node"
	KindPhysNic   Kind = "physnic"
	KindBond      Kind = "bond"
	KindBridge    Kind = "bridge"
	KindVlan      Kind = "vlan"
	KindOVSBridge Kind = "ovs-bridge"
	KindOVSBond   Kind = "ovs-bond"
	KindSDNZone   Kind = "sdn-zone"
	KindSDNVnet   Kind = "sdn-vnet"
	KindSDNSubnet Kind = "sdn-subnet"
	// KindSDNDnsZone and KindSDNDnsRecord (T-1204: SDN DNS management) name a
	// PVE SDN DNS zone (a forward domain registered in /etc/pve/sdn/dns.cfg,
	// backed by a PowerDNS plugin instance) and one A/AAAA/PTR/CNAME/TXT
	// record within it. Both are cluster-scoped (empty Node, like every other
	// sdn-* kind — SDN config is pmxcfs-replicated). A DNS zone's ID is its
	// domain ("example.com"); a record's ID is the "<zone>/<name>/<type>"
	// composite (params_sdn_dns.go's op-target convention). They are
	// live-polled inventory entities (ingested by the SDN poll,
	// internal/collect.pollSDN) but are deliberately NOT rendered as
	// topology-map nodes (topology.layerOf returns false for them) — the map
	// surfaces a matching record only as a guest's dnsName badge
	// (docs/features/sdn.md §6), not as a node of its own.
	KindSDNDnsZone   Kind = "sdn-dns-zone"
	KindSDNDnsRecord Kind = "sdn-dns-record"
	KindGuest        Kind = "guest"
	KindGuestNic     Kind = "guest-nic"
	KindLldpNeighbor Kind = "lldp-neighbor"
	KindFwRuleset    Kind = "fw-ruleset"
	// KindQosShape (T-1505: QoS & traffic shaping) names a qos.shape.*
	// changeset op's target: a bridge-level tc/HTB shape, node-scoped with a
	// caller-chosen id. It has no dedicated live-polled inventory entity of
	// its own (like KindFwRuleset's fw.alias/ipset/group members) — its
	// entire state lives in the app-owned qos_shapes store table
	// (internal/store/migrations, docs/data-model.md §3), never a shadow
	// copy of live tc state.
	KindQosShape Kind = "qos-shape"

	// KindWgTunnel / KindWgPeer are T-1401's WireGuard op-target kinds. They
	// are app-owned intent (docs/data-model.md §2's wireguard_tunnels/
	// wireguard_peers tables), not live-polled inventory entities the way
	// every kind above is — no collector ever emits one into the graph. They
	// exist here only so a wg.* changeset op's target Ref (docs/data-model.md
	// §3) can be a first-class, parseable Ref like every other op target,
	// keyed by the tunnel's app-store id (KindWgTunnel) or "<tunnelID>/<peer
	// public key hash>" (KindWgPeer). The tunnel/peer lives on its owning
	// node, so Node is the owning PVE node, never empty.
	KindWgTunnel Kind = "wg-tunnel"
	KindWgPeer   Kind = "wg-peer"
	// KindNatRule and KindStaticRoute (T-1403: Edge & NAT cockpit) name a
	// nat.masquerade/nat.portforward rule and a route.static route,
	// respectively — node-scoped, caller-chosen ids (docs/data-model.md §3),
	// with no interfaces(5) stanza of their own (they live inside an
	// *existing* iface's post-up/post-down lines — see
	// internal/change/ifaces/edgeop.go).
	KindNatRule     Kind = "nat-rule"
	KindStaticRoute Kind = "static-route"
	// KindVF names an SR-IOV virtual function (T-1506): a PhysNic acting as
	// a PF carries zero or more VFs (PhysNic.SRIOVVFs), each identified by
	// "<pfName>/vf<index>" within its owning node. A VF *is*
	// collector-observed (host-netlink), like PhysNic itself, but — like
	// Bridge.FDB/Bond.SlaveDetail — is not merge/provenance-tracked as a
	// top-level graph entity (see entity.go's PhysNic.SRIOVVFs doc
	// comment); this Kind exists purely so a VF has a first-class,
	// parseable Ref for changeset op targets (vf.provision targets its PF,
	// but a VF's own Ref is exposed in findings/inspector output),
	// mirroring the role KindLldpNeighbor plays for another
	// host-netlink-sourced, per-NIC observation.
	KindVF Kind = "vf"
	// KindCephOSD names a Ceph OSD (T-1503): read from PVE's own Ceph
	// config (GET /nodes/{node}/ceph/osd, internal/pve.Client.CephOSDs),
	// identified by "osd<id>" within its hosting node. Like KindVF, an OSD
	// is not merge/provenance-tracked as a top-level graph entity (it is
	// never emitted into internal/inventory.Graph at all — internal/ceph
	// computes its bond attribution live against the graph, the same
	// live-resolved-on-read pattern ResolveVFAssignments established for
	// VFs); this Kind exists purely so an OSD has a first-class, parseable
	// Ref for GET /ceph/status/inspector output, mirroring KindVF's role.
	KindCephOSD Kind = "ceph-osd"
	// KindPBSHost names a Proxmox Backup Server host (T-1206): discovered
	// read-only from PVE's own storage config (GET /storage, storage.cfg
	// entries of type "pbs") by internal/pbs.Discover — never a PBS API
	// client of its own, never a stored PBS credential. A pbs-host is NOT
	// merge/provenance-tracked as a top-level graph entity: no collector
	// ever emits one into internal/inventory.Graph. This Kind exists purely
	// so a PBS host has a first-class, parseable Ref for its GET /topology
	// synthetic node and GET /pbs inspector output. A PBS host is
	// cluster-scoped (its storage.cfg entry is cluster-wide config, shared
	// across nodes), so Node is empty and ID is the server's address
	// (internal/pbs.HostRef).
	KindPBSHost Kind = "pbs-host"

	// KindSwitchPort names a physical switch's port (T-1205: guarded switch
	// config push). It is app-owned intent, not a live-polled inventory
	// entity — no collector ever emits one into the graph; a switch is an
	// external device vnprox drives through a SwitchDriver
	// (internal/switchdrv), not a PVE node. This Kind exists only so a
	// switch.port.update changeset op's target Ref (docs/data-model.md §3) is
	// a first-class, parseable Ref like every other op target. It is
	// cluster-scoped (Node is empty — a switch is not a PVE node); ID encodes
	// "<switchID>/<port name>" (the app-store switch id from the switches
	// table plus the driver-native port identifier, e.g.
	// "sw-01ABC/Ethernet1/14"). The '/' is not structural to ParseRef (which
	// splits on only the first two ':'), so the port name may itself contain
	// '/' (common on chassis switches) and still round-trip.
	KindSwitchPort Kind = "switch-port"
)

// knownKinds is the closed set of valid Kind values. ParseRef rejects any
// kind not in this set so a malformed URL segment cannot fabricate an
// entity kind other packages will not understand.
var knownKinds = map[Kind]bool{
	KindNode: true, KindPhysNic: true, KindBond: true, KindBridge: true,
	KindVlan: true, KindOVSBridge: true, KindOVSBond: true, KindSDNZone: true,
	KindSDNVnet: true, KindSDNSubnet: true, KindGuest: true, KindGuestNic: true,
	KindLldpNeighbor: true, KindFwRuleset: true, KindQosShape: true,
	KindWgTunnel: true, KindWgPeer: true,
	KindNatRule: true, KindStaticRoute: true, KindVF: true, KindCephOSD: true,
	KindPBSHost:    true,
	KindSDNDnsZone: true, KindSDNDnsRecord: true,
	KindSwitchPort: true,
}

// Ref is the stable identity of one inventory entity: a (Kind, Node, ID)
// triplet. Node is empty for cluster-scoped entities (SDN, cluster
// firewall). ID is stable within (Kind, Node) — e.g. "vmbr0", "eno1",
// "zone1/vnet1" (docs/data-model.md §1).
//
// Ref is a comparable value type: it is used directly as a map key and
// compared with ==. Do not add non-comparable fields.
type Ref struct {
	Kind Kind
	Node string
	ID   string
}

// String encodes a Ref as "kind:node:id" (docs/api.md: entities use Ref
// triplets in URLs). The scheme is deliberately delimiter-simple:
//
//   - Kind is drawn from the closed knownKinds set and never contains ':'.
//   - Node is a PVE node hostname and never contains ':'.
//   - ID may contain any character, including '/' and ':' (e.g. an SDN
//     subnet's CIDR "sdn-subnet::2001:db8::/64", or a vnet path
//     "sdn-vnet::zone1/vnet1").
//
// Because only the first two ':' are structural, ParseRef recovers the ID
// verbatim regardless of how many ':' or '/' it contains — no percent
// encoding of the ID is required for the encoding itself to round-trip.
// When a Ref string is placed in a URL path segment, the HTTP layer is
// responsible for ordinary percent-encoding of reserved characters; that
// is orthogonal to this triplet encoding.
func (r Ref) String() string {
	return string(r.Kind) + ":" + r.Node + ":" + r.ID
}

// IsZero reports whether the Ref is the zero value (no kind).
func (r Ref) IsZero() bool { return r.Kind == "" && r.Node == "" && r.ID == "" }

// ClusterScoped reports whether the Ref names a cluster-wide entity (empty
// Node) — SDN objects and the cluster firewall ruleset.
func (r Ref) ClusterScoped() bool { return r.Node == "" }

// ParseRef decodes a "kind:node:id" string produced by Ref.String back into
// a Ref, recovering the exact original triplet. It splits on only the first
// two ':' so an ID containing ':' or '/' round-trips unchanged. It errors if
// the string has fewer than two ':' or names an unknown kind.
func ParseRef(s string) (Ref, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return Ref{}, fmt.Errorf("inventory: malformed ref %q: want kind:node:id", s)
	}
	k := Kind(parts[0])
	if !knownKinds[k] {
		return Ref{}, fmt.Errorf("inventory: ref %q has unknown kind %q", s, parts[0])
	}
	return Ref{Kind: k, Node: parts[1], ID: parts[2]}, nil
}

// MustParseRef is ParseRef that panics on error, for tests and static refs.
func MustParseRef(s string) Ref {
	r, err := ParseRef(s)
	if err != nil {
		panic(err)
	}
	return r
}
