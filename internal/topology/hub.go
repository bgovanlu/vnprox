// SPDX-License-Identifier: Apache-2.0

package topology

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Subscription topics this hub understands producing events for. Clients
// may subscribe to other docs/api.md topics ("changesets", "metrics:<ref>",
// "tasks") without error — the hub just never has anything to send them,
// since those events are later tasks' responsibility.
const topicTopology = "topology"

// topicEvents is T-1104's automation firehose topic (docs/api.md's
// WebSocket section): a superset envelope reusing the existing
// changeset.status/drift.changed/findings.changed producers verbatim (see
// eventsSourceTopics below) plus the new audit.appended event, gated on the
// "automation" scope (auth.CapAutomation — the READ half as of
// T-3003-followup-01's split; internal/auth.forceReadOnly leaves it set, so
// this topic stays reachable in a read_only deployment) rather than plain
// netRead. Unlike every other topic this hub knows about, subscribing to it
// is itself an authorization decision (setTopics below), not merely
// "nothing to send yet".
const topicEvents = "events"

// eventsSourceTopics names the existing WS topics whose broadcasts are
// additionally fanned into topicEvents verbatim — the literal
// implementation of docs/api.md's "a SUPERSET envelope REUSING the
// existing changeset.status/drift.changed/findings.changed producers (do
// not duplicate them)": Broadcast below sends the exact same encoded
// payload a "changesets"/"drift"/"findings" subscriber would get to any
// "events" subscriber too, rather than a second producer re-deriving the
// same event. audit.appended (cmd/vnproxd's audit-repo hook) is not listed
// here because it has no topic of its own to reuse — it is broadcast
// directly to topicEvents.
var eventsSourceTopics = map[string]bool{
	topicChangesets: true,
	topicDrift:      true,
	topicFindings:   true,
}

// topicChangesets/topicDrift/topicFindings mirror the topic name string
// constants internal/change.Service, cmd/vnproxd/drift.go, and
// cmd/vnproxd/findings.go each already define privately in their own
// packages (this package has no import of any of them, by design — see
// Broadcaster's doc comment on internal/change.Service — so it cannot
// reference those constants directly and instead re-declares the same
// wire-documented strings here). Keeping them as named constants (rather
// than bare string literals in eventsSourceTopics above) documents that
// this is not an arbitrary topic list — docs/api.md's WebSocket section is
// the single source of truth for all four spellings.
const (
	topicChangesets = "changesets"
	topicDrift      = "drift"
	topicFindings   = "findings"
)

const (
	// wsSendQueueSize bounds each connection's outbound buffer. A client
	// slower than this many pending broadcasts gets its message dropped
	// (see wsConn.enqueue) rather than blocking the broadcaster or growing
	// memory unboundedly — the T-106 acceptance criterion that "a slow/non-
	// reading client among [500] must not stall the others".
	wsSendQueueSize = 32
	wsWriteTimeout  = 10 * time.Second
	// wsReadLimit bounds an inbound frame (a {"subscribe":[...]} message);
	// far larger than any real subscription list, just a sanity ceiling.
	wsReadLimit = 64 * 1024
)

// ConnObserver is notified about the lifecycle of live /api/ws
// connections: when one is accepted (with the identity the middleware chain
// already resolved), when it changes its subscription set, and when it goes
// away.
//
// It exists for T-2805's presence and advisory locks, which need exactly two
// facts this hub is the only place that knows: which connections currently
// declare interest in a `presence:<scope>` topic, and — the one that matters
// — the instant a connection DIES. A lock released only by an explicit
// endpoint call is a lock a closed laptop holds forever, so the disconnect
// has to be observable rather than polled for.
//
// Implementations must not block: every method is called from the
// connection's own goroutine, on the accept and teardown paths.
type ConnObserver interface {
	ConnOpened(connID, username, sessionID string)
	ConnTopics(connID string, topics []string)
	ConnClosed(connID string)
}

// Hub is the /api/ws server: it tracks connected clients' subscription
// topics and fans out topology.delta events from the collector's
// inventory.Delta callback (docs/api.md's WebSocket section).
type Hub struct {
	log       *slog.Logger
	conns     map[*wsConn]struct{}
	eventSink func([]byte)
	observer  ConnObserver
	nextConn  atomic.Uint64
	mu        sync.Mutex
}

// SetConnObserver registers o to receive connection lifecycle callbacks. A
// nil observer (the default) means nothing observes them, which is exactly
// how this hub behaved before T-2805 — the same optional-hook convention
// SetEventSink above already follows.
func (h *Hub) SetConnObserver(o ConnObserver) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observer = o
}

func (h *Hub) connObserver() ConnObserver {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.observer
}

// NewHub constructs an empty Hub. log defaults to slog.Default() if nil.
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{log: log, conns: map[*wsConn]struct{}{}}
}

// SetEventSink registers fn to be invoked with the exact encoded payload of
// every event that lands on topicEvents — whether fanned in from
// eventsSourceTopics (changeset.status/drift.changed/findings.changed) or
// broadcast directly to "events" (audit.appended) — regardless of whether
// any WS client is currently subscribed. cmd/vnproxd wires this to
// internal/automation's webhook dispatcher (T-1104): webhook targets
// receive the identical envelope an "events"-subscribed WS client would,
// from this single fan-in point, so there is exactly one place that
// decides "what counts as an automation event" for both delivery
// mechanisms. fn must not block (called from whichever goroutine invoked
// Broadcast) and a nil fn (the default) simply means no webhook dispatcher
// is wired, matching every other optional-hook convention in this
// codebase.
func (h *Hub) SetEventSink(fn func([]byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.eventSink = fn
}

// wsConn is one accepted WebSocket client: its own bounded outbound queue
// and current subscription set.
type wsConn struct {
	ws       *websocket.Conn
	send     chan []byte
	topics   map[string]bool
	tokenID  string
	observer ConnObserver
	// id is this connection's hub-unique identifier, handed to the
	// ConnObserver so presence can attribute (and, on close, retract) a
	// subscription set without holding a pointer to the connection itself.
	id        string
	dropped   int64
	topicsMu  sync.Mutex
	canEvents bool
}

// setTopics replaces c's subscription set with topics, per docs/api.md's
// "each subscribe message replaces the connection's entire topic set"
// contract. topicEvents is silently dropped from the requested set unless
// c.canEvents — a connection whose session/token lacks the "automation"
// scope simply never receives anything on "events", the same fail-closed
// treatment a malformed subscribe message already gets (this package's
// readLoop doc comment), rather than a distinct rejection the client would
// have to parse out of an otherwise ack-less protocol.
func (c *wsConn) setTopics(topics []string) {
	c.topicsMu.Lock()
	accepted := make([]string, 0, len(topics))
	c.topics = make(map[string]bool, len(topics))
	for _, t := range topics {
		if t == topicEvents && !c.canEvents {
			continue
		}
		c.topics[t] = true
		accepted = append(accepted, t)
	}
	c.topicsMu.Unlock()

	// The observer is told the ACCEPTED set, not the requested one, so a
	// topic this connection was refused (the automation-gated "events"
	// topic) can never register presence either.
	if c.observer != nil {
		c.observer.ConnTopics(c.id, accepted)
	}
}

func (c *wsConn) subscribed(topic string) bool {
	c.topicsMu.Lock()
	defer c.topicsMu.Unlock()
	return c.topics[topic]
}

// ServeWS upgrades r to a WebSocket and serves it until the client
// disconnects or the request context is cancelled (server shutdown). It is
// meant to run behind session + capability gating (internal/api mounts it
// with the same read capability as GET /topology) — callers must ensure
// authentication happened before this is invoked, since the WS handshake
// itself carries no further auth step.
//
// T-1104: it additionally reads the resolved auth.Identity already
// attached to r's context by that middleware chain (whichever of the
// cookie-session/bearer-token paths authenticated the request) to decide,
// once, at accept time, whether this connection may ever subscribe to
// topicEvents (auth.CapAutomation) and — for a bearer-token connection —
// which token's id it should be force-closed alongside on revocation
// (CloseByTokenID). A request with no Identity in context (any pre-T-1104
// caller, or a test that hits ServeWS directly without the auth
// middleware) simply gets canEvents=false/tokenID="", the same fail-closed
// default every other optional-context lookup in this codebase falls back
// to.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.log.Warn("topology: ws accept failed", "error", err)
		return
	}

	var tokenID, username, sessionID string
	var canEvents bool
	if id, ok := auth.IdentityFromContext(r.Context()); ok {
		tokenID = id.TokenID
		username = id.Username
		sessionID = id.SessionID
		canEvents = id.HasCap("", auth.CapAutomation)
	}

	observer := h.connObserver()
	connID := strconv.FormatUint(h.nextConn.Add(1), 10)
	conn := &wsConn{
		ws: ws, send: make(chan []byte, wsSendQueueSize), topics: map[string]bool{},
		tokenID: tokenID, canEvents: canEvents, id: connID, observer: observer,
	}
	h.add(conn)
	defer h.remove(conn)
	if observer != nil {
		observer.ConnOpened(connID, username, sessionID)
		// T-2805 AC3: this runs on EVERY exit from ServeWS — a clean close,
		// a read error, a killed socket, a cancelled server context — which
		// is the whole point. The disconnect is the release trigger.
		defer observer.ConnClosed(connID)
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer func() { _ = ws.CloseNow() }()

	ws.SetReadLimit(wsReadLimit)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn.writeLoop(ctx, h.log)
	}()

	conn.readLoop(ctx, h.log) // blocks until the client disconnects or errors
	cancel()
	wg.Wait()
}

// subscribeRequest is the documented client->server message: {"subscribe":
// ["topology", ...]} (docs/api.md's WebSocket section).
type subscribeRequest struct {
	Subscribe []string `json:"subscribe"`
}

func (c *wsConn) readLoop(ctx context.Context, log *slog.Logger) {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return // client closed, errored, or ctx was cancelled
		}
		var req subscribeRequest
		if err := json.Unmarshal(data, &req); err != nil {
			log.Debug("topology: ignoring malformed ws message", "error", err)
			continue
		}
		c.setTopics(req.Subscribe)
	}
}

func (c *wsConn) writeLoop(ctx context.Context, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				log.Debug("topology: ws write failed, closing connection", "error", err)
				return
			}
		}
	}
}

// enqueue non-blockingly hands msg to c's outbound queue, dropping (and
// counting/logging) it if the client is too slow to keep up rather than
// ever blocking the broadcaster.
func (c *wsConn) enqueue(msg []byte, log *slog.Logger) {
	select {
	case c.send <- msg:
	default:
		c.dropped++
		log.Warn("topology: dropping ws event for slow client", "queue_size", wsSendQueueSize, "total_dropped", c.dropped)
	}
}

func (h *Hub) add(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[c] = struct{}{}
}

func (h *Hub) remove(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
}

// ConnCount reports the number of currently connected clients (used by
// tests and, potentially, /api/v1/health).
func (h *Hub) ConnCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// deltaEvent is the wire shape of a topology.delta push: docs/api.md's
// `{added, updated, removed: [Ref]}`, plus the documented flat "event" name
// field web/src/api/ws.ts's client already assumes every server message
// carries (see that file's top-of-file comment).
type deltaEvent struct {
	Event   string   `json:"event"`
	Added   []string `json:"added"`
	Updated []string `json:"updated"`
	Removed []string `json:"removed"`
}

// BroadcastDelta translates an inventory.Delta into a topology.delta event
// and fans it out to every connection subscribed to the "topology" topic.
// It is the function cmd/vnproxd wires in as collect.Config.OnDelta: it
// must not block (collect's contract) and never does — enqueue is always
// non-blocking, and this method itself never touches the network.
func (h *Hub) BroadcastDelta(d inventory.Delta) {
	if d.Empty() {
		return
	}
	evt := deltaEvent{
		Event:   "topology.delta",
		Added:   refStrings(d.Added),
		Updated: refStrings(d.Updated),
		Removed: refStrings(d.Removed),
	}
	data, err := json.Marshal(evt)
	if err != nil {
		h.log.Error("topology: marshaling topology.delta event", "error", err)
		return
	}
	h.Broadcast(topicTopology, data)
}

// Broadcast fans out a pre-encoded JSON event to every connection
// subscribed to topic. It is BroadcastDelta's generic counterpart, added
// so other packages that need to push events over the same shared
// /api/ws connection (docs/api.md's WebSocket section documents one
// connection multiplexing "topology", "changesets", "metrics:<ref>", and
// "tasks" topics alike) can reuse this hub's connection management instead
// of standing up a second WS endpoint — see internal/change.Service's
// Broadcaster seam and this package's Service.Broadcast passthrough. Like
// BroadcastDelta, it never blocks: enqueue always drops rather than
// waiting on a slow client.
//
// T-1104: when topic is one of eventsSourceTopics (or topicEvents itself,
// e.g. a direct audit.appended push), the identical payload is additionally
// delivered to every "events"-subscribed connection that isn't already
// getting it via its own topic subscription (so a connection subscribed to
// both "changesets" and "events" is never sent the same message twice),
// and handed to h.eventSink once per call if one is registered — the
// single fan-in point backing both the WS "events" topic and T-1104's
// webhook dispatcher.
func (h *Hub) Broadcast(topic string, payload []byte) {
	h.mu.Lock()
	targets := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		targets = append(targets, c)
	}
	sink := h.eventSink
	h.mu.Unlock()

	feedsEvents := topic == topicEvents || eventsSourceTopics[topic]

	for _, c := range targets {
		sentDirect := c.subscribed(topic)
		if sentDirect {
			c.enqueue(payload, h.log)
		}
		if feedsEvents && topic != topicEvents && !sentDirect && c.subscribed(topicEvents) {
			c.enqueue(payload, h.log)
		}
	}

	if feedsEvents && sink != nil {
		sink(payload)
	}
}

// CloseByTokenID force-closes every live WS connection whose tokenID
// matches id (T-1104 acceptance criterion 5: "revoking a token ... force-
// closes its open WS subscriptions within one server tick") and returns
// how many it closed. internal/api's DELETE /tokens/{id} handler calls
// this synchronously right after persisting the revocation, so the close
// happens within the same request tick that revoked the token — no
// separate poller is needed.
//
// It uses CloseNow, not Close: Close performs a graceful close handshake
// that blocks the caller for up to 5s waiting for the peer to ack (this
// package's doc comment on the method, nhooyr.io/websocket's own
// documented behavior) — acceptable for a client-initiated disconnect, but
// exactly wrong here, since a revoked/compromised token's connection is
// the one case where the peer might never ack at all, and this method must
// not make DELETE /tokens/{id} itself hang on that. CloseNow tears down
// the underlying connection immediately (the same call ServeWS's own
// deferred cleanup already uses), unblocking the connection's readLoop
// (and its own deferred h.remove) right away. Safe to call concurrently
// with the connection's own read/write loops, per nhooyr.io/websocket's
// documented concurrency guarantees.
func (h *Hub) CloseByTokenID(id string) int {
	if id == "" {
		return 0
	}
	h.mu.Lock()
	var targets []*wsConn
	for c := range h.conns {
		if c.tokenID == id {
			targets = append(targets, c)
		}
	}
	h.mu.Unlock()

	for _, c := range targets {
		_ = c.ws.CloseNow()
	}
	return len(targets)
}

func refStrings(refs []inventory.Ref) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.String()
	}
	return out
}
