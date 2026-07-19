// correlate.go implements T-1501's node<->guest correlation: a k8s node's
// reported InternalIP matched against addresses vnprox's own inventory
// already knows about (IPAM allocations / guest-agent-reported addresses)
// — "the same 'observed, never guessed' resolution gap docs/api.md's Flows
// section documents for guest-NIC IP resolution" (T-1501's own card
// wording). An unmatched node is surfaced as unmatched, never a wrong Ref.

package k8s

// GuestIPIndex resolves an address to the inventory Ref of the guest it
// belongs to, or ok=false when nothing in the live inventory/IPAM data
// claims that address. internal/api's k8s routes build this from the
// identical IPAM-allocation-based lookup GET /edge/nat and GET
// /ingress/status already use (buildGuestLookup in internal/api/edge.go) —
// no second address->guest resolution path.
type GuestIPIndex func(ip string) (guestRef string, ok bool)

// NodeCorrelation is one k8s node's correlation result.
type NodeCorrelation struct {
	// K8sNode is the k8s Node object's own name.
	K8sNode string `json:"k8sNode"`
	// InternalIP is the address correlation was attempted against ("" if
	// the node reported none).
	InternalIP string `json:"internalIp,omitempty"`
	// GuestRef is the resolved PVE guest's inventory Ref string, "" when
	// unmatched.
	GuestRef string `json:"guestRef,omitempty"`
	// Matched is true iff GuestRef was resolved from a genuine index hit —
	// distinguishes "unmatched" from "matched to an empty-string ref",
	// which cannot happen, but keeps the JSON shape's intent unambiguous
	// for a frontend that shouldn't have to treat "" specially.
	Matched bool `json:"matched"`
}

// CorrelateNodes resolves every node's InternalIP against index, in the
// same order nodes were given. index may be nil (every node then surfaces
// unmatched — the same nil-dependency degraded-mode convention every
// other optional index/lookup in this codebase follows, e.g.
// internal/api/edge.go's EdgeIPAMSource).
func CorrelateNodes(nodes []Node, index GuestIPIndex) []NodeCorrelation {
	out := make([]NodeCorrelation, 0, len(nodes))
	for _, n := range nodes {
		ip := n.InternalIP()
		nc := NodeCorrelation{K8sNode: n.Metadata.Name, InternalIP: ip}
		if index != nil && ip != "" {
			if ref, ok := index(ip); ok && ref != "" {
				nc.GuestRef = ref
				nc.Matched = true
			}
		}
		out = append(out, nc)
	}
	return out
}
