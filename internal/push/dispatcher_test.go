// SPDX-License-Identifier: Apache-2.0

package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeProvider hands back a fixed set of subscriptions per category and
// records every category it was asked about, so tests can assert
// "nothing was even looked up" for events that should be ignored entirely.
type fakeProvider struct {
	askedErr error
	byCat    map[Category][]SubscriptionRecord
	asked    []Category
	mu       sync.Mutex
}

func (p *fakeProvider) Subscriptions(_ context.Context, cat Category) ([]SubscriptionRecord, error) {
	p.mu.Lock()
	p.asked = append(p.asked, cat)
	p.mu.Unlock()
	if p.askedErr != nil {
		return nil, p.askedErr
	}
	return p.byCat[cat], nil
}

func (p *fakeProvider) askedFor() []Category {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Category{}, p.asked...)
}

// fakeTracker records TouchLastUsed/Prune calls on a channel so a test can
// synchronize on the dispatcher's background goroutines finishing without
// a sleep loop.
type fakeTracker struct {
	touched chan string
	pruned  chan string
}

func newFakeTracker() *fakeTracker {
	return &fakeTracker{touched: make(chan string, 8), pruned: make(chan string, 8)}
}

func (f *fakeTracker) TouchLastUsed(_ context.Context, id string, _ int64) error {
	f.touched <- id
	return nil
}

func (f *fakeTracker) Prune(_ context.Context, id string) error {
	f.pruned <- id
	return nil
}

func waitFor(t *testing.T, ch chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for a value on the channel (want %q)", want)
	}
}

func assertNothingArrives(t *testing.T, ch chan string) {
	t.Helper()
	select {
	case got := <-ch:
		t.Errorf("unexpected value arrived: %q", got)
	case <-time.After(150 * time.Millisecond):
		// Nothing arrived within the window — expected.
	}
}

func newTestSubscriptionRecord(t *testing.T, id string, endpoint string) SubscriptionRecord {
	t.Helper()
	sub, _, _ := testSubscriber(t)
	sub.Endpoint = endpoint
	return SubscriptionRecord{ID: id, Subscription: sub}
}

func TestDispatcher_PublishRoutesToTheCorrectCategoryOnly(t *testing.T) {
	var received []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	awaitingSub := newTestSubscriptionRecord(t, "sub-awaiting", srv.URL+"/awaiting")
	provider := &fakeProvider{byCat: map[Category][]SubscriptionRecord{
		CategoryAwaitingConfirm: {awaitingSub},
	}}
	tracker := newFakeTracker()
	d := NewDispatcher(DispatcherConfig{
		Provider: provider, Tracker: tracker, VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com", Client: srv.Client(),
	})

	d.Publish([]byte(`{"event":"changeset.status","id":"cs1","status":"awaiting_confirm"}`))
	waitFor(t, tracker.touched, "sub-awaiting")

	askedFor := provider.askedFor()
	if len(askedFor) != 1 || askedFor[0] != CategoryAwaitingConfirm {
		t.Errorf("provider was asked for %v, want exactly [awaitingConfirm]", askedFor)
	}
}

// TestDispatcher_Publish_UnhandledEventNeverConsultsProvider is the
// negative leg: an event Publish should ignore (per BuildFromEvent) must
// not even call Provider.Subscriptions — proves the filtering happens
// before any fan-out attempt, not that fan-out happens to zero recipients.
func TestDispatcher_Publish_UnhandledEventNeverConsultsProvider(t *testing.T) {
	provider := &fakeProvider{byCat: map[Category][]SubscriptionRecord{}}
	d := NewDispatcher(DispatcherConfig{Provider: provider, VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com"})

	d.Publish([]byte(`{"event":"findings.changed","count":5}`))

	// Publish is async (a bare `go`) even when it does nothing further, so
	// give any (incorrect) goroutine a moment to have called Subscriptions
	// before asserting it didn't.
	time.Sleep(150 * time.Millisecond)
	if got := provider.askedFor(); len(got) != 0 {
		t.Errorf("provider.Subscriptions was called for an unhandled event: %v", got)
	}
}

func TestDispatcher_PublishCriticalFinding_DeliversToCriticalSubscribers(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = b
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sub, uaPriv, authSecret := testSubscriber(t)
	sub.Endpoint = srv.URL + "/crit"
	provider := &fakeProvider{byCat: map[Category][]SubscriptionRecord{
		CategoryCritical: {{ID: "sub-crit", Subscription: sub}},
	}}
	tracker := newFakeTracker()
	d := NewDispatcher(DispatcherConfig{
		Provider: provider, Tracker: tracker, VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com", Client: srv.Client(),
	})

	d.PublishCriticalFinding()
	waitFor(t, tracker.touched, "sub-crit")

	plain := decryptForTest(t, gotBody, uaPriv, authSecret)
	var n Notification
	if err := json.Unmarshal(plain, &n); err != nil {
		t.Fatalf("decoding delivered notification: %v", err)
	}
	if n.Category != CategoryCritical {
		t.Errorf("delivered Category = %q, want critical", n.Category)
	}
}

func TestDispatcher_DeadSubscriptionIsPrunedNotTouched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	sub, _, _ := testSubscriber(t)
	sub.Endpoint = srv.URL + "/dead"
	provider := &fakeProvider{byCat: map[Category][]SubscriptionRecord{
		CategoryDrift: {{ID: "sub-dead", Subscription: sub}},
	}}
	tracker := newFakeTracker()
	d := NewDispatcher(DispatcherConfig{
		Provider: provider, Tracker: tracker, VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com", Client: srv.Client(),
	})

	d.Publish([]byte(`{"event":"drift.changed","count":2}`))
	waitFor(t, tracker.pruned, "sub-dead")
	assertNothingArrives(t, tracker.touched)
}

func TestDispatcher_NilProviderIsANoOp(t *testing.T) {
	d := NewDispatcher(DispatcherConfig{VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com"})
	// Must not panic — this is the "no [push]-equivalent dependency wired"
	// degraded-mode convention every other optional Provider/eventSinkSetter
	// in this codebase follows (automation.Dispatcher.Publish's identical
	// nil-Provider check).
	d.Publish([]byte(`{"event":"drift.changed","count":1}`))
	d.PublishCriticalFinding()
}

func TestDispatcher_ProviderErrorDoesNotPanicOrDeliver(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	provider := &fakeProvider{askedErr: errors.New("db down")}
	d := NewDispatcher(DispatcherConfig{Provider: provider, VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com", Client: srv.Client()})

	d.Publish([]byte(`{"event":"drift.changed","count":1}`))
	time.Sleep(150 * time.Millisecond)
	if called {
		t.Error("delivery was attempted despite Provider.Subscriptions erroring")
	}
}
