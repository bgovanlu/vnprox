package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// EntityHistoryService is the read seam behind `GET /inventory/history`
// (T-2403) — *change.Service satisfies it.
type EntityHistoryService interface {
	EntityHistory(ctx context.Context, ref inventory.Ref, limit int) ([]change.EntityHistoryEntry, bool, error)
}

// mountEntityHistoryRoutes registers T-2403's entity change history.
//
// The route is `/inventory/history?ref=…`, NOT `/inventory/{ref}/history`.
// Two reasons, both structural rather than stylistic:
//
//   - `/inventory/*` is a wildcard that already owns everything below
//     `/inventory/`. A static segment beats it in chi (static > param >
//     wildcard), which is the same mechanism `/inventory/search` relies on; a
//     `{ref}/history` shape would be swallowed.
//   - Refs legitimately contain "/" — an SDN vnet path (`sdn-vnet::zone1/vnet1`)
//     or a subnet CIDR. Carrying one in a path segment means every caller must
//     percent-encode it correctly, and exactly one caller failing to do so is
//     how T-1304's guest-interior routes returned 400 to every browser request
//     for months. A query parameter is decoded by net/url before we see it.
//
// It requires the same capability as `GET /audit`: this is the audit trail,
// re-sliced by entity, and re-slicing data must never widen who can read it.
func mountEntityHistoryRoutes(r chi.Router, svc EntityHistoryService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capAudit))
		r.Get("/inventory/history", handleEntityHistory(svc))
	})
}

// entityHistoryResponse is the route's body. `truncated` is deliberately part
// of the contract: the changeset scan is bounded, and a silently short history
// is indistinguishable from a genuinely short one. An operator reading
// "nothing has touched this bridge" needs that to be true.
type entityHistoryResponse struct {
	Items     []change.EntityHistoryEntry `json:"items"`
	Truncated bool                        `json:"truncated"`
}

func handleEntityHistory(svc EntityHistoryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(r.URL.Query().Get("ref"))
		if raw == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref is required")
			return
		}
		// net/url has already decoded the query value once. A caller that
		// double-encoded (encodeURIComponent over an already-encoded ref) is
		// tolerated by unescaping again when that yields a parseable ref —
		// but never at the cost of breaking a plain one, so the plain form is
		// tried first.
		ref, err := inventory.ParseRef(raw)
		if err != nil {
			if unescaped, uerr := url.PathUnescape(raw); uerr == nil {
				ref, err = inventory.ParseRef(unescaped)
			}
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref is not a valid kind:node:id reference")
			return
		}

		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			n, convErr := strconv.Atoi(v)
			if convErr != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "limit must be a positive integer")
				return
			}
			limit = n
		}

		items, truncated, err := svc.EntityHistory(r.Context(), ref, limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read entity history")
			return
		}
		writeJSON(w, http.StatusOK, entityHistoryResponse{Items: items, Truncated: truncated})
	}
}
