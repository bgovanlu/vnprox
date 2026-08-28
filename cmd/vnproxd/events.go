// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/store"
)

// topicEvents is the WS subscribe topic name for T-1104's automation
// firehose (docs/api.md's WebSocket section) — see
// internal/topology/hub.go's topicEvents/eventsSourceTopics doc comments
// for the full fan-in contract this constant's producer (below) feeds.
const topicEvents = "events"

// auditAppendedEvent is docs/api.md's documented `audit.appended` WS event
// payload: the same field shape GET /audit's own auditEntryResponse uses
// (internal/api/audit.go), plus the flat "event" name field every
// server->client message on this connection carries.
type auditAppendedEvent struct {
	Event       string          `json:"event"`
	Username    string          `json:"username"`
	Action      string          `json:"action"`
	Target      string          `json:"target,omitempty"`
	ChangesetID string          `json:"changesetId,omitempty"`
	Result      string          `json:"result"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	ID          int64           `json:"id"`
	At          int64           `json:"at"`
}

// eventsBroadcaster is the subset of *topology.Service T-1104 needs to push
// audit.appended over the shared WS hub — the same seam pattern
// driftBroadcaster/internal/change.Broadcaster already use.
type eventsBroadcaster interface {
	Broadcast(topic string, payload []byte)
}

// wireAuditAppendedEvents registers audit's T-1104 append hook
// (store.AuditRepo.SetOnAppend) to broadcast docs/api.md's `audit.appended`
// event directly onto topicEvents over ws — the one place in this daemon
// that watches every audit_log row written, regardless of which package's
// call site appended it (login/logout, token.use, changeset lifecycle,
// snapshot restore, ...). This must be wired against the single shared
// *store.AuditRepo instance every other audit-writing call site in this
// binary also uses (see server.go's construction order) — a second,
// independent AuditRepo wrapping the same table would silently miss every
// append that went through it instead.
func wireAuditAppendedEvents(audit *store.AuditRepo, ws eventsBroadcaster, logger *slog.Logger) {
	audit.SetOnAppend(func(e store.AuditEntry) {
		evt := auditAppendedEvent{
			Event: "audit.appended", ID: e.ID, At: e.At, Username: e.Username,
			Action: e.Action, Result: e.Result,
		}
		if e.Target.Valid {
			evt.Target = e.Target.String
		}
		if e.ChangesetID.Valid {
			evt.ChangesetID = e.ChangesetID.String
		}
		if e.DetailJSON.Valid {
			evt.Detail = json.RawMessage(e.DetailJSON.String)
		}
		data, err := json.Marshal(evt)
		if err != nil {
			logger.Error("events: marshaling audit.appended event", "error", err)
			return
		}
		ws.Broadcast(topicEvents, data)
	})
}
