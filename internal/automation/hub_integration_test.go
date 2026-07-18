package automation_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/automation"
	"github.com/bgovanlu/vnprox/internal/topology"
)

type fakeWebhookProvider struct{ webhooks []automation.Webhook }

func (f fakeWebhookProvider) Webhooks(context.Context) ([]automation.Webhook, error) {
	return f.webhooks, nil
}

type fakeFailureTracker struct{}

func (fakeFailureTracker) RecordSuccess(context.Context, string, int64) error { return nil }
func (fakeFailureTracker) RecordFailure(context.Context, string, int64, string) (int, error) {
	return 0, nil
}

// TestHubEventSink_DriftChangedDeliversOneSignedWebhookPOST is T-1104's
// acceptance criterion 4's webhook half: register a target, trigger a
// drift finding (simulated here as cmd/vnproxd/drift.go's own
// `ws.Broadcast(topicDrift, data)` call would after a drift cycle changes)
// → the registered webhook receives exactly one signed POST, over the
// real internal/topology.Hub -> automation.Dispatcher wiring
// (Hub.SetEventSink), not a fake seam on either side. A tampered copy of
// the delivered body then fails signature verification.
func TestHubEventSink_DriftChangedDeliversOneSignedWebhookPOST(t *testing.T) {
	var mu sync.Mutex
	var receivedBodies [][]byte
	var receivedSigs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedBodies = append(receivedBodies, body)
		receivedSigs = append(receivedSigs, r.Header.Get(automation.HeaderSignature))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := "s3cret"
	dispatcher := automation.NewDispatcher(automation.DispatcherConfig{
		Provider: fakeWebhookProvider{webhooks: []automation.Webhook{{ID: "wh1", URL: srv.URL, Secret: secret}}},
		Tracker:  fakeFailureTracker{},
		Client:   srv.Client(),
		Sleep:    func(context.Context, time.Duration) {},
	})

	hub := topology.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	hub.SetEventSink(dispatcher.Publish)

	// Simulate cmd/vnproxd/drift.go's OnChange hook broadcasting
	// drift.changed after a cycle's finding set changed.
	driftPayload := []byte(`{"event":"drift.changed","count":1}`)
	hub.Broadcast("drift", driftPayload)

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(receivedBodies)
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(receivedBodies) != 1 {
		t.Fatalf("webhook target received %d POSTs, want exactly 1: %v", len(receivedBodies), receivedBodies)
	}
	if string(receivedBodies[0]) != string(driftPayload) {
		t.Errorf("delivered body = %s, want the exact drift.changed envelope %s", receivedBodies[0], driftPayload)
	}
	if !automation.VerifySignature([]byte(secret), receivedBodies[0], receivedSigs[0]) {
		t.Fatalf("delivered signature %q does not verify against the delivered body", receivedSigs[0])
	}

	tampered := append([]byte(nil), receivedBodies[0]...)
	tampered = append(tampered, 'x')
	if automation.VerifySignature([]byte(secret), tampered, receivedSigs[0]) {
		t.Error("a tampered body must fail signature verification, but it verified")
	}
}
