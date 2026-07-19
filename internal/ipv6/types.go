package ipv6

// Segment is one node's per-interface IPv6 RA/DHCPv6 observation, attached
// to its known Bridge/SdnVnet context where resolvable (docs/api.md's
// GET /ipv6/segments response shape).
type Segment struct {
	Ref                  string   `json:"ref,omitempty"`
	Node                 string   `json:"node"`
	Iface                string   `json:"iface"`
	Kind                 string   `json:"kind,omitempty"` // "bridge" | "vnet" | ""
	Vnet                 string   `json:"vnet,omitempty"`
	Zone                 string   `json:"zone,omitempty"`
	Prefixes             []string `json:"prefixes,omitempty"`
	Vid                  int      `json:"vid,omitempty"`
	RouterLifetimeSec    int      `json:"routerLifetimeSec,omitempty"`
	RAPresent            bool     `json:"raPresent"`
	ManagedFlag          bool     `json:"managedFlag,omitempty"`
	OtherFlag            bool     `json:"otherFlag,omitempty"`
	DHCPv6ServerPresent  bool     `json:"dhcpv6ServerPresent,omitempty"`
	DHCPv6InferredFromRA bool     `json:"dhcpv6InferredFromRA,omitempty"`
}

// SegmentsResponse is GET /ipv6/segments' full response — mirrors
// GET /sdn/evpn/status's partial/failedNodes cluster-fan-out convention.
type SegmentsResponse struct {
	Items       []Segment `json:"items"`
	FailedNodes []string  `json:"failedNodes,omitempty"`
	GeneratedAt int64     `json:"generatedAt"`
	Partial     bool      `json:"partial,omitempty"`
}
