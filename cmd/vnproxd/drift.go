package main

import (
	"encoding/json"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// topicDrift is the WS subscribe topic name for T-305's `drift.changed`
// event (docs/api.md's WebSocket section: one shared /api/ws connection
// multiplexes topics by name; a client subscribes to "drift" to receive
// these pushes, the same way it subscribes to "topology" or "changesets").
const topicDrift = "drift"

// driftChangedEvent is docs/api.md's documented `drift.changed {count}`
// WS event payload, plus the flat "event" name field every server->client
// message on this connection carries (see topology/hub.go's deltaEvent
// doc comment).
type driftChangedEvent struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

// driftBroadcaster is the subset of *topology.Service T-305 needs to push
// its own event type over the shared WS hub — the same seam pattern
// internal/change.Broadcaster already uses for `changeset.status`.
type driftBroadcaster interface {
	Broadcast(topic string, payload []byte)
}

// setupDrift builds T-305's *drift.Service over graph, wired so a changed
// finding set (drift.Service.RunLoop's own change-detection — see that
// method's doc comment) broadcasts docs/api.md's `drift.changed {count}`
// event over the shared WS hub.
func setupDrift(graph *inventory.Graph, ws driftBroadcaster, logger *slog.Logger) *drift.Service {
	return drift.New(drift.Config{
		Graph:  graph,
		Logger: logger,
		OnChange: func(count int) {
			data, err := json.Marshal(driftChangedEvent{Event: "drift.changed", Count: count})
			if err != nil {
				logger.Error("drift: marshaling drift.changed event", "error", err)
				return
			}
			ws.Broadcast(topicDrift, data)
		},
	})
}
