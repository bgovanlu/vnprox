// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
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
// event over the shared WS hub. pins is T-1102's spec_drift seam (nil-safe:
// the spec_drift check family simply never fires without one, e.g. a caller
// that hasn't wired store.NewPinnedSpecRepo yet).
func setupDrift(graph *inventory.Graph, ws driftBroadcaster, pins drift.PinProvider, logger *slog.Logger) *drift.Service {
	return drift.New(drift.Config{
		Graph:  graph,
		Pins:   pins,
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

// specPinAdapter adapts *store.PinnedSpecRepo into drift.PinProvider's
// context-free Pin() (content string, ok bool) — the drift Service's
// RunLoop cycles on its own goroutine with no natural request context to
// thread through, the same shape cmd/vnproxd/findings.go's mgmtStatusAdapter
// already establishes for findings.MgmtProvider (see that adapter's doc
// comment for the fuller rationale).
type specPinAdapter struct {
	repo   *store.PinnedSpecRepo
	logger *slog.Logger
}

func (a specPinAdapter) Pin() (string, bool) {
	ps, err := a.repo.Get(context.Background())
	if errors.Is(err, store.ErrNotFound) {
		return "", false
	}
	if err != nil {
		a.logger.Error("drift: reading pinned spec", "error", err)
		return "", false
	}
	return ps.Content, true
}
