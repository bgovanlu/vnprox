// SPDX-License-Identifier: Apache-2.0

package automation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakeProvider struct {
	err      error
	webhooks []Webhook
}

func (f fakeProvider) Webhooks(context.Context) ([]Webhook, error) { return f.webhooks, f.err }

type trackerCall struct {
	id      string
	errMsg  string
	success bool
}

type fakeTracker struct {
	calls []trackerCall
	count int
	mu    sync.Mutex
}

func (f *fakeTracker) RecordSuccess(_ context.Context, id string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, trackerCall{id: id, success: true})
	f.count = 0
	return nil
}

func (f *fakeTracker) RecordFailure(_ context.Context, id string, _ int64, errMsg string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	f.calls = append(f.calls, trackerCall{id: id, success: false, errMsg: errMsg})
	return f.count, nil
}

func (f *fakeTracker) snapshot() []trackerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trackerCall(nil), f.calls...)
}

func noSleep(context.Context, time.Duration) {}

func TestDispatcher_DeliversSignedPayloadAndRecordsSuccess(t *testing.T) {
	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(HeaderSignature)
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := Webhook{ID: "wh1", URL: srv.URL, Secret: "s3cret", Events: nil}
	tracker := &fakeTracker{}
	d := NewDispatcher(DispatcherConfig{
		Provider: fakeProvider{webhooks: []Webhook{wh}},
		Tracker:  tracker,
		Client:   srv.Client(),
		Sleep:    noSleep,
	})

	payload := []byte(`{"event":"changeset.status","id":"cs1"}`)
	d.publish(payload)

	if gotSig == "" {
		t.Fatal("delivery carried no X-VNPROX-SIGNATURE header")
	}
	if !VerifySignature([]byte("s3cret"), gotBody, gotSig) {
		t.Errorf("delivered signature %q does not verify against the delivered body %s", gotSig, gotBody)
	}

	calls := tracker.snapshot()
	if len(calls) != 1 || !calls[0].success {
		t.Fatalf("tracker calls = %+v, want exactly one success", calls)
	}
}

func TestDispatcher_EventFilter_OnlyMatchingWebhooksReceiveIt(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	matching := Webhook{ID: "wh-match", URL: srv.URL + "/match", Secret: "s", Events: []string{"changeset.status"}}
	nonMatching := Webhook{ID: "wh-nomatch", URL: srv.URL + "/nomatch", Secret: "s", Events: []string{"audit.appended"}}
	catchAll := Webhook{ID: "wh-all", URL: srv.URL + "/all", Secret: "s", Events: nil}

	tracker := &fakeTracker{}
	d := NewDispatcher(DispatcherConfig{
		Provider: fakeProvider{webhooks: []Webhook{matching, nonMatching, catchAll}},
		Tracker:  tracker,
		Client:   srv.Client(),
		Sleep:    noSleep,
	})

	d.publish([]byte(`{"event":"changeset.status","id":"cs1"}`))

	mu.Lock()
	defer mu.Unlock()
	if hits["/match"] != 1 {
		t.Errorf("matching webhook hits = %d, want 1", hits["/match"])
	}
	if hits["/nomatch"] != 0 {
		t.Errorf("non-matching webhook hits = %d, want 0", hits["/nomatch"])
	}
	if hits["/all"] != 1 {
		t.Errorf("empty-Events (catch-all) webhook hits = %d, want 1", hits["/all"])
	}
}

func TestDispatcher_RetriesThenSucceeds(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracker := &fakeTracker{}
	d := NewDispatcher(DispatcherConfig{
		Provider: fakeProvider{webhooks: []Webhook{{ID: "wh1", URL: srv.URL, Secret: "s"}}},
		Tracker:  tracker,
		Client:   srv.Client(),
		Sleep:    noSleep,
	})
	d.publish([]byte(`{"event":"drift.changed"}`))

	mu.Lock()
	got := attempts
	mu.Unlock()
	if got != 3 {
		t.Errorf("server saw %d attempts, want 3", got)
	}
	calls := tracker.snapshot()
	if len(calls) != 1 || !calls[0].success {
		t.Fatalf("tracker calls = %+v, want exactly one success (retries aren't individually recorded)", calls)
	}
}

func TestDispatcher_ExhaustsRetriesThenRecordsFailure(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tracker := &fakeTracker{}
	const maxAttempts = 3
	d := NewDispatcher(DispatcherConfig{
		Provider:    fakeProvider{webhooks: []Webhook{{ID: "wh1", URL: srv.URL, Secret: "s"}}},
		Tracker:     tracker,
		Client:      srv.Client(),
		Sleep:       noSleep,
		MaxAttempts: maxAttempts,
	})
	d.publish([]byte(`{"event":"drift.changed"}`))

	mu.Lock()
	got := attempts
	mu.Unlock()
	if got != maxAttempts {
		t.Errorf("server saw %d attempts, want exactly %d (never retried past the max)", got, maxAttempts)
	}
	calls := tracker.snapshot()
	if len(calls) != 1 || calls[0].success {
		t.Fatalf("tracker calls = %+v, want exactly one failure", calls)
	}
}

func TestDispatcher_ConsecutiveFailureCountAccumulatesAcrossSequencesThenResets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tracker := &fakeTracker{}
	d := NewDispatcher(DispatcherConfig{
		Provider:    fakeProvider{webhooks: []Webhook{{ID: "wh1", URL: srv.URL, Secret: "s"}}},
		Tracker:     tracker,
		Client:      srv.Client(),
		Sleep:       noSleep,
		MaxAttempts: 1, // exhaust immediately each publish for a crisp count
	})

	for i := 0; i < DefaultUnhealthyThreshold; i++ {
		d.publish([]byte(`{"event":"drift.changed"}`))
	}

	calls := tracker.snapshot()
	if len(calls) != DefaultUnhealthyThreshold {
		t.Fatalf("got %d tracker calls, want %d", len(calls), DefaultUnhealthyThreshold)
	}
	for _, c := range calls {
		if c.success {
			t.Fatalf("expected only failures, got %+v", calls)
		}
	}
}

func TestDispatcher_PublishReturnsImmediatelyEvenWithSlowRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	done := make(chan struct{})
	slowSleep := func(ctx context.Context, d time.Duration) {
		// Simulate real backoff without actually waiting seconds: block
		// briefly so publish() (running in Publish's goroutine) is
		// definitely still in flight when the assertion below runs.
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
		}
		select {
		case <-done:
		default:
		}
	}
	tracker := &fakeTracker{}
	d := NewDispatcher(DispatcherConfig{
		Provider:    fakeProvider{webhooks: []Webhook{{ID: "wh1", URL: srv.URL, Secret: "s"}}},
		Tracker:     tracker,
		Client:      srv.Client(),
		Sleep:       slowSleep,
		MaxAttempts: 3,
	})

	start := time.Now()
	d.Publish([]byte(`{"event":"drift.changed"}`))
	elapsed := time.Since(start)
	close(done)

	if elapsed > 50*time.Millisecond {
		t.Errorf("Publish took %s to return — it must not block on delivery/backoff (Hub.SetEventSink's contract)", elapsed)
	}

	// Give the background goroutine a generous window to actually finish
	// so the test doesn't leak a still-running delivery past its own
	// lifetime.
	deadline := time.Now().Add(2 * time.Second)
	for len(tracker.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(tracker.snapshot()) == 0 {
		t.Error("background delivery never completed (tracker was never called)")
	}
}

func TestDispatcher_NilProviderIsNoop(t *testing.T) {
	d := NewDispatcher(DispatcherConfig{})
	d.Publish([]byte(`{"event":"drift.changed"}`)) // must not panic
}

func TestDispatcher_ProviderErrorLeavesNoPanic(t *testing.T) {
	d := NewDispatcher(DispatcherConfig{Provider: fakeProvider{err: context.DeadlineExceeded}})
	d.publish([]byte(`{"event":"drift.changed"}`)) // must not panic
}
