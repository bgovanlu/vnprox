// SPDX-License-Identifier: Apache-2.0

package evpn

// Status is docs/api.md's GET /sdn/evpn/status response shape. Controllers
// (T-3102, additive per docs/architecture.md §10's "new optional fields may
// be added without a version bump") is the re-attachment: the same
// EVPN/BGP session data already gathered per node/peer above, regrouped by
// which SDN controller it belongs to rather than left inferable only from
// zone state — see ControllerHealth's doc comment for exactly what
// "belongs to" means here.
type Status struct {
	Nodes       []NodeStatus       `json:"nodes"`
	ExitNodes   []ExitNodeHealth   `json:"exitNodes"`
	Controllers []ControllerHealth `json:"controllers"`
	Findings    []Finding          `json:"findings"`
	FailedNodes []string           `json:"failedNodes,omitempty"`
	GeneratedAt int64              `json:"generatedAt"`
	Partial     bool               `json:"partial,omitempty"`
}

// NodeStatus is one cluster node's FRR observation. FRRInstalled is false
// (Peers/VNIs/RouterID/ASN all zero-valued, Error empty) for a node
// running no FRR at all — T-404 AC2's clean "no EVPN" case, distinct from
// Error being set (a real read/parse failure on a node that does have
// FRR).
type NodeStatus struct {
	Node         string `json:"node"`
	RouterID     string `json:"routerId,omitempty"`
	Error        string `json:"error,omitempty"`
	Peers        []Peer `json:"peers"`
	VNIs         []VNI  `json:"vnis"`
	ASN          int    `json:"asn,omitempty"`
	FRRInstalled bool   `json:"frrInstalled"`
}

// Peer is one BGP/EVPN peering session's detail: state (for the peering
// matrix's node×peer color), and prefixes/uptime/last-error (for the
// session detail panel) — docs/features/sdn.md §3.
type Peer struct {
	PeerAddr        string `json:"peerAddr"`
	PeerNode        string `json:"peerNode,omitempty"`
	AddressFamily   string `json:"addressFamily,omitempty"`
	State           string `json:"state"`
	StateReason     string `json:"stateReason,omitempty"`
	RemoteAS        int    `json:"remoteAs,omitempty"`
	PfxRcd          int    `json:"pfxRcd,omitempty"`
	PfxSnt          int    `json:"pfxSnt,omitempty"`
	UptimeSecs      int64  `json:"uptimeSecs,omitempty"`
	FlapTransitions int    `json:"flapTransitions,omitempty"`
}

// VNI is one EVPN VNI observed on a node.
type VNI struct {
	Type      string `json:"type"`
	VxlanIf   string `json:"vxlanIf,omitempty"`
	TenantVRF string `json:"tenantVrf,omitempty"`
	VNI       int    `json:"vni"`
	NumMacs   int    `json:"numMacs,omitempty"`
	NumArpND  int    `json:"numArpNd,omitempty"`
}

// ExitNodeHealth is one EVPN zone exit node's derived health
// (docs/features/sdn.md §3: "exit-node health"). Controller (T-3102,
// additive) names the zone's own `controller` reference when it resolves to
// a real internal/sdn.Tree.Controllers entry — "" when the zone has none
// set or the id doesn't resolve, so an older/absent controller never turns
// this into a missing-field error, only an unattributed one.
type ExitNodeHealth struct {
	Zone       string `json:"zone"`
	Node       string `json:"node"`
	Controller string `json:"controller,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Healthy    bool   `json:"healthy"`
}

// ControllerHealth is one SDN controller's BGP/EVPN peering health (T-3102
// acceptance criterion 3: "EVPN/BGP status attaches to the controller
// rather than being inferred"). Before this, session state
// (NodeStatus.Peers) was reported purely per observing node, and exit-node
// health (ExitNodeHealth) was derived purely from zone state — a
// controller's own health had no place of its own to live and had to be
// inferred by cross-referencing both. This type attaches it directly: for
// a bgp/evpn controller, Peers is matched against every observed
// NodeStatus.Peer whose PeerAddr appears in the controller's own
// configured peer address list (internal/sdn.Controller.Peers) — the same
// underlying FRR sessions ExitNodeHealth/NodeStatus already report, viewed
// by controller identity instead of by node/zone identity. A faucet/isis
// controller (no BGP peer list to match against) still appears, with an
// empty Peers/Healthy=true (nothing to be unhealthy about) rather than
// being omitted — an operator asking "is this controller OK" should never
// get silence for an answer.
type ControllerHealth struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Detail  string   `json:"detail,omitempty"`
	Zones   []string `json:"zones,omitempty"`
	Peers   []string `json:"peers,omitempty"`
	Healthy bool     `json:"healthy"`
}

// Finding is a flapping-session health finding (docs/features/sdn.md §3:
// "Flapping sessions raise a health finding"), reusing internal/drift's
// {id, severity, detail, ...} finding vocabulary rather than inventing a
// new shape.
type Finding struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Node     string `json:"node"`
	PeerAddr string `json:"peerAddr"`
	Detail   string `json:"detail"`
}
