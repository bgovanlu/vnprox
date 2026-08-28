// SPDX-License-Identifier: Apache-2.0

// history.go implements T-3905's binding-history recorder and flap
// detector: "the ARP table now" -> "what changed and when."
//
// DESIGN NOTE, deliberately deviating from the task card's literal "consume
// the existing internal/neighbor service" wording — recorded here so the
// choice is visible, not silently made. T-3712 (peer.resultCache) coalesces
// repeat calls to Service.Neighbors within a short TTL of each other, which
// covers the within-one-findings-cycle collision it was built for
// (ipam/rogue calling it ~24ms apart). A fourth caller on its *own*,
// differently-phased ticker would routinely fall outside that TTL and
// reintroduce exactly the "N timers hit the same peer endpoint" problem
// T-3712 fixed — so HistoryRecorder does not call Service.Neighbors (the
// cluster-fanned-out method) at all. Instead it reads only through the same
// nodeNeighborReader interface Service already uses for its *local* branch
// (cfg.Host, i.e. host.Reader), for this node's own name — zero peer
// traffic, so there is no coalescing question to defeat in the first place.
// This also matches the established storage architecture, not just avoids
// a problem: flow_samples (internal/flow) and metric_samples' documented
// sibling pattern is "each node's store holds only what that node observed
// locally; a cluster-wide view is assembled at READ time by fanning a peer
// route out and merging" (docs/architecture.md §7, docs/api.md's GET
// /flows: "flow_samples is node-local app data, so a cluster-wide view
// re-queries every peer and merges the pages") — never a second node's
// vnproxd polling peers and writing their data into its own local ring.
// neighbor_bindings follows flow_samples here, not metric_samples (whose
// collector *does* fan out and store peer data locally — the documented
// exception, not the rule). GET /neighbors/history (internal/api) is the
// read-time fan-out half of this design.
//
// Append-ON-CHANGE, not append-on-every-poll: Poll compares each
// currently-observed (ip, mac) against the most recently recorded MAC for
// that ip (Store.LatestByIP) and writes a row only when it's new or
// different — see 0050_neighbor_bindings.sql's header. A neighbor-cache
// entry that simply ages out of the kernel table (evicted, not rebound) is
// NOT recorded as a "removal": /proc/net/arp and the netlink neighbor table
// only ever list what the kernel currently happens to be tracking, so an
// entry's absence on one poll tick is not evidence the binding is gone —
// only a *different* MAC for the same IP is.

package neighbor

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// Default tunables for HistoryRecorder (docs/development.md pins no numeric
// value for this task). PollInterval mirrors internal/collect.Collector's
// DefaultHostInterval-order-of-magnitude cadence (this reads the exact same
// host.Reader.Neighbors seam that collector loop would, just on its own
// timer) — but see the package doc comment: this recorder never fans out to
// peers, so there is no coalescing budget to spend regardless of interval.
// Retention/MaxRows follow store.MetricSampleRepo's documented 24h window
// (a "what changed in the last day" timeline is the useful shape for this
// feature — a 60-minute flow_samples-style window would cut off exactly the
// "flapped yesterday evening, investigated this morning" case a binding
// timeline exists for) plus flow_samples' belt-and-suspenders hard row cap,
// sized down for append-ON-CHANGE's much lower expected row rate than
// append-every-poll: a stable small cluster writes only a handful of rows a
// day, so 50,000 rows is generous headroom even for a genuinely flapping
// binding (T-3905's own failure mode) without risking the unbounded growth
// CLAUDE.md forbids.
const (
	DefaultHistoryPollInterval  = 15 * time.Second
	DefaultHistoryPruneInterval = time.Minute
	DefaultHistoryRetention     = 24 * time.Hour
	DefaultHistoryMaxRows       = 50_000
)

// Flap thresholds. Both windows match arp_spoof_suspected's own
// arpChurnWindow (internal/findings/health_rogue.go) deliberately: it is
// the closest existing precedent for "how fast is too fast" over exactly
// this kind of ARP/ND churn, and picking a different number here with no
// stated reason would just be noise between two checks describing the same
// underlying physical phenomenon (see health_neighborflap.go's doc comment
// for how this check relates to arp_spoof_suspected).
//
//   - IPFlapThreshold=3 within IPFlapWindow: matches arpChurnThreshold
//     exactly (a single lease handoff/DHCP rebind is one transition and
//     never fires; only rapid oscillation trips it).
//   - MACClaimThreshold=5 within MACClaimWindow: no existing precedent
//     (arp_spoof_suspected does not track this direction at all — it has no
//     per-MAC view, only per-IP). Set higher than IPFlapThreshold on
//     purpose: a boot storm or a DHCP server handing out several leases in
//     quick succession can legitimately put 2-3 distinct IPs through one
//     MAC's history in two minutes (a hypervisor's own management NIC
//     picking up VLAN-tagged addresses, a NIC re-provisioned across guests).
//     Five is comfortably above that ordinary churn while still catching a
//     MAC flooding/claiming many addresses in the same short window a real
//     ARP-spoofing or MAC-flooding attempt would need to pull off.
const (
	IPFlapWindow      = 2 * time.Minute
	IPFlapThreshold   = 3
	MACClaimWindow    = 2 * time.Minute
	MACClaimThreshold = 5
)

// HistoryStore is the subset of *store.NeighborBindingRepo HistoryRecorder
// needs, declared as an interface so tests can substitute an in-memory
// fake without a real SQLite file — the same seam internal/flow.FlowStore
// establishes over *store.FlowSampleRepo.
type HistoryStore interface {
	Insert(ctx context.Context, b store.NeighborBinding) error
	LatestByIP(ctx context.Context, node string) (map[string]store.NeighborBinding, error)
	Query(ctx context.Context, filter store.NeighborBindingFilter, cursor string, limit int) ([]store.NeighborBinding, string, error)
	PruneOlderThan(ctx context.Context, cutoff int64) (int64, error)
	PruneToCap(ctx context.Context, maxRows int64) (int64, error)
	CandidateIPsSince(ctx context.Context, node string, since int64) ([]string, error)
	CandidateMACsSince(ctx context.Context, node string, since int64) ([]string, error)
	CountSince(ctx context.Context, node, ip, mac string, since int64) (int64, error)
	DistinctIPsSince(ctx context.Context, node, mac string, since int64) ([]string, error)
}

// HistoryConfig configures a HistoryRecorder.
type HistoryConfig struct {
	// Host is this node's own local-only neighbor-table reader — the same
	// nodeNeighborReader interface Service.Config.Host satisfies (in
	// production, the identical host.Reader instance). Never a
	// peer-fanning reader: see this file's package doc comment.
	Host nodeNeighborReader
	// Store is the app-owned persistence seam (in production,
	// store.NewNeighborBindingRepo(db)). A nil Store makes every method a
	// documented no-op, the same degraded-mode contract every other
	// optional Config field in this codebase uses.
	Store HistoryStore
	// LocalNode returns this daemon's own PVE node name, or "" before the
	// PVE poller has discovered it yet — mirrors Service.Config's
	// identically-named field exactly.
	LocalNode func() string
	// Now overrides time.Now for tests, the same clock-injection
	// convention internal/flow.Config.Now uses.
	Now    func() time.Time
	Logger *slog.Logger

	PollInterval  time.Duration
	PruneInterval time.Duration
	Retention     time.Duration
	MaxRows       int64
}

// HistoryRecorder records IP<->MAC binding transitions for the local node
// into HistoryStore (Poll/RunPollLoop) and detects flapping bindings over
// that recorded history (Flaps).
// Field order is densest-pointer-first (log, now are bare pointers; cfg is a
// large struct with a pointer-free tail), which is what govet's
// fieldalignment measures.
type HistoryRecorder struct {
	log *slog.Logger
	now func() time.Time
	cfg HistoryConfig
}

// NewHistoryRecorder builds a HistoryRecorder from cfg, defaulting unset
// tunables.
func NewHistoryRecorder(cfg HistoryConfig) *HistoryRecorder {
	if cfg.LocalNode == nil {
		cfg.LocalNode = func() string { return "" }
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultHistoryPollInterval
	}
	if cfg.PruneInterval <= 0 {
		cfg.PruneInterval = DefaultHistoryPruneInterval
	}
	if cfg.Retention <= 0 {
		cfg.Retention = DefaultHistoryRetention
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = DefaultHistoryMaxRows
	}
	return &HistoryRecorder{cfg: cfg, log: cfg.Logger, now: cfg.Now}
}

// BindingChange is one transition Poll decided to record this tick —
// returned for tests and for any caller (none in production today) that
// wants the delta without re-querying the store.
type BindingChange struct {
	store.NeighborBinding
	// FirstSeen is true when this (node, ip) had no prior recorded MAC
	// (PrevMAC is NULL) — a discovery, not a rebind.
	FirstSeen bool
}

// Poll reads the local node's current neighbor table (via cfg.Host, never a
// peer) and records exactly the (ip, mac) pairs that are new or changed
// relative to Store.LatestByIP, returning what it recorded. A nil
// Host/Store or an undiscovered local node (LocalNode() == "") is a
// documented no-op, degrading the same way every optional seam in this
// codebase does — this feature simply has nothing to record yet.
func (r *HistoryRecorder) Poll(ctx context.Context) ([]BindingChange, error) {
	if r.cfg.Host == nil || r.cfg.Store == nil {
		return nil, nil
	}
	node := r.cfg.LocalNode()
	if node == "" {
		return nil, nil
	}

	observed, err := r.cfg.Host.Neighbors(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("neighbor: reading local neighbor table for history: %w", err)
	}
	latest, err := r.cfg.Store.LatestByIP(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("neighbor: reading latest recorded bindings for %s: %w", node, err)
	}

	now := r.now()
	var changes []BindingChange
	for _, n := range observed {
		if n.IP == "" || n.MAC == "" {
			continue
		}
		prior, seenBefore := latest[n.IP]
		if seenBefore && prior.MAC == n.MAC {
			continue // unchanged: the common case, most ticks write nothing
		}

		b := store.NeighborBinding{
			At: now.Unix(), Node: node, IP: n.IP, MAC: n.MAC, Iface: n.Iface, State: n.State,
		}
		if seenBefore {
			b.PrevMAC = sql.NullString{String: prior.MAC, Valid: true}
		}
		if err := r.cfg.Store.Insert(ctx, b); err != nil {
			return changes, fmt.Errorf("neighbor: recording binding transition for %s/%s: %w", node, n.IP, err)
		}
		changes = append(changes, BindingChange{NeighborBinding: b, FirstSeen: !seenBefore})
	}
	return changes, nil
}

// RunPollLoop calls Poll every cfg.PollInterval until ctx is cancelled,
// logging failures rather than stopping the loop — the same
// keep-going-on-transient-error posture store.MetricSampleRepo.RunPruneLoop
// and internal/flow.Service.RunPruneLoop both use. A nil Store makes this
// an immediate no-op, mirroring RunPruneLoop below.
func (r *HistoryRecorder) RunPollLoop(ctx context.Context) error {
	if r.cfg.Store == nil {
		return nil
	}
	if _, err := r.Poll(ctx); err != nil {
		r.log.Warn("neighbor: history poll", "error", err)
	}
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := r.Poll(ctx); err != nil {
				r.log.Warn("neighbor: history poll", "error", err)
			}
		}
	}
}

// RunPruneLoop enforces this ring's documented bound (retention window AND
// hard row cap, whichever is smaller prunes first) every cfg.PruneInterval
// until ctx is cancelled — the same tick-based shape
// store.MetricSampleRepo.RunPruneLoop and internal/flow.Service.RunPruneLoop
// both use. A nil Store makes this an immediate no-op.
func (r *HistoryRecorder) RunPruneLoop(ctx context.Context) error {
	if r.cfg.Store == nil {
		return nil
	}
	r.prune(ctx) // prime immediately rather than waiting a full interval
	ticker := time.NewTicker(r.cfg.PruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.prune(ctx)
		}
	}
}

func (r *HistoryRecorder) prune(ctx context.Context) {
	cutoff := r.now().Add(-r.cfg.Retention).Unix()
	if _, err := r.cfg.Store.PruneOlderThan(ctx, cutoff); err != nil {
		r.log.Error("neighbor: pruning neighbor_bindings by retention window", "error", err)
	}
	if _, err := r.cfg.Store.PruneToCap(ctx, r.cfg.MaxRows); err != nil {
		r.log.Error("neighbor: pruning neighbor_bindings to the hard row cap", "error", err)
	}
}

// Query serves GET /neighbors/history's local-node read directly off
// Store. A nil Store returns an empty result, not an error — the same
// degraded-mode treatment internal/flow.Service.Query gives a nil Store.
func (r *HistoryRecorder) Query(ctx context.Context, filter store.NeighborBindingFilter, cursor string, limit int) ([]store.NeighborBinding, string, error) {
	if r.cfg.Store == nil {
		return nil, "", nil
	}
	return r.cfg.Store.Query(ctx, filter, cursor, limit)
}

// FlapKind names which of T-3905's two flap directions a FlapEvent reports
// (see the package intro's "one IP rapidly changing MAC, or one MAC
// claiming many IPs").
type FlapKind string

const (
	// FlapKindIPChurn is one IP whose resolved MAC has changed
	// IPFlapThreshold or more times within IPFlapWindow.
	FlapKindIPChurn FlapKind = "ip_churn"
	// FlapKindMACClaim is one MAC that has been recorded as the owner of
	// MACClaimThreshold or more distinct IPs within MACClaimWindow.
	FlapKindMACClaim FlapKind = "mac_claim"
)

// FlapEvent is one binding that has crossed a flap threshold on the local
// node, as of the instant Flaps was called — recomputed fresh from Store
// each call, never itself persisted (the persisted state is the
// neighbor_bindings rows it's computed from; a flap verdict is a live
// read over that history, exactly like every other continuously-recomputed
// findings producer in this codebase).
type FlapEvent struct {
	Node string
	Kind FlapKind
	// IP is set for FlapKindIPChurn.
	IP string
	// MAC is set for FlapKindMACClaim (the claiming MAC).
	MAC string
	// IPs is the claimed IP set, set only for FlapKindMACClaim (a Detail
	// string needs the actual addresses, not just a count).
	IPs []string
	// Count is the window's transition count (ip_churn) or distinct-IP
	// count (mac_claim) — whichever number crossed the threshold.
	Count int
}

// Flaps evaluates both flap directions against the local node's recorded
// history as of now, returning every binding currently over threshold. A
// nil Store or undiscovered local node returns (nil, nil), the same
// no-op-not-error degradation Poll uses.
func (r *HistoryRecorder) Flaps(ctx context.Context, now time.Time) ([]FlapEvent, error) {
	if r.cfg.Store == nil {
		return nil, nil
	}
	node := r.cfg.LocalNode()
	if node == "" {
		return nil, nil
	}

	var out []FlapEvent

	ipSince := now.Add(-IPFlapWindow).Unix()
	ips, err := r.cfg.Store.CandidateIPsSince(ctx, node, ipSince)
	if err != nil {
		return nil, fmt.Errorf("neighbor: reading IP-churn candidates for %s: %w", node, err)
	}
	for _, ip := range ips {
		n, cntErr := r.cfg.Store.CountSince(ctx, node, ip, "", ipSince)
		if cntErr != nil {
			return nil, fmt.Errorf("neighbor: counting transitions for %s/%s: %w", node, ip, cntErr)
		}
		if n >= IPFlapThreshold {
			out = append(out, FlapEvent{Node: node, Kind: FlapKindIPChurn, IP: ip, Count: int(n)})
		}
	}

	macSince := now.Add(-MACClaimWindow).Unix()
	macs, err := r.cfg.Store.CandidateMACsSince(ctx, node, macSince)
	if err != nil {
		return nil, fmt.Errorf("neighbor: reading MAC-claim candidates for %s: %w", node, err)
	}
	for _, mac := range macs {
		claimedIPs, err := r.cfg.Store.DistinctIPsSince(ctx, node, mac, macSince)
		if err != nil {
			return nil, fmt.Errorf("neighbor: reading claimed IPs for %s/%s: %w", node, mac, err)
		}
		if len(claimedIPs) >= MACClaimThreshold {
			out = append(out, FlapEvent{Node: node, Kind: FlapKindMACClaim, MAC: mac, Count: len(claimedIPs), IPs: claimedIPs})
		}
	}

	return out, nil
}
