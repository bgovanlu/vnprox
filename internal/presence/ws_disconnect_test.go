package presence_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/presence"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// observedHub wraps the real presence service as the hub's ConnObserver and
// signals a channel once ConnClosed has RETURNED.
//
// It exists so T-2805 AC3 can be asserted deterministically. The obvious
// alternative — close the socket, then poll the lock table until it empties
// — is a bet on scheduling, and this repository has five recorded sightings
// of load-sensitive tests failing under CPU pressure. The hub calls
// ConnClosed synchronously on its own teardown path, so waiting for that
// call to complete is an exact happens-before edge with no timing in it: the
// select's timeout below is a deadlock guard, never the thing being timed.
type observedHub struct {
	topology.ConnObserver
	closed chan string
}

func (o observedHub) ConnClosed(connID string) {
	o.ConnObserver.ConnClosed(connID)
	o.closed <- connID
}

// serveWSAs mounts hub.ServeWS behind a fake auth middleware that reads the
// principal from two headers the dialling test controls. Real requests get
// their auth.Identity from internal/auth's middleware chain; this package
// only needs the identity to arrive, not to be produced.
func serveWSAs(hub *topology.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := auth.Identity{
			Username:  r.Header.Get("X-Test-User"),
			SessionID: r.Header.Get("X-Test-Session"),
		}
		hub.ServeWS(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	}
}

// TestWSDisconnect_ReleasesTheSessionsLocks is T-2805 AC3, asserted the way
// the card demands: by DROPPING THE CONNECTION, not by calling a release
// endpoint. The real failure mode is a closed laptop — a browser that never
// says goodbye — and an endpoint-driven test says nothing about it.
//
// The control leg is deliberate and load-bearing: before the socket is
// closed, the lock is proven held AND proven to warn a second stager. Only
// then does the disconnect happen. Without that, "no lock afterwards" would
// pass just as well against a build that never took one.
func TestWSDisconnect_ReleasesTheSessionsLocks(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	hub := topology.NewHub(testLogger())
	closed := make(chan string, 4)
	hub.SetConnObserver(observedHub{ConnObserver: f.svc, closed: closed})

	srv := httptest.NewServer(serveWSAs(hub))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{
		"X-Test-User":    {"alice@pam"},
		"X-Test-Session": {"sess-alice"},
	}})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}

	// A second operator, still connected throughout, whose lock must survive
	// alice's disconnect untouched.
	bobConn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{
		"X-Test-User":    {"bob@pam"},
		"X-Test-Session": {"sess-bob"},
	}})
	if err != nil {
		t.Fatalf("websocket.Dial (bob): %v", err)
	}
	defer func() { _ = bobConn.Close(websocket.StatusNormalClosure, "") }()

	if _, stageErr := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false); stageErr != nil {
		t.Fatalf("alice Stage: %v", stageErr)
	}
	if _, stageErr := f.svc.Stage(ctx, "cs-bob", []string{"bridge:pve2:vmbr0"}, bob(), false); stageErr != nil {
		t.Fatalf("bob Stage: %v", stageErr)
	}

	// Control leg: the lock is genuinely held, and genuinely warns, at the
	// moment the connection is about to drop.
	held, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks before disconnect: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("locks before disconnect = %+v, want two", held)
	}
	warned, err := f.svc.Stage(ctx, "cs-carol", []string{"bridge:pve1:vmbr0"},
		presence.Principal{Username: "carol@pam", SessionID: "sess-carol"}, false)
	if err != nil {
		t.Fatalf("carol Stage before disconnect: %v", err)
	}
	if len(warned.Conflicts) != 1 || warned.Conflicts[0].Holder != "alice@pam" {
		t.Fatalf("carol saw %+v before the disconnect, want a conflict held by alice@pam — the control leg failed, so nothing below proves anything", warned.Conflicts)
	}

	// The disconnect. CloseNow tears the socket down without a close
	// handshake, which is as close to "the laptop lid shut" as a test gets.
	if closeErr := conn.CloseNow(); closeErr != nil {
		t.Fatalf("CloseNow: %v", closeErr)
	}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the hub never reported the dropped connection to its observer")
	}

	after, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks after disconnect: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("locks after alice's connection dropped = %+v, want only bob's", after)
	}
	if after[0].Holder != "bob@pam" {
		t.Errorf("surviving lock holder = %q, want bob@pam — a disconnect must free its own session's locks and nobody else's", after[0].Holder)
	}

	// The entity is genuinely free again: a fresh stager takes it unwarned.
	free, err := f.svc.Stage(ctx, "cs-carol2", []string{"bridge:pve1:vmbr0"},
		presence.Principal{Username: "carol@pam", SessionID: "sess-carol"}, false)
	if err != nil {
		t.Fatalf("carol Stage after disconnect: %v", err)
	}
	if free.Warned() {
		t.Errorf("staging after the holder disconnected still warned: %+v", free)
	}
}

// TestWSDisconnect_DropsPresenceForThatConnection: the same teardown that
// frees the locks must also remove the departed operator from every scope
// they were on, or the UI shows a colleague who left.
func TestWSDisconnect_DropsPresenceForThatConnection(t *testing.T) {
	f := newFixture(t, 15*time.Minute)

	hub := topology.NewHub(testLogger())
	closed := make(chan string, 4)
	hub.SetConnObserver(observedHub{ConnObserver: f.svc, closed: closed})

	srv := httptest.NewServer(serveWSAs(hub))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{
		"X-Test-User":    {"alice@pam"},
		"X-Test-Session": {"sess-alice"},
	}})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}

	// Subscribing to the presence topic IS the declaration of presence: no
	// second channel, no separate "I am here" call (docs/api.md's WebSocket
	// section).
	subscribed := make(chan struct{})
	go func() {
		defer close(subscribed)
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer writeCancel()
		_ = conn.Write(writeCtx, websocket.MessageText, []byte(`{"subscribe":["presence:changeset:cs-1"]}`))
	}()
	<-subscribed

	// The subscribe travels over the wire, so unlike the disconnect above
	// there is no happens-before edge to wait on — this is the one bounded
	// poll in the file, and it exits the instant the hub has processed the
	// frame rather than sleeping for a fixed period.
	deadline := time.Now().Add(10 * time.Second)
	for f.svc.Scope(presence.ChangesetScope("cs-1")).Count == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := f.svc.Scope(presence.ChangesetScope("cs-1")); got.Count != 1 {
		t.Fatalf("presence after subscribe = %+v, want alice present", got)
	}

	if err := conn.CloseNow(); err != nil {
		t.Fatalf("CloseNow: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the hub never reported the dropped connection to its observer")
	}

	if got := f.svc.Scope(presence.ChangesetScope("cs-1")); got.Count != 0 {
		t.Errorf("presence after the connection dropped = %+v, want nobody", got)
	}
}
