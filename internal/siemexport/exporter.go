// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultBufferSize is SIEMExportConfig.BufferSize's fallback when unset —
// generous enough to absorb a burst (a findings cycle touching a few
// hundred refs, a changeset apply's per-op audit rows) without ever
// approaching unbounded growth.
const DefaultBufferSize = 4096

// DropReason names why Exporter dropped an event — the label
// DropObserver.SIEMExportDropped and every log line below use, and what
// exporter_test.go's table-driven cases assert against.
type DropReason string

const (
	// DropBufferFull: the bounded buffer was at capacity when a new event
	// arrived, so the oldest not-yet-attempted event was evicted to make
	// room for it.
	DropBufferFull DropReason = "buffer_full"
	// DropSendError: Sink.Send returned an error (far end down, connection
	// reset, write timeout, ...). The event is not retried — see doc.go's
	// "Delivery semantics" section for why.
	DropSendError DropReason = "send_error"
)

// DropObserver is notified, synchronously and off Exporter's internal
// lock, every time an event is dropped. Implementations must not block —
// the same "must not block" contract every other optional hook in this
// codebase carries (store.AuditRepo.SetOnAppend, topology.Hub.SetEventSink,
// ...). A nil DropObserver (Exporter's default) means "nothing is told",
// matching every other optional-callback convention here; Exporter.Stats
// still counts drops either way, so a caller that only polls Stats
// periodically loses nothing by leaving this nil.
type DropObserver interface {
	SIEMExportDropped(reason DropReason)
}

// Sink is one export transport: RFC 5424 syslog (sink_syslog.go) or
// newline-delimited JSON (sink_jsonl.go). Send is called at most once per
// event (see doc.go) with a per-attempt-bounded ctx; Close releases
// whatever connection/file handle Send opened.
type Sink interface {
	Send(ctx context.Context, ev Event) error
	Close() error
}

// Stats is a point-in-time snapshot of Exporter's counters, returned by
// Exporter.Stats for tests and any future health/status surface.
type Stats struct {
	Sent     uint64
	Dropped  uint64
	Buffered int
}

// sendTimeout bounds a single Sink.Send attempt — long enough for a real
// network write, short enough that a wedged connection cannot stall the
// sender loop indefinitely.
const sendTimeout = 5 * time.Second

// Backoff bounds between failed send attempts, so a sustained outage
// paces its dial/write attempts rather than spin-looping against a dead
// endpoint.
const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// Exporter is the bounded, best-effort sender T-4012 adds: a ring buffer
// of pending Events plus a single background sender goroutine (Run) that
// drains it into a Sink. See doc.go's "Delivery semantics" section for the
// at-most-once contract this type implements.
//
// The zero value is not usable; construct with NewExporter. Safe for
// concurrent use: ExportAudit/ExportFinding may be called from any
// goroutine (the audit-append hook and the findings-cycle observer each
// run on their own), and Run owns the single consumer side.
//
// at startup by cmd/vnproxd's setupSIEMExport), never a hot allocation
// path — field order groups "config", "sync state", "backoff tuning" for
// readability.
//
//nolint:govet // fieldalignment: one Exporter per daemon (constructed once
type Exporter struct {
	sink         Sink
	capacity     int
	dropObserver DropObserver
	logger       *slog.Logger

	// onSent, when set (SetOnSent), is called once per successfully sent
	// event — the same nil-safe optional-callback convention
	// store.AuditRepo.SetOnAppend/topology.Hub.SetEventSink already use in
	// this codebase, kept as a plain func rather than growing
	// DropObserver into a two-method interface since exactly one caller
	// (cmd/vnproxd's self-observability wiring) ever needs it.
	onSent func()

	mu      sync.Mutex
	buf     []Event
	sent    uint64
	dropped uint64

	wake chan struct{}

	// backoffInitial/backoffMax mirror the initialBackoff/maxBackoff
	// package consts above; broken out as per-instance fields (defaulted
	// to those consts in NewExporter) purely so exporter_test.go's
	// far-end-down/reconnect cases can shrink them and run in
	// milliseconds instead of tens of seconds. No production caller sets
	// these to anything but the defaults.
	backoffInitial time.Duration
	backoffMax     time.Duration
}

// NewExporter constructs an Exporter over sink. capacity <= 0 falls back
// to DefaultBufferSize (mirrors every other "non-positive means use the
// documented default" convention in this codebase, e.g.
// config.firstNonZeroInt's callers). dropObserver and logger are both
// nil-safe (logger defaults to slog.Default(), matching every other
// constructor in this codebase).
func NewExporter(sink Sink, capacity int, dropObserver DropObserver, logger *slog.Logger) *Exporter {
	if capacity <= 0 {
		capacity = DefaultBufferSize
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Exporter{
		sink:           sink,
		capacity:       capacity,
		dropObserver:   dropObserver,
		logger:         logger,
		wake:           make(chan struct{}, 1),
		backoffInitial: initialBackoff,
		backoffMax:     maxBackoff,
	}
}

// SetOnSent registers fn to be called (off Exporter's internal lock) once
// per successfully sent event. A nil fn (the default) means nothing is
// told. Not required for correctness — Stats().Sent already carries the
// count for any poller — it exists purely so a push-model metric (like
// internal/metrics.Registry's vnprox_siemexport_events_total{outcome="sent"})
// can be driven the same way DropObserver drives the dropped outcomes.
func (x *Exporter) SetOnSent(fn func()) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.onSent = fn
}

// ExportAudit enqueues one audit row for export. Never blocks (Enqueue's
// own doc comment): it appends to an in-memory slice under a mutex and
// returns, so calling it from store.AuditRepo's synchronous SetOnAppend
// hook cannot slow down or fail an audit write.
func (x *Exporter) ExportAudit(in AuditInput) { x.enqueue(NewAuditEvent(in)) }

// ExportFinding enqueues one finding transition for export. Same
// never-blocks contract as ExportAudit.
func (x *Exporter) ExportFinding(in FindingInput) { x.enqueue(NewFindingEvent(in)) }

// enqueue appends ev to the bounded buffer, evicting the oldest buffered
// event first if it is already at capacity (DropBufferFull) — see doc.go.
// The whole mutation happens under one lock/unlock pair so a drop and its
// replacement append are never observably interleaved with a concurrent
// enqueue or with Run's own dequeue.
func (x *Exporter) enqueue(ev Event) {
	x.mu.Lock()
	dropped := false
	if len(x.buf) >= x.capacity {
		// Drop the oldest (index 0) to make room for the newest.
		copy(x.buf, x.buf[1:])
		x.buf = x.buf[:len(x.buf)-1]
		x.dropped++
		dropped = true
	}
	x.buf = append(x.buf, ev)
	x.mu.Unlock()

	if dropped {
		x.notifyDrop(DropBufferFull)
	}
	select {
	case x.wake <- struct{}{}:
	default:
	}
}

// dequeue pops and returns the oldest buffered event, or ok=false if the
// buffer is currently empty.
func (x *Exporter) dequeue() (Event, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.buf) == 0 {
		return Event{}, false
	}
	ev := x.buf[0]
	x.buf = x.buf[1:]
	return ev, true
}

func (x *Exporter) notifyDrop(reason DropReason) {
	if x.dropObserver != nil {
		x.dropObserver.SIEMExportDropped(reason)
	}
}

// Stats returns a point-in-time snapshot of Exporter's counters.
func (x *Exporter) Stats() Stats {
	x.mu.Lock()
	defer x.mu.Unlock()
	return Stats{Sent: x.sent, Dropped: x.dropped, Buffered: len(x.buf)}
}

// Run drains the buffer into Sink until ctx is cancelled, then closes the
// sink and returns nil — the same "blocks for the daemon's lifetime,
// returns nil on cancellation" contract every other cmd/vnproxd runGroup
// actor follows (cmd/vnproxd/rungroup.go's actor doc comment). Each
// dequeued event is handed to Sink.Send exactly once (doc.go's "Delivery
// semantics"): a failed send is counted as DropSendError and NOT
// requeued, and Run backs off (initialBackoff, doubling to maxBackoff)
// before its next attempt so a sustained outage paces its retries rather
// than spin-looping.
func (x *Exporter) Run(ctx context.Context) error {
	defer func() {
		if x.sink != nil {
			_ = x.sink.Close()
		}
	}()

	backoff := x.backoffInitial
	for {
		ev, ok := x.dequeue()
		if !ok {
			select {
			case <-ctx.Done():
				return nil
			case <-x.wake:
				continue
			}
		}

		sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		err := x.sink.Send(sendCtx, ev)
		cancel()

		if err != nil {
			x.mu.Lock()
			x.dropped++
			x.mu.Unlock()
			x.notifyDrop(DropSendError)
			x.logger.Warn("siemexport: dropping event after send failure", "kind", ev.Kind, "error", err)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > x.backoffMax {
				backoff = x.backoffMax
			}
			continue
		}

		backoff = x.backoffInitial
		x.mu.Lock()
		x.sent++
		onSent := x.onSent
		x.mu.Unlock()
		if onSent != nil {
			onSent()
		}
	}
}
