package evpn

// Status is docs/api.md's GET /sdn/evpn/status response shape.
type Status struct {
	Nodes       []NodeStatus     `json:"nodes"`
	ExitNodes   []ExitNodeHealth `json:"exitNodes"`
	Findings    []Finding        `json:"findings"`
	FailedNodes []string         `json:"failedNodes,omitempty"`
	GeneratedAt int64            `json:"generatedAt"`
	Partial     bool             `json:"partial,omitempty"`
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
// (docs/features/sdn.md §3: "exit-node health").
type ExitNodeHealth struct {
	Zone    string `json:"zone"`
	Node    string `json:"node"`
	Detail  string `json:"detail,omitempty"`
	Healthy bool   `json:"healthy"`
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
