package flow

import (
	"net"
	"strings"
	"sync"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Resolver resolves an IP address to an inventory Ref string where the
// address genuinely matches something in the live inventory graph — never a
// guess (docs/api.md's Flows section: "srcRef/dstRef are resolved against
// the inventory graph ... where the IP matches a known guest or subnet,
// left unset otherwise").
type Resolver interface {
	Resolve(ip string) (ref string, ok bool)
}

// subnetEntry is one CIDR this package knows how to resolve an address
// within, and the Ref an address inside it resolves to.
type subnetEntry struct {
	network *net.IPNet
	ref     string
}

// GraphResolver implements Resolver over a snapshot of the live inventory
// graph: it indexes every Bridge's declared address CIDR and every SdnSubnet
// (matched to its owning SdnVnet) once per Refresh call, then answers
// Resolve by linear CIDR containment scan (the subnet/vnet count in any
// realistic cluster is small — tens, not thousands — so this is not worth a
// trie).
//
// Deliberately guest-nic-level-blind: internal/inventory's Graph does not
// carry guest IP addresses at all (the same documented gap
// internal/sim.SimulateResult already discloses for the path simulator —
// docs/api.md's "guest IPs are not currently carried in the inventory
// graph"), so this resolver can only ever land on a Bridge or SdnVnet Ref,
// never a GuestNic Ref, however precisely an address matches a known
// subnet. Per this package's "never guessed" contract, this is the honest
// answer given what the graph actually carries; wiring a guest-IP-aware
// enrichment source (e.g. T-1004's host samplers, which observe live
// conntrack/ARP data) into a second Resolver stage is a documented follow-up
// (see planning/reports/T-1002.md).
type GraphResolver struct {
	subnets []subnetEntry
	mu      sync.RWMutex
}

// NewGraphResolver builds an empty GraphResolver; call Refresh (or
// RefreshFromGraph) at least once before Resolve returns anything.
func NewGraphResolver() *GraphResolver {
	return &GraphResolver{}
}

// InventorySnapshot is the read-only view Refresh needs — *inventory.Graph
// satisfies it directly via its own Snapshot method's return value.
type InventorySnapshot interface {
	All() []inventory.Entity
}

// InventoryGraph is the small seam RefreshFromGraph needs — *inventory.Graph
// satisfies it directly (Snapshot() inventory.Snapshot, and
// inventory.Snapshot satisfies InventorySnapshot above).
type InventoryGraph interface {
	Snapshot() inventory.Snapshot
}

// RefreshFromGraph re-indexes the resolver from graph's current snapshot —
// the convenience form callers with a live *inventory.Graph use (e.g.
// Service's periodic refresh, listener.go).
func (g *GraphResolver) RefreshFromGraph(graph InventoryGraph) {
	if graph == nil {
		return
	}
	g.Refresh(graph.Snapshot().All())
}

// Refresh re-indexes the resolver from entities (a live inventory snapshot's
// full entity list). Safe to call concurrently with Resolve (RWMutex-backed
// swap).
func (g *GraphResolver) Refresh(entities []inventory.Entity) {
	vnetRefByID := map[string]string{} // "<zone>/<vnetID>" style vnet name -> vnet Ref string
	var subnets []subnetEntry

	// First pass: bridges (immediately resolvable) and a zone/vnet id ->
	// Ref index (SdnSubnet only carries its owning vnet's bare name, not a
	// full Ref, so subnets need this to resolve their own Ref target).
	for _, e := range entities {
		switch v := e.(type) {
		case *inventory.Bridge:
			for _, addr := range v.Addresses {
				if _, ipnet, err := net.ParseCIDR(addr); err == nil {
					subnets = append(subnets, subnetEntry{network: ipnet, ref: v.String()})
				}
			}
		case *inventory.SdnVnet:
			vnetRefByID[v.ID] = v.String()
		}
	}

	// Second pass: SDN subnets, resolved to their owning vnet's Ref (the
	// vnet is the closest graph entity to "where this subnet's traffic
	// actually rides" without a per-node realized-bridge lookup this
	// package doesn't have enough data to do honestly — see GraphResolver's
	// doc comment).
	for _, e := range entities {
		sub, ok := e.(*inventory.SdnSubnet)
		if !ok {
			continue
		}
		_, ipnet, err := net.ParseCIDR(sub.ID) // SdnSubnet.ID is the CIDR (docs/data-model.md §3)
		if err != nil {
			continue
		}
		ref, ok := vnetRefByID[sub.Vnet]
		if !ok {
			continue // subnet names a vnet this snapshot doesn't (yet) have — never guess a Ref
		}
		subnets = append(subnets, subnetEntry{network: ipnet, ref: ref})
	}

	g.mu.Lock()
	g.subnets = subnets
	g.mu.Unlock()
}

// Resolve implements Resolver: the first indexed CIDR (bridges before SDN
// subnets, in Refresh's own build order) containing ip wins — an address
// legitimately inside two overlapping subnets is not expected in a
// well-formed cluster, and this package does not attempt to adjudicate that
// case beyond "first match".
func (g *GraphResolver) Resolve(ip string) (string, bool) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, s := range g.subnets {
		if s.network.Contains(parsed) {
			return s.ref, true
		}
	}
	return "", false
}

// ResolveRecord fills rec.SrcRef/DstRef in place from r (nil-safe: a nil
// Resolver leaves both empty, the same "no resolver wired yet" degraded
// mode every other optional dependency in this codebase gets).
func ResolveRecord(r Resolver, rec *Record) {
	if r == nil {
		return
	}
	if ref, ok := r.Resolve(rec.SrcIP); ok {
		rec.SrcRef = ref
	}
	if ref, ok := r.Resolve(rec.DstIP); ok {
		rec.DstRef = ref
	}
}
