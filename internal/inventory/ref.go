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
	KindNode         Kind = "node"
	KindPhysNic      Kind = "physnic"
	KindBond         Kind = "bond"
	KindBridge       Kind = "bridge"
	KindVlan         Kind = "vlan"
	KindOVSBridge    Kind = "ovs-bridge"
	KindOVSBond      Kind = "ovs-bond"
	KindSDNZone      Kind = "sdn-zone"
	KindSDNVnet      Kind = "sdn-vnet"
	KindSDNSubnet    Kind = "sdn-subnet"
	KindGuest        Kind = "guest"
	KindGuestNic     Kind = "guest-nic"
	KindLldpNeighbor Kind = "lldp-neighbor"
	KindFwRuleset    Kind = "fw-ruleset"

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
)

// knownKinds is the closed set of valid Kind values. ParseRef rejects any
// kind not in this set so a malformed URL segment cannot fabricate an
// entity kind other packages will not understand.
var knownKinds = map[Kind]bool{
	KindNode: true, KindPhysNic: true, KindBond: true, KindBridge: true,
	KindVlan: true, KindOVSBridge: true, KindOVSBond: true, KindSDNZone: true,
	KindSDNVnet: true, KindSDNSubnet: true, KindGuest: true, KindGuestNic: true,
	KindLldpNeighbor: true, KindFwRuleset: true, KindWgTunnel: true, KindWgPeer: true,
	KindNatRule: true, KindStaticRoute: true,
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
