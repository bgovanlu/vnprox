// Package mtuprobe implements T-1306's active per-path MTU discovery:
// binary-search DF-set (Don't Fragment) ICMP probes along each path the
// latency & loss mesh (internal/latmesh, T-1303) already knows about,
// producing a verified (measured) MTU per link on a coarser interval than
// latency probing — upgrading the config-derived MTU findings T-803 already
// ships (health_vxlanmtu.go's vxlan_underlay_mtu) with a live, measured
// annotation.
//
// # Reuses T-1303's infrastructure, does not duplicate it
//
// This package deliberately does not implement a second pair-discovery
// mechanism or a second scheduler:
//
//   - Path discovery: Service.Config.Discoverer is exactly
//     internal/latmesh.Discoverer — in production the same *latmesh.
//     GraphDiscoverer instance latmesh.Service itself uses (wired at the
//     cmd/vnproxd composition root), so "each path already known to the
//     mesh" is literal, not aspirational. Pair/LinkID (internal/latmesh's
//     own types) are reused verbatim as this package's own path identity —
//     a measured MTU reading is keyed by the identical LinkID a latency
//     reading for the same path already uses.
//   - Scheduling: Service.RunLoop calls latmesh.RunTicker, the exact
//     ticker-loop primitive latmesh.Service.RunLoop/RunPruneLoop themselves
//     use (extracted to internal/latmesh/scheduler.go by this task
//     specifically so this package has a real, shared implementation to
//     call rather than a hand-rolled second one — see that file's doc
//     comment). cmd/vnproxd registers Service.RunLoop as its own supervised
//     run-group actor on its own coarser interval ([mtuprobe]
//     probe_interval_sec, default 300s) — a second *goroutine*, since the
//     two probe families run on genuinely different cadences, but not a
//     second scheduler *implementation*.
//
// # Scope: reuses latmesh's own documented fabric narrowing
//
// This task's card asks for probing "along each bridge/bond/VXLAN-EVPN
// path". internal/latmesh's own doc.go already narrowed its Discoverer's
// scope from a four-fabric wish list down to two concretely-modeled ones:
// "corosync" (corosync ring addresses) and "guest" (bridge names shared by
// two or more nodes — which is exactly the VXLAN/EVPN underlay path when
// that shared bridge is an SDN vxlan/evpn zone's own bridge). A bond is a
// node-local link-aggregation construct with no node-to-node IP path of its
// own to path-MTU-discover — there is nothing for a DF-probe binary search
// to target at a bond in isolation — so "bond" in the card's wish-list
// phrasing is satisfied by probing the bridge/VNet path *carried over* that
// bond's member links, not by a third, separate bond-keyed pair set. This
// mirrors T-1303's own "quietly narrower scope, flagged in the report"
// precedent rather than inventing a new, unverified path-discovery
// mechanism this codebase has no substrate for yet.
//
// # Current-state, not a ring
//
// Unlike latmesh's latency_samples ring (a time series, queried for
// rolling stats/history), this package holds only each link's *current*
// verified MTU reading in memory (Service.Results/Result) — MTU rarely
// changes, so there is no rolling-window/history use case to serve, and no
// new SQLite table this task's context docs asked for. A daemon restart
// simply loses the last reading until the next probe tick re-establishes
// it — no different in kind from every other in-memory "current status"
// seam in this codebase (e.g. internal/host's corosync status read).
//
// # WireGuard hook (declared, not wired)
//
// See wireguard.go: Service.ProbeWireGuardLink is the exact seam Phase 14's
// T-1401 should wire a real implementation into once WireGuard tunnels
// exist as a modeled inventory entity and a WireGuard capability flag
// exists. It always returns ErrWireGuardNotAvailable today, gated behind
// WireGuardCapability (always false) so the seam stays dark — this task's
// card explicitly declares it "not blocking".
package mtuprobe
