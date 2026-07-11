// fdb.go implements T-306's MAC/FDB browser support: flattening the
// per-bridge FDB tables embedded in Links()' BridgeDetail into a single
// bridge-tagged list, the shape the peer API's GET /api/peer/host/fdb route
// (docs/features/lldp-discovery.md §4) and internal/collect's ingestion
// path both want. This file has no build tag and does no I/O of its own —
// it is a pure function over an already-fetched []LinkState, so it works
// identically for Real (netlink_linux.go) and FixtureReader (fixture.go)
// results.

package host

// FDBRow is one bridge forwarding-database entry, flattened out of its
// owning bridge's LinkState.Bridge.FDB and tagged with that bridge's name.
type FDBRow struct {
	Bridge    string
	Mac       string
	Port      string
	Vlan      int
	Master    bool
	Permanent bool
	Stale     bool
}

// FlattenFDB flattens every bridge-kind link's FDB out of links into a
// single bridge-tagged list, skipping non-bridge links and bridges with no
// FDB detail (nil Bridge, or a bridge with an empty table).
func FlattenFDB(links []LinkState) []FDBRow {
	var out []FDBRow
	for _, l := range links {
		if l.Bridge == nil {
			continue
		}
		for _, e := range l.Bridge.FDB {
			out = append(out, FDBRow{
				Bridge:    l.Name,
				Mac:       e.Mac,
				Port:      e.Port,
				Vlan:      e.Vlan,
				Master:    e.Master,
				Permanent: e.Permanent,
				Stale:     e.Stale,
			})
		}
	}
	return out
}
