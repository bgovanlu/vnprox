// fdb.go implements T-306's MAC/FDB browser (docs/features/lldp-discovery.md
// §4): a cluster-wide, ownership-labeled view over every Bridge entity's
// forwarding-database table. The data itself is already cluster-wide by the
// time it reaches this package — T-303's peer fan-out feeds host-netlink
// Links() observations for every reachable cluster node into the same
// inventory graph this file reads (internal/collect's hostPollOnce), the
// same way PhysNic/Bridge/Bond entities already are — so no additional
// fan-out is needed here, exactly like Ports()/LLDPNeighbors() in this
// package need none either.

package topology

import (
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Ownership labels an FDB entry's Mac may resolve to.
const (
	OwnerGuest       = "guest"
	OwnerVnproxKnown = "vnprox-known"
	OwnerUnknown     = "unknown"
)

// macOwner is the pre-indexed answer to "what does this normalized MAC
// belong to, elsewhere in the cluster-wide inventory".
type macOwner struct {
	owner string
	ref   string
	label string
}

// normalizeMac canonicalizes a MAC for comparison: uppercase, whitespace
// trimmed. Fixture/PVE data is written in either case (see
// parseGuestNic's strings.ToUpper vs. netlink's lowercase HardwareAddr
// rendering), so every lookup in this file goes through this first.
func normalizeMac(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// buildMacIndex indexes every GuestNic and PhysNic MAC in snap once, so
// FDB/FDBSearch/Detail's per-bridge enrichment is O(1) per entry instead of
// re-scanning the whole snapshot per row. Guest ownership always wins a
// same-MAC collision against a PhysNic (built second, skipped when already
// present): "this is guest X's NIC" is the more actionable answer to
// "where does this MAC live" than "some physical NIC also happens to
// report this address", and the two should never legitimately collide in a
// correctly-configured cluster anyway.
func buildMacIndex(snap inventory.Snapshot) map[string]macOwner {
	idx := map[string]macOwner{}
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok {
			continue
		}
		mac := normalizeMac(nic.Mac)
		if mac == "" {
			continue
		}
		label := nic.Key
		if g, ok := snap.Get(nic.Guest); ok {
			if guest, ok := g.(*inventory.Guest); ok && guest.Name != "" {
				label = guest.Name
			}
		}
		idx[mac] = macOwner{owner: OwnerGuest, ref: nic.Guest.String(), label: label}
	}
	for _, e := range snap.All() {
		p, ok := e.(*inventory.PhysNic)
		if !ok {
			continue
		}
		mac := normalizeMac(p.Mac)
		if mac == "" {
			continue
		}
		if _, exists := idx[mac]; exists {
			continue
		}
		idx[mac] = macOwner{owner: OwnerVnproxKnown, ref: p.GetRef().String(), label: p.Name}
	}
	return idx
}

// fdbRowsForBridge flattens one bridge entity's FDB table into enriched,
// idx-labeled FDBRow values (Score left zero — only FDBSearch populates
// it).
func fdbRowsForBridge(b *inventory.Bridge, idx map[string]macOwner) []FDBRow {
	if len(b.FDB) == 0 {
		return nil
	}
	ref := b.GetRef()
	out := make([]FDBRow, 0, len(b.FDB))
	for _, entry := range b.FDB {
		row := FDBRow{
			Node: ref.Node, Bridge: b.Name, BridgeRef: ref.String(),
			Mac: entry.Mac, Port: entry.Port, Vlan: entry.Vlan,
			Master: entry.Master, Permanent: entry.Permanent, Stale: entry.Stale,
			Owner: OwnerUnknown,
		}
		if o, ok := idx[normalizeMac(entry.Mac)]; ok {
			row.Owner, row.OwnerRef, row.OwnerLabel = o.owner, o.ref, o.label
		}
		out = append(out, row)
	}
	return out
}

// allFDBRows walks every Bridge entity in snap, enriching each one's FDB
// table via idx.
func allFDBRows(snap inventory.Snapshot, idx map[string]macOwner) []FDBRow {
	var out []FDBRow
	for _, e := range snap.All() {
		b, ok := e.(*inventory.Bridge)
		if !ok {
			continue
		}
		out = append(out, fdbRowsForBridge(b, idx)...)
	}
	return out
}

// FDB returns every bridge forwarding-database entry cluster-wide,
// ownership-labeled and sorted (node, bridge, mac) for a stable table
// order — the plain "browse everything" listing behind Tools → MAC search
// with no query typed yet.
func FDB(snap inventory.Snapshot) []FDBRow {
	out := allFDBRows(snap, buildMacIndex(snap))
	sort.Slice(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		if out[i].Bridge != out[j].Bridge {
			return out[i].Bridge < out[j].Bridge
		}
		return out[i].Mac < out[j].Mac
	})
	return out
}

// stripMacSeparators drops the punctuation a pasted MAC commonly carries
// (colons, dashes, dots as in Cisco's xxxx.xxxx.xxxx form, spaces) so a
// query like "aa24" matches "AA:24:11:..." without the caller needing to
// type separators at all.
func stripMacSeparators(s string) string {
	return strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(s)
}

// FDBSearch ranks every FDB entry cluster-wide against q (a full or
// partial MAC), reusing Search's own exact/prefix/substring/fuzzy scoring
// (search.go's matchScore) so every ranked-search surface in this package
// agrees on what "better match" means. Ordered by descending score, capped
// at DefaultSearchLimit (blank q returns nil, same "nothing typed yet"
// convention Search itself uses).
func FDBSearch(snap inventory.Snapshot, q string) []FDBRow {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	needle := strings.ToLower(stripMacSeparators(q))

	var out []FDBRow
	for _, row := range allFDBRows(snap, buildMacIndex(snap)) {
		haystack := strings.ToLower(stripMacSeparators(row.Mac))
		score := matchScore(needle, haystack)
		if score == 0 {
			continue
		}
		row.Score = score
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Mac < out[j].Mac
	})
	if len(out) > DefaultSearchLimit {
		out = out[:DefaultSearchLimit]
	}
	return out
}
