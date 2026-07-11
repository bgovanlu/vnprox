package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/docexport"
)

// DocExportService is the subset of *docexport.Service the router needs:
// one call that assembles the current config-documentation Data (docs/
// features/blueprints.md §4). Declared as an interface (the same seam
// pattern as every other *Service in this package) so this file's
// dependency on the concrete service stays a small, explicit seam.
type DocExportService interface {
	Build(ctx context.Context) docexport.Data
}

// mountDocExportRoutes registers `GET /export/doc?format=md|html` (additive
// to docs/api.md's original contract; documented there in this same
// change per docs/development.md's definition-of-done #4). netRead-gated
// like every other read route in this package — an export is a read-only,
// derived artifact (docs/architecture.md's "an export is a derived
// read-only artifact, not something to persist" framing), never written
// to the SQLite store.
//
// svc is nil-safe (route not mounted), matching every other mountXRoutes
// function in this package.
func mountDocExportRoutes(r chi.Router, svc DocExportService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/export/doc", handleDocExport(svc))
	})
}

func handleDocExport(svc DocExportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		if format != "md" && format != "html" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `format must be "md" or "html"`)
			return
		}

		data := svc.Build(r.Context())
		stamp := time.Unix(data.GeneratedAt, 0).UTC().Format("20060102-150405")

		switch format {
		case "md":
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vnprox-network-%s.md"`, stamp))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(docexport.Markdown(data)))
		case "html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vnprox-network-%s.html"`, stamp))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(docexport.HTML(data)))
		}
	}
}
