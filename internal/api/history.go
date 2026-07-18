package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// HistoryAuditSource is the subset of *store.AuditRepo GET /history/events
// needs (T-1007): the changeset-lifecycle-filtered, time-ranged query. The
// concrete type already satisfies this signature, so cmd/vnproxd wires it
// in directly (the same "small interface, real type satisfies it for free"
// shape AuditService establishes for GET /audit).
type HistoryAuditSource interface {
	ListActionsInRange(ctx context.Context, actions []string, from, to int64) ([]store.AuditEntry, error)
}

// HistoryFindingEventsSource is the subset of *store.FindingEventRepo GET
// /history/events needs.
type HistoryFindingEventsSource interface {
	ListByTimeRange(ctx context.Context, from, to int64) ([]store.FindingEvent, error)
}

// historyEventResponse is one item of GET /history/events' merged timeline
// feed (docs/api.md's History section, T-1007): {at, kind, ...}. `kind:
// "changeset"` carries action/target/changesetId/result (mirroring GET
// /audit's own auditEntryResponse fields, minus username/detail — this
// route is a timeline-marker feed for HistoryTimeline.tsx, not a second
// full audit browser: the marker's own deep link is `changesetId`, which
// opens the existing changeset detail/diff view). `kind: "finding"` carries
// findingId/transition.
type historyEventResponse struct {
	Kind        string `json:"kind"`
	Action      string `json:"action,omitempty"`
	Target      string `json:"target,omitempty"`
	ChangesetID string `json:"changesetId,omitempty"`
	Result      string `json:"result,omitempty"`
	FindingID   string `json:"findingId,omitempty"`
	Transition  string `json:"transition,omitempty"`
	At          int64  `json:"at"`
}

// historyEventsResponse is GET /history/events' response envelope.
type historyEventsResponse struct {
	Items []historyEventResponse `json:"items"`
}

// mountHistoryRoutes registers docs/api.md's `GET /history/events`
// (T-1007), gated on the same `audit` capability GET /audit uses — this
// route's changeset half re-exposes real audit_log rows, so it is never
// gated more loosely than the route that already serves that data.
// Either dependency alone is enough to mount the route (a nil audit source
// simply contributes no changeset markers; a nil finding-events source
// contributes no finding markers — matching every other optional-producer
// convention in this package), but both nil skips mounting it entirely,
// same as auth being nil.
func mountHistoryRoutes(r chi.Router, audit HistoryAuditSource, findingEvents HistoryFindingEventsSource, auth AuthService) {
	if auth == nil || (audit == nil && findingEvents == nil) {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capAudit))
		r.Get("/history/events", handleHistoryEvents(audit, findingEvents))
	})
}

// handleHistoryEvents serves `GET /history/events?fromTs=&toTs=`: merges
// changeset-lifecycle audit_log rows with finding_events rows into one
// ascending-by-at timeline feed. fromTs/toTs default to "no bound on that
// side", mirroring handleMetricsHistory's identical convention.
func handleHistoryEvents(audit HistoryAuditSource, findingEvents HistoryFindingEventsSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		fromTs := parseTsOrDefault(q.Get("fromTs"), 0)
		toTs := parseTsOrDefault(q.Get("toTs"), 1<<62)

		var items []historyEventResponse

		if audit != nil {
			rows, err := audit.ListActionsInRange(r.Context(), store.ChangesetLifecycleActions, fromTs, toTs)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list changeset history events")
				return
			}
			for _, e := range rows {
				items = append(items, historyEventResponse{
					Kind:        "changeset",
					At:          e.At,
					Action:      e.Action,
					Target:      e.Target.String,
					ChangesetID: e.ChangesetID.String,
					Result:      e.Result,
				})
			}
		}

		if findingEvents != nil {
			rows, err := findingEvents.ListByTimeRange(r.Context(), fromTs, toTs)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list finding history events")
				return
			}
			for _, e := range rows {
				items = append(items, historyEventResponse{
					Kind:       "finding",
					At:         e.At,
					FindingID:  e.FindingID,
					Transition: e.Transition,
				})
			}
		}

		sort.SliceStable(items, func(i, j int) bool { return items[i].At < items[j].At })
		if items == nil {
			items = []historyEventResponse{}
		}
		writeJSON(w, http.StatusOK, historyEventsResponse{Items: items})
	}
}
