package topology

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Subscription topics this hub understands producing events for. Clients
// may subscribe to other docs/api.md topics ("changesets", "metrics:<ref>",
// "tasks") without error — the hub just never has anything to send them,
// since those events are later tasks' responsibility.
const topicTopology = "topology"

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

// Hub is the /api/ws server: it tracks connected clients' subscription
// topics and fans out topology.delta events from the collector's
// inventory.Delta callback (docs/api.md's WebSocket section).
type Hub struct {
	log   *slog.Logger
	conns map[*wsConn]struct{}
	mu    sync.Mutex
}

// NewHub constructs an empty Hub. log defaults to slog.Default() if nil.
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{log: log, conns: map[*wsConn]struct{}{}}
}

// wsConn is one accepted WebSocket client: its own bounded outbound queue
// and current subscription set.
type wsConn struct {
	ws       *websocket.Conn
	send     chan []byte
	topics   map[string]bool
	dropped  int64
	topicsMu sync.Mutex
}

func (c *wsConn) setTopics(topics []string) {
	c.topicsMu.Lock()
	defer c.topicsMu.Unlock()
	c.topics = make(map[string]bool, len(topics))
	for _, t := range topics {
		c.topics[t] = true
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
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.log.Warn("topology: ws accept failed", "error", err)
		return
	}

	conn := &wsConn{ws: ws, send: make(chan []byte, wsSendQueueSize), topics: map[string]bool{}}
	h.add(conn)
	defer h.remove(conn)

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

	h.mu.Lock()
	targets := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		targets = append(targets, c)
	}
	h.mu.Unlock()

	for _, c := range targets {
		if c.subscribed(topicTopology) {
			c.enqueue(data, h.log)
		}
	}
}

func refStrings(refs []inventory.Ref) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.String()
	}
	return out
}
