// gitsync.go implements T-2701's `GET /gitsync/status`: the last fetched
// commit, the last plan, and why the current draft changeset exists.
//
// It is read-only. There is deliberately no route here that triggers a sync,
// applies a sync draft, or changes the remote — a sync draft is an ordinary
// changeset and goes out through the ordinary /changesets surface with the
// ordinary review, and the remote is daemon configuration, not API state.
//
// Unlike most optional families in this package, the route mounts even when
// the service is absent: "gitsync is off" is a real answer an operator (and
// `vnproxctl gitsync status`) needs, and mounting unconditionally is also
// what keeps the route inside T-2405's route-completeness gate, which walks
// the daemon brought up under the checked-in dev configuration.

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

// GitSyncStatusService is the GET /gitsync/status seam — *gitsync.Service
// satisfies it via its Status method. Note what this interface does NOT
// have: no Sync, no Apply, nothing that mutates. The API surface of the git
// sync is exactly one read.
type GitSyncStatusService interface {
	Status() gitsync.Status
}

// mountGitSyncRoutes registers GET /gitsync/status behind session auth +
// netRead, the same gate every other read route uses.
func mountGitSyncRoutes(r chi.Router, svc GitSyncStatusService, auth AuthService) {
	if auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/gitsync/status", handleGitSyncStatus(svc))
	})
}

func handleGitSyncStatus(svc GitSyncStatusService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if svc == nil {
			// Not wired at all (a partially assembled router, or a build
			// without the subsystem): the honest answer is the same one a
			// configured-but-disabled daemon gives.
			writeJSON(w, http.StatusOK, gitsync.Status{Enabled: false})
			return
		}
		writeJSON(w, http.StatusOK, svc.Status())
	}
}
