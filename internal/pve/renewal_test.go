package pve_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// countingHandler wraps an http.Handler and counts POST /access/ticket
// calls, so the renewal test can observe a *real* second login happen
// against the mock server, not just assert on the client's internal
// timer logic in isolation (T-101 acceptance criterion 1).
type countingHandler struct {
	inner http.Handler

	mu          sync.Mutex
	ticketCalls int
}

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api2/json/access/ticket" {
		h.mu.Lock()
		h.ticketCalls++
		h.mu.Unlock()
	}
	h.inner.ServeHTTP(w, r)
}

func (h *countingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ticketCalls
}

func TestTicketAuth_RenewsBeforeShortTTLExpiry(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	counter := &countingHandler{inner: pvemock.NewServer(f)}
	ts := httptest.NewServer(counter)
	defer ts.Close()

	c, err := pve.New(pve.Config{
		APIURL:   ts.URL,
		Auth:     pve.AuthTicket,
		Username: "root@pam",
		Password: "vnprox-mock",
		// A short-TTL "fixture" for the renewal threshold: renew after
		// only 20ms instead of the production default of 90 minutes, so
		// the test can observe a real renewal within its own runtime.
		TicketRenewAfter: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()

	if _, err := c.ClusterStatus(ctx); err != nil {
		t.Fatalf("ClusterStatus (initial login): %v", err)
	}
	if got := counter.count(); got != 1 {
		t.Fatalf("ticket calls after first request = %d, want 1", got)
	}

	// Immediately calling again, within the renewal threshold, must not
	// trigger another login.
	if _, err := c.ClusterStatus(ctx); err != nil {
		t.Fatalf("ClusterStatus (within threshold): %v", err)
	}
	if got := counter.count(); got != 1 {
		t.Fatalf("ticket calls after second (fast) request = %d, want still 1 (no renewal yet)", got)
	}

	// Wait past the renewal threshold, then call again: this must
	// trigger a real renewal (a second POST /access/ticket) before the
	// wrapped request.
	time.Sleep(40 * time.Millisecond)
	if _, err := c.ClusterStatus(ctx); err != nil {
		t.Fatalf("ClusterStatus (after threshold): %v", err)
	}
	if got := counter.count(); got != 2 {
		t.Fatalf("ticket calls after threshold elapsed = %d, want 2 (a renewal should have happened)", got)
	}
}
