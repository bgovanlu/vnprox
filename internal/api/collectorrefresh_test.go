// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// fakeRefresher records the scope it was asked for and returns a canned
// delta/error.
type fakeRefresher struct {
	err    error
	delta  inventory.Delta
	scopes []inventory.Scope
}

func (f *fakeRefresher) RefreshNow(_ context.Context, scope inventory.Scope) (inventory.Delta, error) {
	f.scopes = append(f.scopes, scope)
	return f.delta, f.err
}

func newCollectorRefreshRouter(refresher CollectorRefresher, audit collectorRefreshAuditor, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{},
		CollectorRefresher: refresher, CollectorAudit: audit,
	})
}

func postRefresh(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(http.MethodPost, "/api/v1/collectors/refresh", nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, "/api/v1/collectors/refresh", bytes.NewBufferString(body))
	}
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCollectorRefresh_NotMountedWithoutCollector(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	if rec := postRefresh(t, r, "{}"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestCollectorRefresh_RequiresNetWrite(t *testing.T) {
	f := &fakeRefresher{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetWrite: false},
	}
	if rec := postRefresh(t, newCollectorRefreshRouter(f, nil, auth), "{}"); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if len(f.scopes) != 0 {
		t.Error("a session without netWrite must not cause a poll")
	}
}

// An empty body is the common case — the cluster-wide staleness banner's
// retry sends no scope at all — and must not be a validation error.
func TestCollectorRefresh_EmptyBodyRefreshesEverything(t *testing.T) {
	f := &fakeRefresher{}
	rec := postRefresh(t, newCollectorRefreshRouter(f, nil, fullCapsAuth("alice")), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if len(f.scopes) != 1 || f.scopes[0].Node != "" {
		t.Errorf("scopes = %+v, want one cluster-wide scope", f.scopes)
	}
}

func TestCollectorRefresh_ScopesToANode(t *testing.T) {
	f := &fakeRefresher{}
	rec := postRefresh(t, newCollectorRefreshRouter(f, nil, fullCapsAuth("alice")), `{"node":"pve2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if len(f.scopes) != 1 || f.scopes[0].Node != "pve2" {
		t.Errorf("scopes = %+v, want one scope for pve2", f.scopes)
	}
	var resp collectorRefreshResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Echoed so a client cannot render this result against a different
	// node's banner.
	if resp.Node != "pve2" {
		t.Errorf("response node = %q, want pve2", resp.Node)
	}
}

func TestCollectorRefresh_RejectsAMalformedBody(t *testing.T) {
	f := &fakeRefresher{}
	rec := postRefresh(t, newCollectorRefreshRouter(f, nil, fullCapsAuth("alice")), `{"nope":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if len(f.scopes) != 0 {
		t.Error("a malformed request must not cause a poll")
	}
}

// A poll that fails is still a 200 with the error in the body. The whole
// point of the retry button is to answer "did it work this time?", and the
// most useful answer is often "no, same error as before" — which is data,
// not an HTTP failure. A 5xx here would make a working route look broken
// every time a peer is down.
func TestCollectorRefresh_ReportsAFailedPollInTheBody(t *testing.T) {
	f := &fakeRefresher{err: errors.New("host links (pve001): context canceled")}
	rec := postRefresh(t, newCollectorRefreshRouter(f, nil, fullCapsAuth("alice")), "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a failed poll is a reported outcome, not a broken route", rec.Code)
	}
	var resp collectorRefreshResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "host links (pve001): context canceled" {
		t.Errorf("error = %q, want the poll's own message verbatim", resp.Error)
	}
}

func TestCollectorRefresh_ReportsWhetherAnythingChanged(t *testing.T) {
	// "It worked but nothing moved" and "nothing happened" look identical
	// without this.
	f := &fakeRefresher{delta: inventory.Delta{Added: []inventory.Ref{{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}}}}
	rec := postRefresh(t, newCollectorRefreshRouter(f, nil, fullCapsAuth("alice")), "{}")
	var resp collectorRefreshResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Changed {
		t.Error("changed = false for a non-empty delta")
	}
}

// The rate limit is the reason this route is safe to put behind a button.
// It is enforced server-side because a client-side throttle protects PVE
// only from the clients that implement it.
func TestCollectorRefresh_RateLimited(t *testing.T) {
	f := &fakeRefresher{}
	r := newCollectorRefreshRouter(f, nil, fullCapsAuth("alice"))

	if rec := postRefresh(t, r, "{}"); rec.Code != http.StatusOK {
		t.Fatalf("first refresh: status = %d, want 200", rec.Code)
	}
	rec := postRefresh(t, r, "{}")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second refresh: status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("no Retry-After header on a rate-limited response")
	}
	if len(f.scopes) != 1 {
		t.Errorf("polled %d times, want 1 — the refused call must not reach the collector", len(f.scopes))
	}
}

func TestCollectorRefreshLimiter_AllowsAgainAfterTheInterval(t *testing.T) {
	// Driven by an injected clock rather than a sleep, so this stays a unit
	// test rather than a ten-second one.
	l := &collectorRefreshLimiter{}
	base := time.Unix(1_700_000_000, 0)
	if ok, _ := l.allow(base); !ok {
		t.Fatal("first call refused")
	}
	if ok, wait := l.allow(base.Add(collectorRefreshMinInterval - time.Second)); ok {
		t.Error("a call inside the interval was allowed")
	} else if wait <= 0 {
		t.Errorf("wait = %v, want a positive duration", wait)
	}
	if ok, _ := l.allow(base.Add(collectorRefreshMinInterval)); !ok {
		t.Error("a call exactly at the interval boundary was refused")
	}
}

func TestRetryAfterSeconds_RoundsUp(t *testing.T) {
	// A client obeying Retry-After exactly must not be refused a second
	// time for being a few hundred milliseconds early.
	// Field order is fieldalignment's, not the reading order.
	tests := []struct {
		want string
		wait time.Duration
	}{
		{"1", 10 * time.Millisecond},
		{"1", time.Second},
		{"2", time.Second + time.Millisecond},
		{"10", 10 * time.Second},
	}
	for _, tt := range tests {
		if got := retryAfterSeconds(tt.wait); got != tt.want {
			t.Errorf("retryAfterSeconds(%v) = %q, want %q", tt.wait, got, tt.want)
		}
	}
}

func TestCollectorRefresh_Audited(t *testing.T) {
	audit := &fakeAuditor{}
	f := &fakeRefresher{err: errors.New("boom")}
	if rec := postRefresh(t, newCollectorRefreshRouter(f, audit, fullCapsAuth("alice")), "{}"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(audit.entries))
	}
	e := audit.entries[0]
	if e.Action != "collector.refresh" {
		t.Errorf("action = %q, want collector.refresh", e.Action)
	}
	if e.Username != "alice" {
		t.Errorf("username = %q, want alice", e.Username)
	}
	// A failed refresh is audited as a failure, not silently as an "ok" —
	// an audit trail that records only successes is worse than none,
	// because it reads as though nothing was attempted.
	if e.Result != "error" {
		t.Errorf("result = %q, want error", e.Result)
	}
	if e.Target.String != "cluster" {
		t.Errorf("target = %q, want cluster", e.Target.String)
	}
}
