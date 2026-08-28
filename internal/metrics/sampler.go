// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// PersistBucket is the downsampling resolution history is stored at
// (docs/features/monitoring.md §2: "24h ring in SQLite ... 30s resolution
// after downsampling"). Only the first sample observed within each
// PersistBucket-wide, wall-clock-aligned window is written per ref; every
// later sample in the same window is a no-op write (the row would just be
// overwritten with a value close to what's already there — see Ingest).
const PersistBucket = 30 * time.Second

// Broadcaster is the seam Sampler uses to push `metrics.sample` events over
// the shared /api/ws connection — the same small-interface pattern
// internal/change.Broadcaster and internal/topology.driftBroadcaster use.
// *topology.Hub/*topology.Service satisfy it.
type Broadcaster interface {
	Broadcast(topic string, payload []byte)
}

// MetricStore is the subset of *store.MetricSampleRepo Sampler needs —
// declared as an interface so tests can substitute an in-memory fake
// without a real SQLite file.
type MetricStore interface {
	Insert(ctx context.Context, s store.MetricSample) error
	List(ctx context.Context, ref string, since, until int64) ([]store.MetricSample, error)
}

// Config configures a Sampler. Store and WS are both optional: a nil Store
// disables history persistence (live rates/WS still work); a nil WS
// disables the metrics.sample push (GET /metrics/live and /metrics/history
// still work).
type Config struct {
	Store  MetricStore
	WS     Broadcaster
	Logger *slog.Logger
	// Now overrides time.Now for tests (time-travel retention/downsampling
	// tests) — see internal/change.LocalTimerConfig.Now for the identical
	// pattern this mirrors. Ingest itself is always given an explicit `at`
	// by its caller (collect's host loop already has a timestamp), so Now
	// is only used internally... it is exposed for symmetry/future use and
	// so tests can construct a Sampler the same way every other clock-
	// injecting type in this codebase is constructed.
	Now func() time.Time
}

// liveEntry is the most recently observed state for one Ref: its raw
// counters (for computing the next rate), its most recently computed rate
// (zero until a second sample arrives), link speed for utilization, and —
// for a Bond — its slave list for the per-slave balance view.
type liveEntry struct {
	at        time.Time
	slaves    []slaveMeta
	counters  Counters
	rates     Rates
	speedMbps int
	hasRates  bool
}

// Sampler ingests raw counter snapshots and serves live rates, history, and
// WS pushes. Safe for concurrent use.
type Sampler struct {
	store         MetricStore
	ws            Broadcaster
	log           *slog.Logger
	now           func() time.Time
	live          map[string]*liveEntry
	lastPersisted map[string]int64
	mu            sync.Mutex
}

// New constructs a Sampler from cfg.
func New(cfg Config) *Sampler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Sampler{
		store:         cfg.Store,
		ws:            cfg.WS,
		log:           logger,
		now:           now,
		live:          map[string]*liveEntry{},
		lastPersisted: map[string]int64{},
	}
}

// Ingest processes one node's raw counter snapshot at time at (collect's
// host-loop tick, local or peer-fanned-out): for every sampleable interface
// in links with a matching entry in stats, it updates the live rate cache,
// broadcasts a metrics.sample WS event (once a previous sample exists to
// diff against), and persists a downsampled counter row to Store on first
// observation of each new PersistBucket window.
func (s *Sampler) Ingest(ctx context.Context, node string, at time.Time, links []host.LinkState, stats map[string]host.IfaceStats) {
	metas := refMetasFromLinks(node, links)
	for _, m := range metas {
		raw, ok := stats[m.Ref.ID]
		if !ok {
			continue
		}
		cur := countersFromIfaceStats(raw)
		s.ingestOne(ctx, m, at, cur)
	}
}

func (s *Sampler) ingestOne(ctx context.Context, m refMeta, at time.Time, cur Counters) {
	refStr := m.Ref.String()

	s.mu.Lock()
	prev, existed := s.live[refStr]
	var rates Rates
	hasRates := false
	if existed {
		dt := at.Sub(prev.at)
		if dt > 0 {
			rates = ComputeRates(prev.counters, cur, dt)
			hasRates = true
		}
	}
	s.live[refStr] = &liveEntry{
		at: at, counters: cur, rates: rates, hasRates: hasRates,
		speedMbps: m.SpeedMbps, slaves: m.Slaves,
	}
	s.mu.Unlock()

	if hasRates && s.ws != nil {
		s.broadcastSample(m.Ref, at, rates)
	}
	s.maybePersist(ctx, refStr, at, cur)
}

// maybePersist writes cur to Store iff at falls in a PersistBucket window
// not yet persisted for ref, downsampling many 5s ticks to one row every
// 30s. Bucket alignment truncates to wall-clock PersistBucket boundaries so
// independent refs/nodes land on the same grid.
func (s *Sampler) maybePersist(ctx context.Context, ref string, at time.Time, cur Counters) {
	if s.store == nil {
		return
	}
	bucket := at.Truncate(PersistBucket).Unix()

	s.mu.Lock()
	last, seen := s.lastPersisted[ref]
	shouldPersist := !seen || bucket > last
	if shouldPersist {
		s.lastPersisted[ref] = bucket
	}
	s.mu.Unlock()

	if !shouldPersist {
		return
	}
	if err := s.store.Insert(ctx, toMetricSample(ref, bucket, cur)); err != nil {
		s.log.Error("metrics: persisting downsampled sample", "ref", ref, "at", bucket, "error", err)
	}
}

// sampleEvent is docs/api.md's documented `metrics.sample {ref, at, rates}`
// WS event, plus the flat "event" name field every server->client message
// on the shared /api/ws connection carries.
type sampleEvent struct {
	Event string `json:"event"`
	Ref   string `json:"ref"`
	At    int64  `json:"at"`
	Rates Rates  `json:"rates"`
}

func (s *Sampler) broadcastSample(ref inventory.Ref, at time.Time, rates Rates) {
	refStr := ref.String()
	data, err := json.Marshal(sampleEvent{Event: "metrics.sample", Ref: refStr, At: at.Unix(), Rates: rates})
	if err != nil {
		s.log.Error("metrics: marshaling metrics.sample event", "ref", refStr, "error", err)
		return
	}
	s.ws.Broadcast("metrics:"+refStr, data)
}

// SlaveRate is one bond slave's own current rate + LACP/MII active state
// (docs/features/monitoring.md §1: "Bond member balance shown per-slave —
// spot the LACP hash imbalance instantly").
type SlaveRate struct {
	Ref    string `json:"ref"`
	Active bool   `json:"active"`
	Rates  Rates  `json:"rates"`
}

// LiveMetric is one entity's current rate snapshot, the GET /metrics/live
// and Live() response shape.
type LiveMetric struct {
	Ref            string      `json:"ref"`
	Slaves         []SlaveRate `json:"slaves,omitempty"`
	Rates          Rates       `json:"rates"`
	At             int64       `json:"at"`
	SpeedMbps      int         `json:"speedMbps,omitempty"`
	RxUtilPct      float64     `json:"rxUtilPct,omitempty"`
	TxUtilPct      float64     `json:"txUtilPct,omitempty"`
	UtilizationPct float64     `json:"utilizationPct,omitempty"`
}

// Live returns the current rate snapshot for each of refs that has been
// sampled at least twice (once to seed, once to diff). Refs never seen, or
// seen only once, are omitted from the result (rather than returned with a
// misleading all-zero rate) — callers should treat a missing ref as "no
// live data yet".
func (s *Sampler) Live(refs []string) []LiveMetric {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]LiveMetric, 0, len(refs))
	for _, ref := range refs {
		e, ok := s.live[ref]
		if !ok || !e.hasRates {
			continue
		}
		out = append(out, s.toLiveMetricLocked(ref, e))
	}
	return out
}

// toLiveMetricLocked renders one liveEntry, resolving bond slave refs
// against the same live cache (must be called with s.mu held).
func (s *Sampler) toLiveMetricLocked(ref string, e *liveEntry) LiveMetric {
	rxUtil := UtilizationPct(e.rates.RxBps, e.speedMbps)
	txUtil := UtilizationPct(e.rates.TxBps, e.speedMbps)
	util := rxUtil
	if txUtil > util {
		util = txUtil
	}
	lm := LiveMetric{
		Ref: ref, At: e.at.Unix(), Rates: e.rates,
		SpeedMbps: e.speedMbps, RxUtilPct: rxUtil, TxUtilPct: txUtil, UtilizationPct: util,
	}
	if len(e.slaves) == 0 {
		return lm
	}
	parsed, err := inventory.ParseRef(ref)
	if err != nil {
		return lm
	}
	slaves := make([]SlaveRate, 0, len(e.slaves))
	for _, sl := range e.slaves {
		slaveRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: parsed.Node, ID: sl.Name}
		se, ok := s.live[slaveRef.String()]
		sr := SlaveRate{Ref: slaveRef.String(), Active: sl.Active}
		if ok {
			sr.Rates = se.rates
		}
		slaves = append(slaves, sr)
	}
	lm.Slaves = slaves
	return lm
}

// HistoryPoint is one 30s-resolution history sample rendered as a rate
// (the delta against the previous stored sample), the GET /metrics/history
// response's per-point shape (docs/features/monitoring.md §2: "Inspector
// charts: rate over time, errors/drops over time").
type HistoryPoint struct {
	At    int64 `json:"at"`
	Rates Rates `json:"rates"`
}

// History returns rate points for ref between fromTs and toTs inclusive
// (unix seconds), derived from the stored downsampled counter ring. The
// first stored row in range has no predecessor to diff against and is
// dropped rather than reported with a bogus/zero rate — callers wanting
// that leading edge's rate should widen fromTs by one PersistBucket.
func (s *Sampler) History(ctx context.Context, ref string, fromTs, toTs int64) ([]HistoryPoint, error) {
	if s.store == nil {
		return nil, nil
	}
	rows, err := s.store.List(ctx, ref, fromTs, toTs)
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, nil
	}
	points := make([]HistoryPoint, 0, len(rows)-1)
	for i := 1; i < len(rows); i++ {
		dt := time.Duration(rows[i].At-rows[i-1].At) * time.Second
		rates := ComputeRates(countersFromRow(rows[i-1]), countersFromRow(rows[i]), dt)
		points = append(points, HistoryPoint{At: rows[i].At, Rates: rates})
	}
	return points, nil
}

// CounterSnapshot is one entity's most recently observed raw counters —
// T-1001's Prometheus exporter needs the cumulative counters themselves
// (Prometheus does its own rate()/increase() math over successive scrapes),
// not the pre-computed Rates LiveMetric/Live carry.
type CounterSnapshot struct {
	Ref      inventory.Ref
	Counters Counters
	At       int64
}

// AllCounters returns the most recently observed raw counters for every ref
// the sampler has ingested at least once. Unlike Live, this has no
// "sampled at least twice" requirement — a scrape wants "what does this
// node see right now" even for a ref observed only once, and Prometheus's
// own rate() naturally produces no data point until it has two scrapes to
// diff, so there is no equivalent need to hide a single-sample ref here.
// Results are sorted by ref string for a deterministic scrape body (stable
// diffs/golden tests, and friendlier to `diff` between two scrapes).
func (s *Sampler) AllCounters() []CounterSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]CounterSnapshot, 0, len(s.live))
	for refStr, e := range s.live {
		ref, err := inventory.ParseRef(refStr)
		if err != nil {
			// Defensive only: refStr always came from Ref.String() in
			// ingestOne, so this should be unreachable in practice.
			s.log.Error("metrics: parsing live ref for counter export", "ref", refStr, "error", err)
			continue
		}
		out = append(out, CounterSnapshot{Ref: ref, Counters: e.counters, At: e.at.Unix()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.String() < out[j].Ref.String() })
	return out
}
