package latmesh

// Fabric names one shared network this package probes across — see doc.go
// for why only these two (of the card's four-fabric wish list) are
// concretely identified.
type Fabric string

const (
	FabricCorosync Fabric = "corosync"
	FabricGuest    Fabric = "guest"
)

// Pair is one directed node-to-node probe target on one shared fabric,
// always originating at the local node (doc.go's "node-local only" scope
// note) — FromNode is always the node whose Discoverer produced it.
type Pair struct {
	// LinkID is the stable, globally-unique key for this directed link:
	// "<fabric>[:<label>]|<fromNode>-><toNode>". Computed by ComputeLinkID,
	// never hand-assembled by a caller, so store rows/findings/heatmap
	// entries always agree on the exact same string for the exact same
	// link.
	LinkID string
	// Label distinguishes multiple links on the same fabric between the
	// same two nodes (e.g. corosync ring0 vs ring1, or two different
	// same-named-bridge groups) — "" when a fabric has no sub-label.
	Label    string
	Fabric   Fabric
	FromNode string
	ToNode   string
	// FromAddr/ToAddr are the concrete addresses this pair's probe dials
	// (ring address for corosync, the bridge's own configured address for
	// guest) — ToAddr is what Prober.Probe actually targets; FromAddr is
	// carried for diagnostic/detail-string purposes only. Either may be ""
	// when the source data has no address for that node (the corosync
	// config always does; a guest bridge with no configured IP does not),
	// in which case ToNode's *name* is still resolvable by whatever DNS/
	// hosts mechanism the cluster already relies on for node-to-node
	// traffic — Prober implementations should treat an empty ToAddr as
	// "dial ToNode by name", not as an error.
	FromAddr string
	ToAddr   string
}

// ComputeLinkID derives p's LinkID from its Fabric/Label/FromNode/ToNode —
// exported so tests and Discoverer implementations share exactly one
// derivation.
func ComputeLinkID(fabric Fabric, label, fromNode, toNode string) string {
	key := string(fabric)
	if label != "" {
		key += ":" + label
	}
	return key + "|" + fromNode + "->" + toNode
}

// Reading is one probe attempt's classified outcome — the unit Prober.Probe
// returns and Scheduler.Tick persists as one Sample row. Unlike
// internal/probe.Result, there is no separate "error" outcome bucket:
// a latency-mesh probe that could not be attempted at all (dial failure,
// DNS failure, prober-internal error) is reported as 100% loss for that
// tick, the honest "we tried and got nothing back" reading a mesh already
// needs to represent regardless of exactly *why* nothing came back — see
// RealProber's doc comment for the one caveat (a hard local error, e.g. the
// probe binary is missing, is instead returned as a Go error so Scheduler
// can log it distinctly from a genuine network-level loss).
type Reading struct {
	// RttMs is the round-trip time in milliseconds, averaged over
	// whichever successful probes this tick sent (a burst, for the real
	// implementation — see RealProber). Meaningless (left at its zero
	// value) when LossPct is 100.
	RttMs float64
	// LossPct is this tick's own loss percentage (0-100), not a rolling
	// average — Service.Heatmap/LinkStats computes the rolling figure from
	// a window of these per-tick readings.
	LossPct float64
}

// Sample is one persisted (or queried-back) probe reading, the shape both
// Scheduler.Tick writes and Service.History/Heatmap read — mirrors
// store.LatencySample field-for-field (internal/store never imports this
// package; see toStoreSample/fromStoreSample).
type Sample struct {
	LinkID   string
	Fabric   Fabric
	FromNode string
	ToNode   string
	At       int64 // unix seconds
	RttMs    float64
	LossPct  float64
}

// LinkHeat is one link's current-plus-rolling status — GET /latmesh/heatmap's
// per-item shape (internal/api/latmesh.go) and the exact shape
// internal/findings.LatMeshProvider consumes (Service.LatMeshHeatmap
// satisfies that interface structurally, no adapter needed — the same
// "small interface, real type satisfies it directly" seam
// internal/metrics.Sampler.Live establishes for MetricsProvider).
type LinkHeat struct {
	LinkID   string
	Fabric   Fabric
	FromNode string
	ToNode   string
	// At/RttMs/LossPct are the most recent single sample's own values.
	At      int64
	RttMs   float64
	LossPct float64
	// RollingRttMs/RollingLossPct are the mean over the configured rolling
	// window (Service Config.RollingWindow) — what the health checks and
	// the heatmap's color scale actually key on, since a single sample is
	// too noisy to fire a finding from directly.
	RollingRttMs   float64
	RollingLossPct float64
	SampleCount    int
}
