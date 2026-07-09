package topology

import (
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// DefaultSearchLimit caps GET /inventory/search results (docs/api.md:
// "fuzzy search across names, MACs, IPs, VMIDs, comments"). 50 keeps the
// spotlight dropdown (docs/features/topology.md §2's `/` hotkey) usable
// without the caller having to paginate a search-as-you-type query.
const DefaultSearchLimit = 50

const (
	scoreExact  = 100
	scorePrefix = 80
	scoreSubstr = 50
	scoreFuzzy  = 20
)

// Search ranks every entity in snap against q across names, MACs, IPs,
// VMIDs, and comments, returning at most DefaultSearchLimit hits ordered by
// descending score (exact/prefix matches before substring/fuzzy ones, per
// docs/features/topology.md §2: "results ranked").
func Search(snap inventory.Snapshot, q string) []SearchResult {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	needle := strings.ToLower(q)

	results := []SearchResult{}
	for _, e := range snap.All() {
		ref := e.GetRef()
		best, field := 0, ""
		for _, cand := range searchFields(snap, e) {
			if s := matchScore(needle, strings.ToLower(cand.value)); s > best {
				best, field = s, cand.name
			}
		}
		if best == 0 {
			continue
		}
		results = append(results, SearchResult{
			Ref:          ref.String(),
			Kind:         string(ref.Kind),
			Label:        labelOf(snap, e),
			Node:         ref.Node,
			MatchedField: field,
			Score:        best,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Ref < results[j].Ref
	})
	if len(results) > DefaultSearchLimit {
		results = results[:DefaultSearchLimit]
	}
	return results
}

type searchField struct{ name, value string }

// searchFields lists every documented-searchable string on e: names, MACs,
// IPs, VMIDs, comments (docs/api.md's GET /inventory/search line).
func searchFields(snap inventory.Snapshot, e inventory.Entity) []searchField {
	var out []searchField
	add := func(name, value string) {
		if value != "" {
			out = append(out, searchField{name, value})
		}
	}

	switch v := e.(type) {
	case *inventory.Node:
		add("name", v.Name)
		add("ip", v.IP)
	case *inventory.PhysNic:
		add("name", v.Name)
		add("mac", v.Mac)
	case *inventory.Bond:
		add("name", v.Name)
	case *inventory.Bridge:
		add("name", v.Name)
		add("comments", v.Comments)
		for _, a := range v.Addresses {
			add("address", a)
		}
	case *inventory.VlanIface:
		add("name", v.Name)
		for _, a := range v.Addresses {
			add("address", a)
		}
	case *inventory.SdnZone:
		add("id", v.ID)
	case *inventory.SdnVnet:
		add("id", v.ID)
		add("alias", v.Alias)
	case *inventory.SdnSubnet:
		add("id", v.ID)
		add("gateway", v.Gateway)
	case *inventory.Guest:
		add("name", v.Name)
		add("vmid", strconv.Itoa(v.VMID))
	case *inventory.GuestNic:
		add("mac", v.Mac)
		add("key", v.Key)
		if g, ok := snap.Get(v.Guest); ok {
			if guest, ok := g.(*inventory.Guest); ok {
				add("guestName", guest.Name)
				add("vmid", strconv.Itoa(guest.VMID))
			}
		}
	case *inventory.LldpNeighbor:
		add("chassisName", v.ChassisName)
		add("chassisId", v.ChassisID)
		add("mgmtIP", v.MgmtIP)
		add("portId", v.PortID)
	case *inventory.FwRuleset:
		// Not rendered on the map, but still a valid search target (e.g.
		// jumping to a ruleset by comment) — comments live per-rule, so
		// nothing entity-level to index beyond scope, which isn't a useful
		// search key. Intentionally no fields added.
	}
	return out
}

// matchScore ranks needle against a lowercased haystack: exact > prefix >
// substring > a light fuzzy (subsequence) match, 0 if none match at all.
func matchScore(needle, haystack string) int {
	switch {
	case haystack == needle:
		return scoreExact
	case strings.HasPrefix(haystack, needle):
		return scorePrefix
	case strings.Contains(haystack, needle):
		return scoreSubstr
	case isSubsequence(needle, haystack):
		return scoreFuzzy
	default:
		return 0
	}
}

// isSubsequence reports whether every rune of needle appears in haystack in
// order (not necessarily contiguously) — a cheap, allocation-free "fuzzy"
// match for typo-tolerant spotlight search.
func isSubsequence(needle, haystack string) bool {
	if needle == "" {
		return false
	}
	i := 0
	for _, r := range haystack {
		if i < len(needle) && rune(needle[i]) == r {
			i++
		}
	}
	return i == len(needle)
}
