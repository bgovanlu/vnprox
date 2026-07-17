package flow

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// TopicFlows is the WS subscribe topic name for this package's flow.batch
// push (docs/api.md's WebSocket section) — a client subscribes to "flows"
// to receive them, the same way it subscribes to "topology" or
// "firewall.log".
const TopicFlows = "flows"

// Broadcaster is the seam Service uses to push flow.batch events over the
// shared /api/ws connection — the same small-interface pattern
// internal/fwlog.Broadcaster/internal/metrics.Broadcaster use.
// *topology.Hub/*topology.Service satisfy it.
type Broadcaster interface {
	Broadcast(topic string, payload []byte)
}

// Default tunables (docs/development.md doesn't pin numeric values for this
// feature). DefaultRetentionMinutes/DefaultMaxRows are this task's card's
// own documented defaults (60 minutes, 2,000,000 rows — whichever prunes
// first, see this package's doc comment); DefaultPruneInterval matches
// internal/metrics' hourly-ish prune cadence pattern scaled down for a
// window an order of magnitude shorter. DefaultMaxBroadcastPerBatch mirrors
// internal/fwlog's own per-tick WS rate cap (DefaultMaxBroadcastPerTick)
// applied per-Ingest-call here instead of per-poll-tick, since flow
// ingestion is push-driven (one UDP datagram at a time) rather than
// poll-driven.
const (
	DefaultRetentionMinutes      = 60
	DefaultMaxRows               = 2_000_000
	DefaultPruneInterval         = time.Minute
	DefaultMaxBroadcastPerBatch  = 200
	DefaultTemplatePruneInterval = 5 * time.Minute
)

// Config configures a Service. Store, Resolver, and WS are all optional:
// a nil Store disables persistence and GET /flows entirely (Query returns
// nothing); a nil Resolver leaves every Record's srcRef/dstRef empty
// (ResolveRecord's nil-safe no-op); a nil WS disables the flow.batch push.
type Config struct {
	Store    FlowStore
	Resolver Resolver
	WS       Broadcaster
	Logger   *slog.Logger
	// Now overrides time.Now for tests (prune-tick time-travel), mirroring
	// every other clock-injecting Config in this codebase.
	Now                  func() time.Time
	RetentionMinutes     int
	MaxRows              int64
	MaxBroadcastPerBatch int
}

// Service is T-1002's ingestion sink: every Listener (listener.go) calls
// Ingest with the batch of Records it just decoded from one UDP datagram;
// Service resolves srcRef/dstRef, persists the batch to the bounded ring
// (Store), and — when WS is configured — pushes a rate-capped flow.batch
// event. Query serves GET /flows' local-node read directly off the same
// Store. RunPruneLoop enforces this package's documented bound (retention
// window AND hard row cap, whichever is smaller prunes first) on a tick.
type Service struct {
	logger       *slog.Logger
	now          func() time.Time
	cfg          Config
	droppedTotal atomic.Int64
}

// New builds a Service from cfg, defaulting unset tunables.
func New(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RetentionMinutes <= 0 {
		cfg.RetentionMinutes = DefaultRetentionMinutes
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = DefaultMaxRows
	}
	if cfg.MaxBroadcastPerBatch <= 0 {
		cfg.MaxBroadcastPerBatch = DefaultMaxBroadcastPerBatch
	}
	return &Service{cfg: cfg, logger: cfg.Logger, now: cfg.Now}
}

// Ingest resolves, persists, and broadcasts records — the seam every
// Listener's Run loop (and, later, T-1004's host samplers) feeds into. A
// nil/empty records is a no-op.
func (s *Service) Ingest(ctx context.Context, records []Record) {
	if len(records) == 0 {
		return
	}
	for i := range records {
		ResolveRecord(s.cfg.Resolver, &records[i])
	}
	if s.cfg.Store != nil {
		s.persist(ctx, records)
	}
	s.broadcast(records)
}

func (s *Service) persist(ctx context.Context, records []Record) {
	samples := make([]store.FlowSample, len(records))
	for i, r := range records {
		samples[i] = toStoreSample(r)
	}
	if err := s.cfg.Store.InsertBatch(ctx, samples); err != nil {
		s.logger.Error("flow: persisting ingested flow records", "count", len(records), "error", err)
	}
}

// batchEvent is docs/api.md's documented `flow.batch {entries, droppedTotal}`
// WS event, plus the flat "event" name field every server->client message
// on the shared /api/ws connection carries.
type batchEvent struct {
	Event        string   `json:"event"`
	Entries      []Record `json:"entries"`
	DroppedTotal int64    `json:"droppedTotal"`
}

// broadcast pushes records over WS, rate-capped at MaxBroadcastPerBatch per
// call (keeping the newest entries, mirroring internal/fwlog.Service.Tick's
// identical "keep the newest N, count the rest as dropped" convention) —
// same storm-indicator contract as firewall.log.batch's droppedTotal.
func (s *Service) broadcast(records []Record) {
	if s.cfg.WS == nil {
		return
	}
	batch := records
	if len(batch) > s.cfg.MaxBroadcastPerBatch {
		dropped := int64(len(batch) - s.cfg.MaxBroadcastPerBatch)
		s.droppedTotal.Add(dropped)
		batch = batch[len(batch)-s.cfg.MaxBroadcastPerBatch:] // keep the newest
	}
	evt := batchEvent{Event: "flow.batch", Entries: batch, DroppedTotal: s.droppedTotal.Load()}
	data, err := json.Marshal(evt)
	if err != nil {
		s.logger.Error("flow: marshaling flow.batch event", "error", err)
		return
	}
	s.cfg.WS.Broadcast(TopicFlows, data)
}

// DroppedTotal returns the cumulative WS rate-cap drop count (the storm
// indicator), independent of any particular broadcast call.
func (s *Service) DroppedTotal() int64 {
	return s.droppedTotal.Load()
}

// Query serves GET /flows' local-node read directly off Store, translating
// store.FlowSample rows back to Records. A nil Store (no daemon-side
// persistence wired) returns an empty result, not an error — the same
// degraded-mode treatment internal/metrics.Sampler.History gives a nil
// Store.
func (s *Service) Query(ctx context.Context, filter Filter, cursor string, limit int) ([]Record, string, error) {
	if s.cfg.Store == nil {
		return nil, "", nil
	}
	samples, next, err := s.cfg.Store.Query(ctx, filter, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]Record, len(samples))
	for i, sm := range samples {
		out[i] = fromStoreSample(sm)
	}
	return out, next, nil
}

// RunPruneLoop enforces this package's documented ring bound (retention
// window AND hard row cap) every interval until ctx is cancelled — the
// same tick-based prune-loop shape store.MetricSampleRepo.RunPruneLoop
// already establishes. A nil Store makes this an immediate no-op.
func (s *Service) RunPruneLoop(ctx context.Context, interval time.Duration) error {
	if s.cfg.Store == nil {
		return nil
	}
	if interval <= 0 {
		interval = DefaultPruneInterval
	}
	s.prune(ctx) // prime immediately rather than waiting a full interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.prune(ctx)
		}
	}
}

func (s *Service) prune(ctx context.Context) {
	cutoff := s.now().Add(-time.Duration(s.cfg.RetentionMinutes) * time.Minute).Unix()
	if _, err := s.cfg.Store.PruneOlderThan(ctx, cutoff); err != nil {
		s.logger.Error("flow: pruning flow_samples by retention window", "error", err)
	}
	if _, err := s.cfg.Store.PruneToCap(ctx, s.cfg.MaxRows); err != nil {
		s.logger.Error("flow: pruning flow_samples to the hard row cap", "error", err)
	}
}
