// Package wan implements T-1405's WAN & upstream health: per-uplink
// availability/latency/loss to operator-configured external reference
// targets, so an operator can tell "it's the ISP, not the cluster" apart
// from a real in-cluster network problem. Visibility and findings only —
// there is no WAN failover automation anywhere in this package, and no
// changeset op type or write route exists for uplink switching (T-1405 AC5;
// internal/change's TestNoWanFailoverOpType pins this structurally).
//
// # Reusing internal/latmesh's scheduler, not building a second one
//
// This package's own probe loop is *internal/latmesh.Service itself*,
// configured with this task's own Discoverer (TargetDiscoverer, emitting
// one Pair per configured node/uplink/reference-target triple instead of
// latmesh's cluster-node-pair discovery) and its own Ring (store.
// WanProbeSampleRepo, backing a new wan_probe_samples table rather than
// latency_samples — a WAN link's own natural jitter/loss profile and
// retention needs are not the LAN mesh's, so this task's [wan]
// probe_interval_sec/retention_minutes/max_rows tunables need their own
// bounded ring to apply to, not a shared one). Every other moving part —
// Tick's per-tick probe-and-persist cycle, the retention-window-AND-
// hard-row-cap prune bound, RunTicker's owned-goroutine scheduling
// primitive, Heatmap's current-plus-rolling aggregation — is the exact
// same *latmesh.Service code T-1303 already shipped and T-1306's
// internal/mtuprobe already set the precedent of reusing rather than
// forking. Even the wire-level probe mechanism is reused verbatim:
// latmesh.RealProber execs `ping` against Pair.ToAddr (falling back to
// Pair.ToNode by name), which works identically whether that name is a
// cluster node or an external hostname/IP — no second Prober
// implementation exists in this package.
//
// # Node-local scope (same documented gap T-1303/T-1306 already carry)
//
// A node only ever probes its own configured targets — the full cluster
// picture is the union of every node's own GET /wan/status, exactly like
// GET /latmesh/heatmap and GET /mtuprobe/results are already documented as
// node-local-only with no peer fan-out (this task's own context docs name
// no Peer API section either). GET/PUT /wan/targets therefore also operate
// on the requesting node's own local store — configuring a different
// node's targets from this node's API is a documented follow-up, not
// implemented here.
package wan
