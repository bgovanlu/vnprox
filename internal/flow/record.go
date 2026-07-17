package flow

import "strconv"

// Source names the protocol a Record was decoded from — one of the four
// wire decoders in this package, or (T-1004, a later task) a host-local
// sampler.
type Source string

const (
	SourceSFlow     Source = "sflow"
	SourceNetFlow5  Source = "netflow5"
	SourceNetFlow9  Source = "netflow9"
	SourceIPFIX     Source = "ipfix"
	SourceConntrack Source = "conntrack"
)

// Record is the normalized shape every decoder in this package produces
// (docs/api.md's Flows section documents this field-for-field): one
// observed flow/conversation sample, with srcRef/dstRef populated only when
// the address genuinely resolves against the live inventory graph — never
// guessed (see resolve.go).
type Record struct {
	// Node is the cluster node this sample was observed on (the listener's
	// own node — an exporter's agent/observation-domain address is not
	// necessarily a cluster node itself, e.g. a physical switch).
	Node  string `json:"node"`
	SrcIP string `json:"srcIp"`
	DstIP string `json:"dstIp"`
	// SrcRef/DstRef are inventory Ref strings (kind:node:id) — populated
	// only when SrcIP/DstIP resolves against a known guest or subnet in the
	// live inventory graph (see Resolver); left empty otherwise, never
	// guessed.
	SrcRef string `json:"srcRef,omitempty"`
	DstRef string `json:"dstRef,omitempty"`
	Source Source `json:"source"`

	// At is the observation time, unix seconds. For sFlow (which carries no
	// per-sample wall-clock timestamp of its own) this is the time the
	// listener received the datagram; for NetFlow/IPFIX it is the
	// exporter's own reported unix-seconds header field.
	At      int64 `json:"at"`
	Bytes   int64 `json:"bytes"`
	Packets int64 `json:"packets"`

	SrcPort int `json:"srcPort,omitempty"`
	DstPort int `json:"dstPort,omitempty"`
	// Proto is the IP protocol number (IANA "Assigned Internet Protocol
	// Numbers": 1=icmp, 6=tcp, 17=udp, 58=icmpv6, ...).
	Proto int `json:"proto"`
	// VLAN is the observed 802.1Q VLAN id, 0 if not carried/known by this
	// sample.
	VLAN int `json:"vlan,omitempty"`
	// IngressIfIndex/EgressIfIndex are the exporter's own SNMP ifIndex
	// values for the observed ingress/egress interface, 0 if not carried.
	IngressIfIndex int `json:"ingressIfIndex,omitempty"`
	EgressIfIndex  int `json:"egressIfIndex,omitempty"`
}

// protoNames maps the small set of IP protocol numbers this package's
// GET /flows ?protocol= filter (internal/api/flows.go) and the UI need a
// human name for. Not exhaustive — an unrecognized protocol number is
// still stored/returned by its numeric value; ProtoNumberFromName only
// recognizes a filter value spelled as one of these names or falls back to
// parsing it as a raw number (see internal/api/flows.go).
var protoNames = map[int]string{
	1:   "icmp",
	6:   "tcp",
	17:  "udp",
	58:  "icmpv6",
	132: "sctp",
}

// ProtoName returns the lowercase conventional name for an IP protocol
// number (e.g. 6 -> "tcp"), or its decimal string when unrecognized.
func ProtoName(proto int) string {
	if name, ok := protoNames[proto]; ok {
		return name
	}
	return strconv.Itoa(proto)
}

// ProtoNumberFromName is ProtoName's inverse for the small recognized set,
// used by the ?protocol= query filter (internal/api/flows.go) so a filter
// value can be either the name ("tcp") or the raw number ("6").
func ProtoNumberFromName(name string) (int, bool) {
	for n, s := range protoNames {
		if s == name {
			return n, true
		}
	}
	return 0, false
}
