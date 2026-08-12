package topology

import (
	"log/slog"
	"net/http"
	"time"

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

// LLDPNeighbors returns every LldpNeighbor entity in the current snapshot's
// canonical field shape (docs/api.md's `GET /lldp`: "all LLDP neighbors
// cluster-wide (fanned out to peers)"). Single-node/no-peer clusters are
// covered today since the collector already polls the local node directly;
// cluster fan-out to peer nodes' own LLDP data is T-303's job (this
// package's contract does not change once that lands — T-303 extends what
// feeds the graph, not how this method reads it).
func (s *Service) LLDPNeighbors() []*inventory.LldpNeighbor {
	var out []*inventory.LldpNeighbor
	for _, e := range s.graph.Snapshot().All() {
		if n, ok := e.(*inventory.LldpNeighbor); ok {
			out = append(out, n)
		}
	}
	return out
}

// VlanFindings runs the VLAN cross-check (spec §2) over the current
// snapshot.
func (s *Service) VlanFindings() []VlanFinding {
	return VlanFindings(s.graph.Snapshot())
}

// Ports builds the flat ports table (spec §2) over the current snapshot,
// evaluated at the real current time.
func (s *Service) Ports() []PortRow {
	return Ports(s.graph.Snapshot(), time.Now())
}

// FDB returns every bridge forwarding-database entry cluster-wide,
// ownership-labeled, over the current snapshot (T-306's MAC/FDB browser,
// docs/features/lldp-discovery.md §4).
func (s *Service) FDB() []FDBRow {
	return FDB(s.graph.Snapshot())
}

// FDBSearch ranks every FDB entry cluster-wide against q over the current
// snapshot.
func (s *Service) FDBSearch(q string) []FDBRow {
	return FDBSearch(s.graph.Snapshot(), q)
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

// CloseByTokenID force-closes every live WS connection authenticated by
// the given api_tokens.id, over this Service's shared Hub (T-1104's
// revoke-forces-WS-disconnect acceptance criterion). See Hub.CloseByTokenID's
// doc comment for the CloseNow-not-Close reasoning.
func (s *Service) CloseByTokenID(id string) int {
	return s.hub.CloseByTokenID(id)
}

// SetEventSink registers fn as the Hub's T-1104 automation-event fan-in
// hook (see Hub.SetEventSink's doc comment) — cmd/vnproxd wires this to
// internal/automation's webhook Dispatcher.Publish.
func (s *Service) SetEventSink(fn func([]byte)) {
	s.hub.SetEventSink(fn)
}

// SetConnObserver registers o as the Hub's T-2805 connection-lifecycle
// observer (see Hub.SetConnObserver) — cmd/vnproxd wires this to
// internal/presence.Service, which is how a dropped connection releases the
// advisory locks its session held.
func (s *Service) SetConnObserver(o ConnObserver) {
	s.hub.SetConnObserver(o)
}
