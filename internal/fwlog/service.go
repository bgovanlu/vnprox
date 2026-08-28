// SPDX-License-Identifier: Apache-2.0

package fwlog

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// Compile-time assertion that *peer.Client (production's real cluster
// transport) satisfies PeerSource with no adapter needed — the same
// "structural typing checked at compile time" precedent
// internal/collect's peer.Client usage already relies on.
var _ PeerSource = (*peer.Client)(nil)

// Broadcaster is the seam Service uses to push `firewall.log.batch` events
// over the shared /api/ws connection — the same small-interface pattern
// internal/metrics.Broadcaster and internal/topology's driftBroadcaster
// use (docs/api.md's WebSocket section: one connection multiplexes every
// topic). *topology.Hub/*topology.Service satisfy it.
type Broadcaster interface {
	Broadcast(topic string, payload []byte)
}

// SnapshotSource supplies the live firewall snapshot Correlate resolves
// guests against. cmd/vnproxd wires this to the same
// fw.BuildSnapshot(graph.Snapshot().All()) helper internal/api/firewall.go
// already uses for GET /firewall/rulesets.
type SnapshotSource interface {
	FirewallSnapshot() fw.Snapshot
}

// PeerSource is the cluster fan-out dependency: peer discovery plus one
// Tail-shaped fetch per peer over T-301's peer transport. *peer.Client
// satisfies this directly (Peers, FirewallLog — the latter added to
// internal/peer alongside this package).
type PeerSource interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
	FirewallLog(ctx context.Context, p peer.Peer, node, cursor string, maxLines int) (lines []string, nextCursor string, err error)
}

// Tunable defaults (docs/development.md doesn't pin numeric values for
// this feature; chosen to comfortably clear AC3's 10k-lines/min storm
// fixture — ~167 lines/sec — while keeping a 1s poll tick's WS payload and
// buffer growth bounded).
const (
	DefaultBufferCapacity         = 5000
	DefaultPollInterval           = time.Second
	DefaultMaxLinesPerNodePerTick = 2000
	DefaultMaxBroadcastPerTick    = 200
)

// Config configures a Service.
type Config struct {
	Local                  Source
	Peers                  PeerSource
	Snapshot               SnapshotSource
	WS                     Broadcaster
	LocalNode              func() string
	Logger                 *slog.Logger
	Now                    func() time.Time
	BufferCapacity         int
	PollInterval           time.Duration
	MaxLinesPerNodePerTick int
	MaxBroadcastPerTick    int
}

// TickResult reports one Tick call's work — exported so tests (including
// the storm test) can assert on fetch/parse/broadcast/drop counts directly
// without needing a real ticker or WS connection.
type TickResult struct {
	NodeErrors     map[string]string
	Fetched        int
	Parsed         int
	GarbageSkipped int
	Broadcast      int
	// Dropped is how many of this tick's newly parsed entries exceeded
	// MaxBroadcastPerTick and were rate-cap-dropped (AC3's "drop
	// indicator" signal) — distinct from RingBuffer's own eviction count,
	// which is ordinary history rotation, not a storm signal (see
	// RingBuffer.Push's doc comment).
	Dropped int
}

// Service is T-505's cluster-wide log tailer/correlator: a supervised
// polling loop (Run/Tick) that merges the local node's log with every
// peer's, parses and correlates each new line, keeps a bounded, rate-capped
// history (RingBuffer), and — when WS is configured — pushes new lines
// live. TailPage serves the REST read (both the initial "tail" view and a
// filtered re-query) directly from that same buffer, so there is exactly
// one merged history, not two independently-fetched views that could
// disagree.
type Service struct {
	buf          *RingBuffer
	logger       *slog.Logger
	cursors      map[string]string
	lastErr      map[string]string
	cfg          Config
	droppedTotal atomic.Int64
	mu           sync.Mutex
}

// New builds a Service from cfg, defaulting unset tunables.
func New(cfg Config) *Service {
	if cfg.BufferCapacity <= 0 {
		cfg.BufferCapacity = DefaultBufferCapacity
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.MaxLinesPerNodePerTick <= 0 {
		cfg.MaxLinesPerNodePerTick = DefaultMaxLinesPerNodePerTick
	}
	if cfg.MaxBroadcastPerTick <= 0 {
		cfg.MaxBroadcastPerTick = DefaultMaxBroadcastPerTick
	}
	if cfg.LocalNode == nil {
		cfg.LocalNode = func() string { return "" }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{
		cfg:     cfg,
		buf:     NewRingBuffer(cfg.BufferCapacity),
		logger:  cfg.Logger,
		cursors: map[string]string{},
		lastErr: map[string]string{},
	}
}

// Run polls every cfg.PollInterval until ctx is cancelled, feeding new
// lines into the shared buffer/WS push each tick (Tick). A nil
// cfg.Local makes this an immediate, silent no-op — the same degraded-mode
// treatment every other optional subsystem in cmd/vnproxd gets when its
// dependency failed to initialize.
func (s *Service) Run(ctx context.Context) error {
	if s.cfg.Local == nil {
		return nil
	}
	s.Tick(ctx) // prime immediately rather than waiting a full interval
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

// sources returns the full node list to poll (local first, then every
// peer sorted by name) and a node -> peer.Peer lookup for the peer
// entries, mirroring internal/api/clusterfanout.go's clusterSources (this
// package doesn't reuse that function directly to avoid an internal/api ->
// internal/fwlog dependency edge in the wrong direction; the algorithm is
// small enough that duplicating it is cheaper than restructuring who owns
// it).
func (s *Service) sources(ctx context.Context, localNode string) ([]string, map[string]peer.Peer) {
	nodes := []string{localNode}
	byPeer := map[string]peer.Peer{}
	if s.cfg.Peers == nil {
		return nodes, byPeer
	}
	list, err := s.cfg.Peers.Peers(ctx)
	if err != nil {
		s.logger.Warn("fwlog: discovering cluster peers", "error", err)
		return nodes, byPeer
	}
	names := make([]string, 0, len(list))
	for _, p := range list {
		if p.Node == localNode {
			continue
		}
		names = append(names, p.Node)
		byPeer[p.Node] = p
	}
	sort.Strings(names)
	return append(nodes, names...), byPeer
}

// Tick fetches every source's new lines since its last cursor, parses and
// correlates them, applies the rate cap, pushes the result into the shared
// buffer, and (if WS is configured) broadcasts it. Exported and free of any
// ticker so tests — notably the storm test — can call it directly, as fast
// as they like, without a real 1-minute wall clock.
func (s *Service) Tick(ctx context.Context) TickResult {
	res := TickResult{NodeErrors: map[string]string{}}

	localNode := s.cfg.LocalNode()
	nodes, byPeer := s.sources(ctx, localNode)

	s.mu.Lock()
	cursors := make(map[string]string, len(s.cursors))
	for k, v := range s.cursors {
		cursors[k] = v
	}
	s.mu.Unlock()

	var newEntries []Entry
	for _, node := range nodes {
		lines, next, err := s.fetch(ctx, node, localNode, cursors[node], byPeer)
		if err != nil {
			res.NodeErrors[node] = err.Error()
			continue // cursor untouched: retried in full next tick
		}
		cursors[node] = next
		res.Fetched += len(lines)
		for _, line := range lines {
			e, ok := ParseLine(node, line)
			if !ok {
				res.GarbageSkipped++
				continue
			}
			res.Parsed++
			newEntries = append(newEntries, e)
		}
	}

	s.mu.Lock()
	s.cursors = cursors
	for node, errStr := range res.NodeErrors {
		s.lastErr[node] = errStr
	}
	for _, node := range nodes {
		if _, failed := res.NodeErrors[node]; !failed {
			delete(s.lastErr, node)
		}
	}
	s.mu.Unlock()

	sortEntriesChronological(newEntries)

	broadcastCap := s.cfg.MaxBroadcastPerTick
	if len(newEntries) > broadcastCap {
		res.Dropped = len(newEntries) - broadcastCap
		s.droppedTotal.Add(int64(res.Dropped))
		newEntries = newEntries[len(newEntries)-broadcastCap:] // keep the newest `broadcastCap`
	}

	var snap fw.Snapshot
	if s.cfg.Snapshot != nil {
		snap = s.cfg.Snapshot.FirewallSnapshot()
	}
	resolvedCache := map[inventory.Ref]fw.ResolvedView{}

	broadcastBatch := make([]StreamEntry, 0, len(newEntries))
	for _, e := range newEntries {
		corr := s.correlate(e, snap, resolvedCache)
		broadcastBatch = append(broadcastBatch, s.buf.Push(e, corr))
	}
	res.Broadcast = len(broadcastBatch)

	if s.cfg.WS != nil && len(broadcastBatch) > 0 {
		s.broadcast(broadcastBatch)
	}
	return res
}

func (s *Service) fetch(ctx context.Context, node, localNode, cursor string, byPeer map[string]peer.Peer) ([]string, string, error) {
	if s.cfg.Local != nil && node == localNode {
		lines, next, _, err := s.cfg.Local.Tail(ctx, node, cursor, s.cfg.MaxLinesPerNodePerTick)
		return lines, next, err
	}
	p, ok := byPeer[node]
	if !ok || s.cfg.Peers == nil {
		return nil, cursor, ErrNotFound
	}
	return s.cfg.Peers.FirewallLog(ctx, p, node, cursor, s.cfg.MaxLinesPerNodePerTick)
}

// correlate resolves e's guest (if it names one — see Entry.Guest) against
// snap and calls Correlate, caching each guest's resolved view for the
// duration of one Tick (many lines in a storm typically share a guest).
func (s *Service) correlate(e Entry, snap fw.Snapshot, cache map[inventory.Ref]fw.ResolvedView) Correlation {
	if !e.Guest {
		return Correlate(e, fw.ResolvedView{})
	}
	guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: e.Node, ID: strconv.Itoa(e.VMID)}

	if _, known := snap.Guests[guestRef]; !known && snap.Cluster == nil {
		return Correlation{Status: StatusNoGuestData, Reason: "no firewall data observed yet for " + guestRef.String()}
	}

	resolved, ok := cache[guestRef]
	if !ok {
		rv, err := fw.Resolve(snap, guestRef)
		if err != nil {
			return Correlation{Status: StatusNoGuestData, Reason: "resolving " + guestRef.String() + ": " + err.Error()}
		}
		cache[guestRef] = rv
		resolved = rv
	}
	return Correlate(e, resolved)
}

// sortEntriesChronological orders entries oldest-first by parsed
// timestamp (stable, so same-timestamp/no-timestamp entries keep their
// original fetch order — local node's own lines before peers', in peer
// name order, per sources()). Purely cosmetic (a nicer merged multi-node
// reading order); correctness never depends on it.
func sortEntriesChronological(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
}

func (s *Service) broadcast(entries []StreamEntry) {
	views := make([]EntryView, len(entries))
	for i, e := range entries {
		views[i] = ToEntryView(e)
	}
	evt := batchEvent{Event: "firewall.log.batch", Entries: views, DroppedTotal: s.droppedTotal.Load()}
	data, err := marshalBatchEvent(evt)
	if err != nil {
		s.logger.Error("fwlog: marshaling firewall.log.batch event", "error", err)
		return
	}
	s.cfg.WS.Broadcast(TopicFirewallLog, data)
}

// Filter narrows TailPage's result. Every non-empty field is ANDed
// (case-insensitive); a zero Filter matches everything.
type Filter struct {
	Node      string
	Direction string
	Action    string
	VMID      int
}

// Match reports whether e satisfies every set field of f.
func (f Filter) Match(e Entry) bool {
	if f.Node != "" && !strings.EqualFold(f.Node, e.Node) {
		return false
	}
	if f.VMID != 0 && f.VMID != e.VMID {
		return false
	}
	if f.Direction != "" && !strings.EqualFold(f.Direction, e.Direction) {
		return false
	}
	if f.Action != "" && !strings.EqualFold(f.Action, e.Action) {
		return false
	}
	return true
}

// Page is TailPage's result.
type Page struct {
	Items            []StreamEntry // oldest first
	UnavailableNodes []string      // nodes whose most recent fetch attempt failed (log data from them may be stale/missing)
	DroppedTotal     int64
}

// TailPage returns up to limit buffered entries matching filter, newest
// available first internally but returned oldest-first (so appending
// subsequent `firewall.log.batch` WS pushes continues the same reading
// order). Backs GET /firewall/log.
func (s *Service) TailPage(filter Filter, limit int) Page {
	// limit <= 0 means "no limit" (bounded anyway by the ring buffer's own
	// capacity) rather than "zero items".
	unbounded := limit <= 0
	items, _ := s.buf.Snapshot()

	out := make([]StreamEntry, 0, len(items))
	for i := len(items) - 1; i >= 0 && (unbounded || len(out) < limit); i-- {
		if filter.Match(items[i].Entry) {
			out = append(out, items[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	s.mu.Lock()
	unavailable := make([]string, 0, len(s.lastErr))
	for n := range s.lastErr {
		unavailable = append(unavailable, n)
	}
	s.mu.Unlock()
	sort.Strings(unavailable)

	return Page{Items: out, DroppedTotal: s.droppedTotal.Load(), UnavailableNodes: unavailable}
}

// DroppedTotal returns the cumulative rate-cap drop count (AC3's storm
// indicator), independent of any particular TailPage call.
func (s *Service) DroppedTotal() int64 {
	return s.droppedTotal.Load()
}
