// SPDX-License-Identifier: Apache-2.0

package pve_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// ticketInspector wraps the mock server, recording the "password" form
// value of every POST /access/ticket and optionally rejecting
// ticket-as-password renewals (passwords that look like a PVE ticket)
// with a 401 — simulating a server where the renewal shortcut fails so
// the client's plaintext-password fallback can be observed.
type ticketInspector struct {
	inner            http.Handler
	passwords        []string
	mu               sync.Mutex
	rejectTicketPass bool
}

func (h *ticketInspector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api2/json/access/ticket" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		form, _ := url.ParseQuery(string(body))
		password := form.Get("password")
		h.mu.Lock()
		h.passwords = append(h.passwords, password)
		reject := h.rejectTicketPass && strings.HasPrefix(password, "PVE:")
		h.mu.Unlock()
		if reject {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"data":null,"message":"authentication failure"}`))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	h.inner.ServeHTTP(w, r)
}

func (h *ticketInspector) recorded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.passwords...)
}

// TestTicketAuth_RenewalBeatsGenuineServerSideExpiry validates renewal
// against real server-side ticket expiry (pvemock's WithTicketTTL), not
// just the client's own timer: with the renewal threshold below the
// server TTL, the client keeps working well past the original ticket's
// lifetime because each renewal (via ticket-as-password) mints a fresh
// server-side ticket.
func TestTicketAuth_RenewalBeatsGenuineServerSideExpiry(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	const ttl = 200 * time.Millisecond
	inspector := &ticketInspector{inner: pvemock.NewServer(f, pvemock.WithTicketTTL(ttl))}
	ts := httptest.NewServer(inspector)
	defer ts.Close()

	c, err := pve.New(pve.Config{
		APIURL:   ts.URL,
		Auth:     pve.AuthTicket,
		Username: "root@pam",
		Password: "vnprox-mock",
		// Renew comfortably before the 200ms server-side TTL.
		TicketRenewAfter: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()

	// Keep issuing requests for ~3x the server TTL. Every one must
	// succeed — a single missed renewal would 401 once the original
	// ticket expires server-side.
	deadline := time.Now().Add(3 * ttl)
	for time.Now().Before(deadline) {
		if _, statusErr := c.ClusterStatus(ctx); statusErr != nil {
			t.Fatalf("ClusterStatus failed %v after start (server TTL %v): %v", 3*ttl-time.Until(deadline), ttl, statusErr)
		}
		time.Sleep(15 * time.Millisecond)
	}

	// Renewal must actually have happened via ticket-as-password: the
	// second and every later /access/ticket call carries a ticket, not
	// the plaintext password.
	passwords := inspector.recorded()
	if len(passwords) < 2 {
		t.Fatalf("only %d ticket logins recorded, want at least an initial login plus one renewal", len(passwords))
	}
	if passwords[0] != "vnprox-mock" {
		t.Errorf("first login password = %q, want the plaintext password", passwords[0])
	}
	for i, p := range passwords[1:] {
		if !strings.HasPrefix(p, "PVE:") {
			t.Errorf("renewal %d used password %q, want a ticket-as-password (PVE:...) value", i+1, p)
		}
	}
}

// TestTicketAuth_NoRenewalFailsWithAuthErrorAfterServerExpiry is the
// negative counterpart: with renewal effectively disabled (threshold far
// above the server TTL), requests past the TTL fail with *pve.ErrPVEAuth —
// proving the server-side expiry is genuine and the client isn't silently
// re-authenticating.
func TestTicketAuth_NoRenewalFailsWithAuthErrorAfterServerExpiry(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	ts := httptest.NewServer(pvemock.NewServer(f, pvemock.WithTicketTTL(40*time.Millisecond)))
	defer ts.Close()

	c, err := pve.New(pve.Config{
		APIURL:   ts.URL,
		Auth:     pve.AuthTicket,
		Username: "root@pam",
		Password: "vnprox-mock",
		// Renewal threshold far beyond the server TTL: the client never
		// renews within this test, so the original ticket expires.
		TicketRenewAfter: time.Hour,
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()

	if _, statusErr := c.ClusterStatus(ctx); statusErr != nil {
		t.Fatalf("ClusterStatus (fresh ticket): %v", statusErr)
	}

	time.Sleep(80 * time.Millisecond) // let the ticket expire server-side

	_, err = c.ClusterStatus(ctx)
	if err == nil {
		t.Fatalf("expected an auth error once the ticket expired server-side, got none")
	}
	var authErr *pve.ErrPVEAuth
	if !errors.As(err, &authErr) {
		t.Fatalf("errors.As(err, &authErr) failed; got %#v (%v)", err, err)
	}
}

// TestTicketAuth_PlaintextPasswordDroppedAfterTicketRenewal proves the
// F-15b behavior end to end: once a ticket-as-password renewal succeeds,
// the client stops retaining the plaintext password — observable because
// a later renewal attempt with an expired ticket has no password to fall
// back on and must fail with *pve.ErrPVEAuth instead of silently
// re-logging-in with the original credentials.
func TestTicketAuth_PlaintextPasswordDroppedAfterTicketRenewal(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	const ttl = 250 * time.Millisecond
	inspector := &ticketInspector{inner: pvemock.NewServer(f, pvemock.WithTicketTTL(ttl))}
	ts := httptest.NewServer(inspector)
	defer ts.Close()

	c, err := pve.New(pve.Config{
		APIURL:           ts.URL,
		Auth:             pve.AuthTicket,
		Username:         "root@pam",
		Password:         "vnprox-mock",
		TicketRenewAfter: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()

	// Initial login (plaintext), then one successful ticket-as-password
	// renewal, after which the plaintext password is dropped.
	if _, statusErr := c.ClusterStatus(ctx); statusErr != nil {
		t.Fatalf("ClusterStatus (initial): %v", statusErr)
	}
	time.Sleep(60 * time.Millisecond) // past renewAfter, well within TTL
	if _, renewalErr := c.ClusterStatus(ctx); renewalErr != nil {
		t.Fatalf("ClusterStatus (triggering ticket renewal): %v", renewalErr)
	}
	passwords := inspector.recorded()
	if len(passwords) != 2 || !strings.HasPrefix(passwords[1], "PVE:") {
		t.Fatalf("recorded ticket-login passwords = %q, want [plaintext, PVE:...]", passwords)
	}

	// Let the renewed ticket expire server-side, then force another
	// renewal: ticket-as-password now fails (expired) and there is no
	// plaintext fallback left, so the call must surface an auth error —
	// and must NOT have replayed "vnprox-mock".
	time.Sleep(ttl + 50*time.Millisecond)
	_, err = c.ClusterStatus(ctx)
	if err == nil {
		t.Fatalf("expected an auth error (expired ticket, password dropped), got none")
	}
	var authErr *pve.ErrPVEAuth
	if !errors.As(err, &authErr) {
		t.Fatalf("errors.As(err, &authErr) failed; got %#v (%v)", err, err)
	}
	for i, p := range inspector.recorded()[1:] {
		if p == "vnprox-mock" {
			t.Errorf("ticket login %d replayed the plaintext password after it should have been dropped", i+1)
		}
	}
}

// TestTicketAuth_FallsBackToPasswordWhenTicketRenewalRejected covers the
// other F-15b branch: against a server that rejects the ticket-as-password
// shortcut (simulated by the inspector 401-ing any PVE:-shaped password),
// the client falls back to the stored plaintext password and keeps
// working. Real-PVE behavior for the shortcut still needs hardware
// validation, which is exactly why this fallback exists.
func TestTicketAuth_FallsBackToPasswordWhenTicketRenewalRejected(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	inspector := &ticketInspector{inner: pvemock.NewServer(f), rejectTicketPass: true}
	ts := httptest.NewServer(inspector)
	defer ts.Close()

	c, err := pve.New(pve.Config{
		APIURL:           ts.URL,
		Auth:             pve.AuthTicket,
		Username:         "root@pam",
		Password:         "vnprox-mock",
		TicketRenewAfter: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()

	if _, statusErr := c.ClusterStatus(ctx); statusErr != nil {
		t.Fatalf("ClusterStatus (initial): %v", statusErr)
	}
	time.Sleep(40 * time.Millisecond)
	// Renewal: ticket-as-password 401s, plaintext fallback succeeds, the
	// request goes through.
	if _, renewalErr := c.ClusterStatus(ctx); renewalErr != nil {
		t.Fatalf("ClusterStatus (renewal with rejected ticket-as-password): %v", renewalErr)
	}

	passwords := inspector.recorded()
	if len(passwords) != 3 {
		t.Fatalf("recorded %d ticket logins %q, want 3 (initial, rejected ticket renewal, password fallback)", len(passwords), passwords)
	}
	if !strings.HasPrefix(passwords[1], "PVE:") {
		t.Errorf("second login password = %q, want the ticket-as-password attempt", passwords[1])
	}
	if passwords[2] != "vnprox-mock" {
		t.Errorf("third login password = %q, want the plaintext fallback", passwords[2])
	}
}
