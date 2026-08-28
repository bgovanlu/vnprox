// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// QosShapeView is one qos.shape row as GET /qos/shapes renders it
// (docs/api.md's QoS section) — the same field set QosShapeCreateParams
// carries, plus the shape's own id/node identity.
type QosShapeView struct {
	MatchVlan *int   `json:"matchVlan,omitempty"`
	CeilMbit  *int   `json:"ceilMbit,omitempty"`
	Priority  *int   `json:"priority,omitempty"`
	ID        string `json:"id"`
	Node      string `json:"node"`
	Bridge    string `json:"bridge"`
	MatchCIDR string `json:"matchCidr,omitempty"`
	RateMbit  int    `json:"rateMbit"`
}

// QosShapeListService is the read-only seam GET /qos/shapes needs: every
// currently-stored shape, for the map's shaping-active badge to draw from
// visually and for an operator to inspect what's applied. Mutating a shape
// is exclusively a qos.shape.* changeset op (CLAUDE.md's change-engine
// invariant) — this file has no write route.
type QosShapeListService interface {
	ListShapes(ctx context.Context) ([]QosShapeView, error)
}

// mountQosRoutes registers docs/api.md's `GET /qos/shapes` — netRead-gated
// like every other read-only inventory-adjacent route (topology, sdn, ipam).
// svc nil-safe: the route simply isn't mounted (a daemon with no QoS
// gateway wired).
func mountQosRoutes(r chi.Router, svc QosShapeListService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/qos/shapes", handleListQosShapes(svc))
	})
}

func handleListQosShapes(svc QosShapeListService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shapes, err := svc.ListShapes(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "listing QoS shapes")
			return
		}
		if shapes == nil {
			shapes = []QosShapeView{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"shapes": shapes})
	}
}
