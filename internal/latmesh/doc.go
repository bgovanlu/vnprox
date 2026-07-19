// Package latmesh implements T-1303's continuous latency & loss mesh:
// low-rate ICMP/TCP probes between cluster nodes over every shared fabric
// it can identify, a bounded history ring (mirroring internal/flow's
// retention-window-AND-hard-row-cap bound), and a rolling-stats surface
// docs/features/monitoring.md §5's path_latency_degraded/path_loss health
// checks and T-806's verify-live UX both consume.
//
// This package extends internal/probe's existing engine rather than
// duplicating it: it reuses the same Outcome-honesty stance (a probe that
// could not be attempted or classified is never conflated with a genuine
// negative result) but not internal/probe.Run itself, since that function
// execs *inside a guest* via the QEMU guest agent — latmesh probes run
// host-to-host, directly from the vnproxd process on each node, which has
// no guest-agent indirection at all.
//
// # Fabric discovery scope (flagged deviation from the task card's wording)
//
// The card asks for probing "over each shared fabric it can identify
// (corosync, migration, storage, guest)". Two of those four are concretely
// modeled in this codebase today: corosync ring addresses
// (internal/host.CorosyncConfig, already read by T-803's
// corosync_link_degraded check) and shared bridges (internal/xnode.
// BridgesByName's existing cross-node grouping — a bridge name present on
// two or more nodes is a real shared L2 segment guests attach to, the
// "guest" fabric). Neither PVE's migration network (datacenter.cfg
// "migration: network=...") nor a distinct storage network is modeled
// anywhere in internal/inventory or internal/pve yet — there is no reader
// for either. Rather than guess an unverified shape for two fabrics no
// other package in this codebase has ever parsed, this task's Discoverer
// identifies exactly the two fabrics it can: "corosync" and "guest"
// (labeled per shared bridge name). Adding migration/storage network
// readers is a documented follow-up (planning/reports/T-1303.md), not
// implemented here — the same "quietly narrower scope, flagged in the
// report" precedent T-803's corosync_link_degraded check already set for
// its own local-node-only fan-out gap.
//
// # Cluster scope (node-local only)
//
// A single node's Discoverer only ever produces pairs with itself as
// FromNode (docs/architecture.md §7's cluster-aware invariant is satisfied
// because *every* cluster node runs its own vnproxd with its own
// Discoverer — the full mesh is the union of every node's own outbound
// pairs, not one node coordinating every other). GET /latmesh/heatmap is
// therefore node-local only, the same documented scope
// GET /metrics/history's Prometheus exporter and T-803's corosync check
// carry — a cluster-wide merged view needs a new peer route, which this
// task's card did not include in its context docs (no Peer API section is
// listed) and is left as a follow-up.
package latmesh
