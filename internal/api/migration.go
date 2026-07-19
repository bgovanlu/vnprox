// migration.go implements T-1507's Migration planner route
// (docs/api.md's new Migration planner section):
//
//   - POST /migration/preflight {guest: Ref, targetNode} — a purely
//     advisory, read-only pre-flight bandwidth-headroom assessment for a
//     live migration/evacuation an operator is about to trigger *in PVE
//     itself*. netRead-gated — a read (this route never stages, applies,
//     or otherwise touches a changeset, and it never calls a PVE
//     migration-start/evacuate endpoint; see internal/migration's own
//     doc.go "Advisory only" section and its regression test).

package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/migration"
)

// maxMigrationPreflightBodyBytes bounds POST /migration/preflight's body —
// a {guest, targetNode} request is tiny; this is generous headroom, not a
// realistic limit, mirroring maxSimulateBodyBytes' identical reasoning for
// a comparably small request shape.
const maxMigrationPreflightBodyBytes = 1 << 12 // 4 KiB

// mountMigrationRoutes registers POST /migration/preflight, netRead-gated
// like every other read-only route in this package (a live-network-state
// read, not a mutation — see this file's own doc comment). Nil planner/
// auth skips mounting, the same degraded-mode convention every other
// mountXRoutes function in this package follows.
func mountMigrationRoutes(r chi.Router, planner *migration.Planner, auth AuthService) {
	if planner == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Post("/migration/preflight", handleMigrationPreflight(planner))
	})
}

// migrationPreflightRequest is POST /migration/preflight's request body —
// docs/api.md's Migration planner section.
type migrationPreflightRequest struct {
	Guest      string `json:"guest"`
	TargetNode string `json:"targetNode"`
}

func handleMigrationPreflight(planner *migration.Planner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req migrationPreflightRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMigrationPreflightBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed migration preflight body: "+err.Error())
			return
		}
		targetNode := strings.TrimSpace(req.TargetNode)
		if targetNode == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "targetNode is required")
			return
		}
		guest, err := inventory.ParseRef(req.Guest)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "guest: "+err.Error())
			return
		}
		if guest.Kind != inventory.KindGuest {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "guest must be a guest ref")
			return
		}

		assessment := planner.Plan(r.Context(), guest, targetNode)
		writeJSON(w, http.StatusOK, assessment)
	}
}
