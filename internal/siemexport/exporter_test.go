// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSink is a Sink whose Send behavior is fully controlled by the test:
// sendFn decides success/failure per call, and every Event actually handed
// to Send is recorded for assertions.
type fakeSink struct {
	sendFn  func(call int, ev Event) error
	sent    []Event
	mu      sync.Mutex
	callNum int
	closed  bool
}

func (f *fakeSink) Send(_ context.Context, ev Event) error {
	f.mu.Lock()
	call := f.callNum
	f.callNum++
	f.mu.Unlock()

	var err error
	if f.sendFn != nil {
		err = f.sendFn(call, ev)
	}
	if err == nil {
		f.mu.Lock()
		f.sent = append(f.sent, ev)
		f.mu.Unlock()
	}
	return err
}

func (f *fakeSink) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSink) sentEvents() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.sent...)
}

// countingDropObserver records every DropReason it was told about, so
// tests can assert the drop-notification mechanism actually fires (not
// just that Stats().Dropped incremented).
type countingDropObserver struct {
	reasons []DropReason
	mu      sync.Mutex
}

func (c *countingDropObserver) SIEMExportDropped(reason DropReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reasons = append(c.reasons, reason)
}

func (c *countingDropObserver) count(reason DropReason) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, r := range c.reasons {
		if r == reason {
			n++
		}
	}
	return n
}

func testExporter(sink Sink, capacity int, obs DropObserver) *Exporter {
	x := NewExporter(sink, capacity, obs, nil)
	// Shrink the retry pacing so far-end-down/reconnect cases run in
	// milliseconds — see Exporter.backoffInitial's doc comment.
	x.backoffInitial = time.Millisecond
	x.backoffMax = 5 * time.Millisecond
	return x
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

// TestExporter_BufferFull_DropsOldestAndNotifies is the "buffer full" case:
// with Run not yet consuming, enqueuing past capacity must evict the
// OLDEST not-yet-attempted event (doc.go's drop policy), count it, and
// call the DropObserver.
func TestExporter_BufferFull_DropsOldestAndNotifies(t *testing.T) {
	obs := &countingDropObserver{}
	x := testExporter(&fakeSink{}, 2, obs)

	x.ExportFinding(FindingInput{ID: "f1", Transition: TransitionNew})
	x.ExportFinding(FindingInput{ID: "f2", Transition: TransitionNew})
	x.ExportFinding(FindingInput{ID: "f3", Transition: TransitionNew}) // evicts f1

	stats := x.Stats()
	if stats.Buffered != 2 {
		t.Fatalf("buffered = %d, want 2", stats.Buffered)
	}
	if stats.Dropped != 1 {
		t.Fatalf("dropped = %d, want 1", stats.Dropped)
	}
	if got := obs.count(DropBufferFull); got != 1 {
		t.Fatalf("buffer_full notifications = %d, want 1", got)
	}

	// The survivors must be f2 and f3 (oldest, f1, was evicted), in order.
	x.mu.Lock()
	ids := make([]string, len(x.buf))
	for i, ev := range x.buf {
		ids[i] = ev.FindingID
	}
	x.mu.Unlock()
	if len(ids) != 2 || ids[0] != "f2" || ids[1] != "f3" {
		t.Fatalf("surviving buffer = %v, want [f2 f3]", ids)
	}
}

// TestExporter_FarEndDown_DropsAndCountsWithoutBlockingProducer is the
// "far end down" case: every Send fails, so every dequeued event is
// counted as DropSendError (never retried — doc.go's at-most-once
// contract) and the primary producer side (ExportAudit/ExportFinding)
// never blocks regardless.
func TestExporter_FarEndDown_DropsAndCountsWithoutBlockingProducer(t *testing.T) {
	sink := &fakeSink{sendFn: func(_ int, _ Event) error {
		return errors.New("connection refused")
	}}
	obs := &countingDropObserver{}
	x := testExporter(sink, 100, obs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = x.Run(ctx)
		close(done)
	}()

	const n = 5
	for i := 0; i < n; i++ {
		x.ExportAudit(AuditInput{ID: int64(i), Action: "login", Result: "success"})
	}

	waitFor(t, 2*time.Second, func() bool { return x.Stats().Dropped >= n })

	stats := x.Stats()
	if stats.Sent != 0 {
		t.Fatalf("sent = %d, want 0 (far end is down)", stats.Sent)
	}
	if got := obs.count(DropSendError); got < n {
		t.Fatalf("send_error notifications = %d, want >= %d", got, n)
	}
	if len(sink.sentEvents()) != 0 {
		t.Fatalf("sink recorded %d successful sends, want 0", len(sink.sentEvents()))
	}

	cancel()
	<-done
}

// TestExporter_Reconnect_ResumesDeliveryAfterFarEndRecovers is the
// "reconnect" case: Send fails for the first few attempts (far end down)
// then starts succeeding (far end recovered) — Run must resume delivering
// without needing to be restarted, and without ever retrying/duplicating
// the events that were already dropped while it was down.
func TestExporter_Reconnect_ResumesDeliveryAfterFarEndRecovers(t *testing.T) {
	const failCount = 3
	sink := &fakeSink{sendFn: func(call int, _ Event) error {
		if call < failCount {
			return errors.New("connection refused")
		}
		return nil
	}}
	obs := &countingDropObserver{}
	x := testExporter(sink, 100, obs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = x.Run(ctx)
		close(done)
	}()

	for i := 0; i < failCount; i++ {
		x.ExportFinding(FindingInput{ID: "down-" + string(rune('a'+i)), Transition: TransitionNew})
	}
	waitFor(t, 2*time.Second, func() bool { return x.Stats().Dropped >= failCount })

	x.ExportFinding(FindingInput{ID: "recovered", Transition: TransitionNew})
	waitFor(t, 2*time.Second, func() bool { return x.Stats().Sent >= 1 })

	sent := sink.sentEvents()
	if len(sent) != 1 || sent[0].FindingID != "recovered" {
		t.Fatalf("sent events = %+v, want exactly the post-recovery event", sent)
	}
	if x.Stats().Dropped != failCount {
		t.Fatalf("dropped = %d, want exactly %d (no retry/duplicate of the down-period events)", x.Stats().Dropped, failCount)
	}

	cancel()
	<-done
}

// TestExporter_RunClosesSinkOnShutdown proves Run's owned-goroutine
// contract: it closes the sink and returns nil once ctx is cancelled, the
// same "must return promptly" rule every other cmd/vnproxd runGroup actor
// follows.
func TestExporter_RunClosesSinkOnShutdown(t *testing.T) {
	sink := &fakeSink{}
	x := testExporter(sink, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- x.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of ctx cancellation")
	}
	if !sink.closed {
		t.Fatal("Run did not close the sink on shutdown")
	}
}

// TestNewAuditEvent_RedactsDetailAndFreeText is the "redaction applied"
// case for audit rows: a secret-shaped value in detail_json and in a
// free-text field must never survive into the exported Event.
func TestNewAuditEvent_RedactsDetailAndFreeText(t *testing.T) {
	in := AuditInput{
		ID:         1,
		Username:   "admin",
		Action:     `login token=eyJhbGciOiJIUzI1NiJ9.secretvalue.sig`,
		Result:     "success",
		DetailJSON: `{"username":"admin","password":"hunter2","note":"ok"}`,
	}
	ev := NewAuditEvent(in)

	if ev.Action == in.Action {
		t.Fatalf("Action was not redacted: %q", ev.Action)
	}
	var detail map[string]any
	if err := json.Unmarshal(ev.Detail, &detail); err != nil {
		t.Fatalf("Detail is not valid JSON: %v (%s)", err, ev.Detail)
	}
	if detail["password"] == "hunter2" {
		t.Fatalf("password leaked into exported Detail: %s", ev.Detail)
	}
	if detail["note"] != "ok" {
		t.Fatalf("non-secret field was unexpectedly altered: %s", ev.Detail)
	}
}

// TestNewFindingEvent_RedactsDetailAndRefs is the "redaction applied" case
// for findings: Detail and Refs both go through redact.Scrub.
func TestNewFindingEvent_RedactsDetailAndRefs(t *testing.T) {
	in := FindingInput{
		ID:         "health:x",
		Severity:   "warning",
		Detail:     `webhook secret=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`,
		Refs:       []string{`token=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`},
		Transition: TransitionNew,
	}
	ev := NewFindingEvent(in)

	if ev.FindingDetail == in.Detail {
		t.Fatalf("FindingDetail was not redacted: %q", ev.FindingDetail)
	}
	if ev.Refs[0] == in.Refs[0] {
		t.Fatalf("Refs entry was not redacted: %q", ev.Refs[0])
	}
}

// TestExporter_SetOnSent_FiresOncePerSuccessfulSend proves the optional
// SetOnSent hook (internal/metrics.Registry's push-model "sent" outcome
// wiring) fires exactly once per successful delivery and not at all for
// dropped ones.
func TestExporter_SetOnSent_FiresOncePerSuccessfulSend(t *testing.T) {
	sink := &fakeSink{sendFn: func(call int, _ Event) error {
		if call == 0 {
			return errors.New("first attempt fails")
		}
		return nil
	}}
	x := testExporter(sink, 10, nil)

	var mu sync.Mutex
	sentCalls := 0
	x.SetOnSent(func() {
		mu.Lock()
		sentCalls++
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = x.Run(ctx); close(done) }()

	x.ExportAudit(AuditInput{ID: 1, Result: "success"}) // dropped (call 0 fails)
	x.ExportAudit(AuditInput{ID: 2, Result: "success"}) // sent (call 1 succeeds)

	waitFor(t, 2*time.Second, func() bool { return x.Stats().Sent >= 1 })

	mu.Lock()
	got := sentCalls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("onSent fired %d times, want exactly 1", got)
	}

	cancel()
	<-done
}

// TestExporter_ProducerNeverBlocks is a light sanity check that
// ExportAudit/ExportFinding return promptly even while Run is stalled
// retrying a down sink — the "must not block the primary write path"
// contract doc.go's own package comment states.
func TestExporter_ProducerNeverBlocks(t *testing.T) {
	sink := &fakeSink{sendFn: func(_ int, _ Event) error { return errors.New("down") }}
	x := testExporter(sink, 4, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = x.Run(ctx); close(done) }()

	start := time.Now()
	for i := 0; i < 20; i++ {
		x.ExportAudit(AuditInput{ID: int64(i), Result: "success"})
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("20 ExportAudit calls took %s, want near-instant (producer must never block on a down sink)", elapsed)
	}

	cancel()
	<-done
}
