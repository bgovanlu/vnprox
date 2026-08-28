// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// DefaultInterval is the drift check cadence docs/features/topology.md §6
// specifies: "a background checker (30s)".
const DefaultInterval = 30 * time.Second

// Config configures a Service. Graph is required; the rest have defaults.
type Config struct {
	Graph *inventory.Graph
	// Pins is T-1102's pinned-spec seam (specdrift.go's CheckSpecDrift
	// family): nil (the pre-T-1102 default) simply means no spec is ever
	// pinned, so that check family always contributes zero findings — every
	// existing caller of drift.New (and every pre-T-1102 test) keeps working
	// unchanged with no pin wired in.
	Pins PinProvider
	// Logger defaults to slog.Default() if nil.
	Logger *slog.Logger
	// OnChange, if set, is invoked from RunLoop whenever the finding set
	// changes between cycles (by ID set, not by count alone — see
	// RunLoop's doc comment), with the new finding count — the hook
	// cmd/vnproxd wires to broadcast docs/api.md's `drift.changed {count}`
	// WS event. Never invoked with an unchanged set, and never invoked
	// concurrently with itself.
	OnChange func(count int)
	// Interval defaults to DefaultInterval when <= 0.
	Interval time.Duration
}

// Service runs the five drift check families (see doc.go) over a live
// *inventory.Graph, on demand (Findings) and on a periodic cycle
// (RunLoop), and computes fixing-changeset op patches for fixable findings
// (FixOps).
type Service struct {
	graph    *inventory.Graph
	log      *slog.Logger
	onChange func(int)
	pins     PinProvider
	lastIDs  map[string]bool
	interval time.Duration
	mu       sync.Mutex
	lastEval bool
}

// New builds a Service from cfg.
func New(cfg Config) *Service {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Service{
		graph:    cfg.Graph,
		log:      logger,
		onChange: cfg.OnChange,
		pins:     cfg.Pins,
		interval: interval,
	}
}

// checkFuncs is every check family, in a fixed order so Findings' output is
// grouped by check even though every individual check already sorts its
// own findings by ID.
var checkFuncs = []func(inventory.Snapshot) []Finding{
	checkBridgeDivergence,
	checkMTUConsistency,
	checkSDNRealization,
	checkSDNZoneStatus,
	checkPendingInterfaces,
	checkFileRuntimeDivergence,
	checkVFSpoofcheckMismatch,
}

// Findings runs every check family fresh against the graph's current
// snapshot and returns the combined, deterministically-ordered result.
// Pure with respect to the snapshot for the five original families: calling
// it twice against an unchanged graph returns identical findings (same IDs,
// same order, same content — T-305 acceptance criterion 5's "stable-key
// dedup"). The T-1102 spec_drift family additionally depends on s.pins'
// current pin (specdrift.go), so Findings as a whole is pure with respect to
// (snapshot, pin state) together, not the snapshot alone.
func (s *Service) Findings() []Finding {
	snap := s.graph.Snapshot()
	var out []Finding
	for _, fn := range checkFuncs {
		out = append(out, fn(snap)...)
	}
	out = append(out, s.specDriftFindings(snap)...)
	out = append(out, s.reconcileFindings(snap)...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RestoreIntentOps returns T-2703's "restore intent" op patch for the finding
// with the given id — the ops that bring the cluster back to what the spec
// declares — and a suggested changeset title. ok is false when no current
// finding has that id, or the finding does not offer the action.
//
// Like FixOps it recomputes fresh against the current snapshot, so the ops
// always reflect live state rather than a cached value, and like FixOps it is
// a LOOKUP: the caller names a finding, never an op list. Staging remains the
// change engine's job — nothing here applies anything.
func (s *Service) RestoreIntentOps(id string) (ops []change.Op, title string, ok bool) {
	for _, f := range s.Findings() {
		if f.ID != id {
			continue
		}
		if f.Reconcile == nil || !f.Reconcile.Actions.RestoreIntent || len(f.fixOps) == 0 {
			return nil, "", false
		}
		return f.fixOps, f.fixTitle, true
	}
	return nil, "", false
}

// AdoptRealityRefs returns the entities an "adopt reality" proposal for the
// finding with the given id would rewrite in the spec document, plus the
// finding's own detail for the pull-request body. ok is false when no current
// finding has that id, or the finding does not offer the action.
//
// It returns refs, not a document: rendering the document is internal/spec's
// job and proposing it is internal/gitsync's, and this package holds neither a
// git credential nor a way to reach a host.
func (s *Service) AdoptRealityRefs(id string) (refs []inventory.Ref, detail string, ok bool) {
	for _, f := range s.Findings() {
		if f.ID != id {
			continue
		}
		if f.Reconcile == nil || !f.Reconcile.Actions.AdoptReality || len(f.adoptRefs) == 0 {
			return nil, "", false
		}
		return append([]inventory.Ref(nil), f.adoptRefs...), f.Detail, true
	}
	return nil, "", false
}

// FixOps returns the computed fixing-changeset op patch for the finding
// with the given ID (re-running the checks fresh against the current
// snapshot, so the ops always reflect live state rather than a possibly
// stale cached value), and a suggested changeset title. ok is false when
// no current finding has that ID, or the finding has no computable fix.
func (s *Service) FixOps(id string) (ops []change.Op, title string, ok bool) {
	for _, f := range s.Findings() {
		if f.ID != id {
			continue
		}
		if !f.Fixable || len(f.fixOps) == 0 {
			return nil, "", false
		}
		return f.fixOps, f.fixTitle, true
	}
	return nil, "", false
}

// RunLoop drives the periodic drift cycle on s.interval until ctx is
// cancelled, matching cmd/vnproxd's runGroup actor signature. Every cycle
// recomputes Findings and compares the resulting ID set against the
// previous cycle's; OnChange fires (with the new count) only when the set
// actually changed — including the very first cycle, which always
// "changes" from the empty pre-start state, so the nav badge/WS event
// reflects reality immediately on startup rather than waiting a full
// interval. This ID-set comparison (not just a count comparison) is the
// package's hysteresis mechanism: two different findings replacing each
// other while the count stays flat still fires OnChange, while a genuinely
// unchanged cycle never does (T-305 acceptance criterion 5).
func (s *Service) RunLoop(ctx context.Context) error {
	s.cycle(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.cycle(ctx)
		}
	}
}

func (s *Service) cycle(_ context.Context) {
	findings := s.Findings()
	ids := make(map[string]bool, len(findings))
	for _, f := range findings {
		ids[f.ID] = true
	}

	s.mu.Lock()
	changed := !s.lastEval || !sameIDSet(s.lastIDs, ids)
	s.lastIDs = ids
	s.lastEval = true
	s.mu.Unlock()

	if changed {
		s.log.Info("drift: findings changed", "count", len(findings))
		if s.onChange != nil {
			s.onChange(len(findings))
		}
	}
}

func sameIDSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}
