// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// newTimerHarness mounts a single Server with a spyTimerAgent and returns a
// Client configured to reach it, mirroring twoDaemonHarness's shape but
// scoped to the /api/peer/timer/* + host/discard-staged routes this file
// tests.
type timerHarness struct {
	now    time.Time
	timers *spyTimerAgent
	writer *spyHostWriter
	client *Client
	node   Peer
}

func newTimerHarness(t *testing.T) *timerHarness {
	t.Helper()
	h := &timerHarness{now: time.Unix(1_700_000_000, 0)}

	writer := newSpyHostWriter()
	timers := newSpyTimerAgent()
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret),
		Reader:  newSpyHostReader(),
		Writer:  writer,
		Timers:  timers,
		Version: "test",
		Logger:  discardLogger(),
		Now:     func() time.Time { return h.now },
	})
	h.timers = timers
	h.writer = writer

	ts := mountedTestServer(t, srv)
	h.node = Peer{Node: "pve3", Addr: ts.Listener.Addr().String()}
	h.client = NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
		Now:     func() time.Time { return h.now },
	})
	return h
}

func TestClient_ArmCancelStatusTimer(t *testing.T) {
	h := newTimerHarness(t)
	ctx := t.Context()

	rec, err := h.client.ArmTimer(ctx, h.node, "cs-1", "pve3", "auto lo\n", 1_700_000_120)
	if err != nil {
		t.Fatalf("ArmTimer: %v", err)
	}
	if rec.Status != TimerArmed || rec.ChangesetID != "cs-1" || rec.Node != "pve3" || rec.Deadline != 1_700_000_120 {
		t.Errorf("ArmTimer() = %+v, want armed record", rec)
	}

	h.now = h.now.Add(time.Second) // each request must carry a distinct signature (replay cache)
	status, err := h.client.TimerStatus(ctx, h.node, "cs-1", "pve3")
	if err != nil {
		t.Fatalf("TimerStatus: %v", err)
	}
	if status.Status != TimerArmed {
		t.Errorf("TimerStatus() = %+v, want armed", status)
	}

	h.now = h.now.Add(time.Second)
	cancelled, err := h.client.CancelTimer(ctx, h.node, "cs-1", "pve3")
	if err != nil {
		t.Fatalf("CancelTimer: %v", err)
	}
	if cancelled.Status != TimerCancelled {
		t.Errorf("CancelTimer() = %+v, want cancelled", cancelled)
	}

	h.now = h.now.Add(time.Second)
	status, err = h.client.TimerStatus(ctx, h.node, "cs-1", "pve3")
	if err != nil {
		t.Fatalf("TimerStatus after cancel: %v", err)
	}
	if status.Status != TimerCancelled {
		t.Errorf("TimerStatus() after cancel = %+v, want cancelled", status)
	}
}

func TestClient_TimerStatus_NotFound(t *testing.T) {
	h := newTimerHarness(t)
	ctx := t.Context()

	_, err := h.client.TimerStatus(ctx, h.node, "cs-never-armed", "pve3")
	if !errors.Is(err, ErrTimerNotFound) {
		t.Fatalf("TimerStatus(never armed) err = %v, want ErrTimerNotFound", err)
	}

	h.now = h.now.Add(time.Second)
	_, err = h.client.CancelTimer(ctx, h.node, "cs-never-armed", "pve3")
	if !errors.Is(err, ErrTimerNotFound) {
		t.Fatalf("CancelTimer(never armed) err = %v, want ErrTimerNotFound", err)
	}
}

func TestClient_DiscardStaged(t *testing.T) {
	h := newTimerHarness(t)
	ctx := t.Context()

	if err := h.client.DiscardStaged(ctx, h.node, "pve3"); err != nil {
		t.Fatalf("DiscardStaged: %v", err)
	}
	if len(h.writer.discarded) != 1 || h.writer.discarded[0] != "pve3" {
		t.Errorf("writer.discarded = %v, want [pve3]", h.writer.discarded)
	}
}

// TestServer_TimerRoutes_UnconfiguredAgent503s asserts a Server with no
// Timers wired (the T-301-only wiring shape, before this daemon's local
// timer agent is constructed) fails closed rather than panicking or 404ing
// silently — matching the existing Reader/Writer-nil convention.
func TestServer_TimerRoutes_UnconfiguredAgent503s(t *testing.T) {
	srv, _, _ := newTestServer(t, time.Now)
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
	})
	node := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}
	ctx := t.Context()

	if _, err := client.ArmTimer(ctx, node, "cs-1", "pve1", "x", 1); err == nil {
		t.Fatal("ArmTimer with no Timers configured: want error, got nil")
	} else {
		var respErr *ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("ArmTimer err = %v, want 503 peer_unavailable", err)
		}
	}
}

// TestServer_DiscardStaged_UnconfiguredWriter503s mirrors the existing
// stage/reload/restore nil-Writer coverage for the new discard-staged route.
func TestServer_DiscardStaged_UnconfiguredWriter503s(t *testing.T) {
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret),
		Reader:  newSpyHostReader(),
		Version: "test",
		Logger:  discardLogger(),
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
	})
	node := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	err := client.DiscardStaged(t.Context(), node, "pve1")
	var respErr *ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("DiscardStaged with no Writer: err = %v, want 503 peer_unavailable", err)
	}
}
