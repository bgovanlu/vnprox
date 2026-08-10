// topodiff.go serves T-2704's `GET /topology/diff` — "what is different about
// this cluster compared to Tuesday", answered from the snapshot series T-2401
// records on a schedule, with each difference either named to the changeset
// that made it or explicitly marked unattributed.
//
// Read-only: this handler resolves two points, compares them, and writes JSON.
// It cannot stage or apply anything, because TopologyDiffService — the seam it
// holds — has exactly one method and that method is a read.

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TopologyDiffService is the subset of *change.Service this route needs.
//
// One method, and it reads. That is structural, not stylistic: a handler that
// cannot reach an apply/stage method cannot grow one by accident, which is the
// same "the interface has no apply" shape the MCP surface uses for the same
// reason.
type TopologyDiffService interface {
	TopologyDiff(ctx context.Context, from, to string) (*change.TopologyDiff, error)
}

// mountTopologyDiffRoutes registers `GET /topology/diff`, gated on netRead
// like every other topology read. It is deliberately NOT gated on `audit`
// (the way GET /inventory/history is): this route exposes captured network
// configuration, which netRead already grants through GET /topology,
// GET /nodes/{node}/interfaces/raw and GET /snapshots/diff — but it does name
// changeset ids and authors in its attribution, so it is never gated more
// loosely than the changeset read routes, which are also netRead.
func mountTopologyDiffRoutes(r chi.Router, svc TopologyDiffService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/topology/diff", handleTopologyDiff(svc))
	})
}

func handleTopologyDiff(svc TopologyDiffService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from, to := q.Get("from"), q.Get("to")
		if from == "" || to == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				"from and to are required (a snapshot id, a unix or RFC3339 timestamp, or to=now)")
			return
		}
		diff, err := svc.TopologyDiff(r.Context(), from, to)
		if err != nil {
			writeTopologyDiffError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, diff)
	}
}

// writeTopologyDiffError maps the diff's refusals to docs/api.md's error
// envelope.
//
// The one that matters is `no_snapshot_in_range` (422, a new additive stable
// code): a range with no snapshot behind one of its ends is NOT an empty diff.
// An empty diff reads as "nothing changed about this cluster", and returning
// one here would be a false statement — so the refusal carries the nearest
// snapshots that do exist in `details.nearest`, which is both the explanation
// and the fix (ask for a range those cover).
func writeTopologyDiffError(w http.ResponseWriter, err error) {
	var missing *change.ErrNoSnapshotForPoint
	if errors.As(err, &missing) {
		nearest := make([]map[string]any, 0, len(missing.Nearest))
		for _, n := range missing.Nearest {
			nearest = append(nearest, map[string]any{
				"snapshotId": n.SnapshotID,
				"kind":       n.Kind,
				"takenAt":    n.TakenAt,
			})
		}
		writeJSONErrorDetails(w, http.StatusUnprocessableEntity, "no_snapshot_in_range", missing.Error(),
			map[string]any{"side": missing.Side, "requested": missing.Requested, "nearest": nearest})
		return
	}

	var inverted *change.ErrDiffRangeInverted
	if errors.As(err, &inverted) {
		writeJSONErrorDetails(w, http.StatusBadRequest, "validation_failed", inverted.Error(),
			map[string]any{"fromAt": inverted.FromAt, "toAt": inverted.ToAt})
		return
	}

	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	var notConfigured *change.ErrApplyNotConfigured
	if errors.As(err, &notConfigured) {
		// Same code writeApplyError uses for the same condition — a node
		// with no snapshot store cannot answer this question either.
		writeJSONError(w, http.StatusServiceUnavailable, "apply_unavailable", "the snapshot store is not available on this node")
		return
	}

	writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not compute the topology diff")
}
