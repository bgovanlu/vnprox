package topology

import (
	"log/slog"
	"net/http"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Service wires the projection, search, detail, and WS hub in this package
// against one live *inventory.Graph. It is the concrete type internal/api's
// TopologyService interface (see cmd/vnproxd's wiring) is implemented
// against.
type Service struct {
	graph *inventory.Graph
	hub   *Hub
}

// NewService builds a Service over graph. log is passed through to the WS
// hub for slow-client-drop / accept-failure logging.
func NewService(graph *inventory.Graph, log *slog.Logger) *Service {
	return &Service{graph: graph, hub: NewHub(log)}
}

// Topology projects the current graph snapshot per f.
func (s *Service) Topology(f Filter) Topology {
	return Project(s.graph.Snapshot(), f)
}

// InventoryDetail returns ref's full detail from the current snapshot.
func (s *Service) InventoryDetail(ref inventory.Ref) (EntityDetail, bool) {
	return Detail(s.graph.Snapshot(), ref)
}

// Search ranks q against the current snapshot.
func (s *Service) Search(q string) []SearchResult {
	return Search(s.graph.Snapshot(), q)
}

// ServeWS upgrades and serves one /api/ws client.
func (s *Service) ServeWS(w http.ResponseWriter, r *http.Request) {
	s.hub.ServeWS(w, r)
}

// OnDelta is the callback to hand to collect.Config.OnDelta: it fans the
// poll's delta out to subscribed WS clients. Never blocks (see Hub.
// BroadcastDelta / wsConn.enqueue), satisfying collect's "must not block"
// requirement on this callback.
func (s *Service) OnDelta(d inventory.Delta) {
	s.hub.BroadcastDelta(d)
}

// ConnCount reports the current WS client count (tests, and potentially a
// future health/metrics surface).
func (s *Service) ConnCount() int {
	return s.hub.ConnCount()
}

// Broadcast fans out a pre-encoded JSON event to every /api/ws client
// subscribed to topic, over this Service's shared Hub. It satisfies
// internal/change.Broadcaster (and any future package that needs to push
// its own event type over the same connection): docs/api.md's WebSocket
// section documents one shared /api/ws connection multiplexing multiple
// topics ("topology", "changesets", "metrics:<ref>", "tasks"), so
// non-topology packages reuse this Service/Hub rather than each standing
// up their own WS endpoint.
func (s *Service) Broadcast(topic string, payload []byte) {
	s.hub.Broadcast(topic, payload)
}
