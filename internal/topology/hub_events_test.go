// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// serveWSWithIdentity wraps hub.ServeWS with a fake auth middleware, driven
// entirely by two request headers a dialing test controls (there is no
// production equivalent — real requests get their auth.Identity from
// internal/auth's SessionMiddleware/bearer-auth chain, exercised by
// internal/auth's own bearer_test.go and by internal/api's router-level
// integration tests): X-Test-Automation ("true" grants CapAutomation) and
// X-Test-Token-Id (echoed into Identity.TokenID, for CloseByTokenID tests).
func serveWSWithIdentity(hub *topology.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := auth.Identity{TokenID: r.Header.Get("X-Test-Token-Id")}
		if r.Header.Get("X-Test-Automation") == "true" {
			id.Caps = map[string]auth.Capabilities{"": {Automation: true}}
		}
		hub.ServeWS(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	}
}

func dialWSWithHeaders(t *testing.T, url string, headers http.Header) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	return c
}

func TestHub_EventsTopic_RequiresAutomationScope(t *testing.T) {
	hub := topology.NewHub(testLogger())
	srv := httptest.NewServer(serveWSWithIdentity(hub))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	automated := dialWSWithHeaders(t, wsURL, http.Header{"X-Test-Automation": {"true"}})
	defer func() { _ = automated.Close(websocket.StatusNormalClosure, "") }()
	plain := dialWSWithHeaders(t, wsURL, nil)
	defer func() { _ = plain.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sub := []byte(`{"subscribe":["events"]}`)
	if err := automated.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatalf("automated subscribe: %v", err)
	}
	if err := plain.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatalf("plain subscribe: %v", err)
	}
	waitForConns(t, hub, 2)

	hub.Broadcast("events", []byte(`{"event":"audit.appended","id":1}`))

	msg := readEvent(t, automated, 2*time.Second)
	if msg["event"] != "audit.appended" {
		t.Fatalf("automated connection event = %v, want audit.appended", msg["event"])
	}

	roCtx, roCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer roCancel()
	if _, _, err := plain.Read(roCtx); err == nil {
		t.Error("a connection without the automation scope must never receive anything on \"events\", even after requesting it")
	}
}

func TestHub_EventsTopic_ReusesExistingProducersWithoutDuplicateDelivery(t *testing.T) {
	hub := topology.NewHub(testLogger())
	srv := httptest.NewServer(serveWSWithIdentity(hub))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Subscribes to BOTH "changesets" and "events" — must receive the
	// reused changeset.status payload exactly once, not twice.
	both := dialWSWithHeaders(t, wsURL, http.Header{"X-Test-Automation": {"true"}})
	defer func() { _ = both.Close(websocket.StatusNormalClosure, "") }()
	// Subscribes to "events" only — must still receive the reused payload.
	eventsOnly := dialWSWithHeaders(t, wsURL, http.Header{"X-Test-Automation": {"true"}})
	defer func() { _ = eventsOnly.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := both.Write(ctx, websocket.MessageText, []byte(`{"subscribe":["changesets","events"]}`)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := eventsOnly.Write(ctx, websocket.MessageText, []byte(`{"subscribe":["events"]}`)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	waitForConns(t, hub, 2)

	payload := []byte(`{"event":"changeset.status","id":"cs1","status":"applying"}`)
	hub.Broadcast("changesets", payload)

	m1 := readEvent(t, both, 2*time.Second)
	if m1["event"] != "changeset.status" || m1["id"] != "cs1" {
		t.Fatalf("both-subscribed connection got %v, want the changeset.status payload", m1)
	}
	m2 := readEvent(t, eventsOnly, 2*time.Second)
	if m2["event"] != "changeset.status" || m2["id"] != "cs1" {
		t.Fatalf("events-only connection got %v, want the changeset.status payload", m2)
	}

	// The dual subscriber must not receive a second copy.
	roCtx, roCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer roCancel()
	if _, _, err := both.Read(roCtx); err == nil {
		t.Error("connection subscribed to both \"changesets\" and \"events\" received the reused event twice")
	}
}

func TestHub_EventsTopic_UnrelatedTopicsDoNotLeakIntoEvents(t *testing.T) {
	hub := topology.NewHub(testLogger())
	srv := httptest.NewServer(serveWSWithIdentity(hub))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	sub := dialWSWithHeaders(t, wsURL, http.Header{"X-Test-Automation": {"true"}})
	defer func() { _ = sub.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sub.Write(ctx, websocket.MessageText, []byte(`{"subscribe":["events"]}`)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	waitForConns(t, hub, 1)

	// "topology" is not in eventsSourceTopics — an events subscriber must
	// not receive topology.delta pushes.
	hub.Broadcast("topology", []byte(`{"event":"topology.delta","added":[],"updated":[],"removed":[]}`))

	roCtx, roCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer roCancel()
	if _, _, err := sub.Read(roCtx); err == nil {
		t.Error("an \"events\" subscriber received a topology.delta push — only the documented reused topics should feed \"events\"")
	}
}

func TestHub_SetEventSink_FiresForReusedTopicsAndDirectEventsOnly(t *testing.T) {
	hub := topology.NewHub(testLogger())

	var got [][]byte
	hub.SetEventSink(func(payload []byte) { got = append(got, payload) })

	hub.Broadcast("changesets", []byte(`{"event":"changeset.status"}`))
	hub.Broadcast("drift", []byte(`{"event":"drift.changed"}`))
	hub.Broadcast("findings", []byte(`{"event":"findings.changed"}`))
	hub.Broadcast("events", []byte(`{"event":"audit.appended"}`))
	hub.Broadcast("topology", []byte(`{"event":"topology.delta"}`))
	hub.Broadcast("flows", []byte(`{"event":"flow.batch"}`))

	if len(got) != 4 {
		t.Fatalf("eventSink fired %d times, want 4 (changesets, drift, findings, events — not topology/flows): %s", len(got), got)
	}
}

func TestHub_CloseByTokenID_ForceClosesOnlyMatchingConnections(t *testing.T) {
	hub := topology.NewHub(testLogger())
	srv := httptest.NewServer(serveWSWithIdentity(hub))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	revoked := dialWSWithHeaders(t, wsURL, http.Header{"X-Test-Token-Id": {"tok-revoked"}})
	defer func() { _ = revoked.Close(websocket.StatusNormalClosure, "") }()
	other := dialWSWithHeaders(t, wsURL, http.Header{"X-Test-Token-Id": {"tok-other"}})
	defer func() { _ = other.Close(websocket.StatusNormalClosure, "") }()
	waitForConns(t, hub, 2)

	// CloseByTokenID itself must return promptly — it uses CloseNow (an
	// immediate teardown), not the graceful Close handshake (which can
	// block up to 5s waiting for a peer ack that a revoked/hostile client
	// might never send); this bounds "within one server tick" from the
	// caller's (DELETE /tokens/{id}'s) perspective, not just the
	// connection's eventual teardown.
	closeStart := time.Now()
	n := hub.CloseByTokenID("tok-revoked")
	if n != 1 {
		t.Fatalf("CloseByTokenID returned %d, want 1", n)
	}
	if elapsed := time.Since(closeStart); elapsed > 1*time.Second {
		t.Errorf("CloseByTokenID took %s to return, want well under a second (it must use CloseNow, not the graceful Close handshake)", elapsed)
	}

	// The connection itself must already be unusable essentially
	// immediately after CloseByTokenID returns.
	readCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	start := time.Now()
	if _, _, err := revoked.Read(readCtx); err == nil {
		t.Fatal("the revoked-token connection should have been force-closed by the server")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Read() took %s to fail — looks like it hit the context timeout rather than an actual server-initiated close", elapsed)
	}

	deadline := time.Now().Add(1 * time.Second)
	for hub.ConnCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ConnCount() != 1 {
		t.Errorf("ConnCount() = %d after closing one of two connections, want 1", hub.ConnCount())
	}
}
