package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// FindingsService is the subset of *findings.Engine the router needs:
// T-602's unified findings stream (docs/features/monitoring.md §5) and the
// fixing-changeset op lookup by finding id — the same two-method shape
// DriftService already established, generalized across every producer.
type FindingsService interface {
	Findings() []findings.Finding
	FixOps(id string) (ops []change.Op, title string, ok bool)
}

// mountFindingsRoutes registers `GET /findings` and `POST /findings/{id}/fix`
// (T-602; not in the original docs/api.md contract — documented there in
// this same change per docs/development.md's definition-of-done #4, the
// same pattern T-305's /drift additions used). This is additive to, not a
// replacement for, the existing `GET /drift`/`POST /drift/{id}/fix` routes
// (mountDriftRoutes) — those keep working entirely unchanged for any
// existing caller that only cares about drift-family findings; /findings is
// the new unified superset spanning drift+lldp+ipam+health.
func mountFindingsRoutes(r chi.Router, svc FindingsService, changesets ChangesetService, auth AuthService, scopeMW func(http.Handler) http.Handler) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		if scopeMW != nil {
			r.Use(scopeMW)
		}
		r.Get("/findings", handleFindings(svc))
	})

	if changesets == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/findings/{id}/fix", handleFindingsFix(svc, changesets, lookup))
	})
}

// handleFindings serves `GET /findings?source=&severity=&node=` — AC2's
// "filter by source/severity/node works ... uniformly": every filter is
// applied generically over the already-unified Finding shape (no
// producer-specific filter logic needed, since every producer's findings
// already arrive through the same fields). Every filter param is optional
// and additive (AND, not OR): source=drift&severity=error returns only
// error-severity drift findings. An unrecognized value for any filter
// simply matches nothing for that filter (never a 400) — this is a
// read-only convenience filter, not a validated query language.
func handleFindings(svc FindingsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		source := strings.TrimSpace(q.Get("source"))
		severity := strings.TrimSpace(q.Get("severity"))
		node := strings.TrimSpace(q.Get("node"))

		all := svc.Findings()
		items := make([]findings.Finding, 0, len(all))
		for _, f := range all {
			if source != "" && string(f.Source) != source {
				continue
			}
			if severity != "" && f.Severity != severity {
				continue
			}
			if node != "" && !findingHasNode(f.Nodes, node) {
				continue
			}
			items = append(items, f)
		}
		// T-1703: a tenant sees only findings that reference one of its own
		// visible resources (never cluster-wide/node-wide findings).
		if scope, scoped := scopeFromContext(r.Context()); scoped {
			items = filterFindingsForScope(items, scope)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func findingHasNode(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func handleFindingsFix(svc FindingsService, changesets ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		ops, title, ok := svc.FixOps(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found", "no fixable finding with that id")
			return
		}
		c, err := changesets.Create(r.Context(), username, title, ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create fixing changeset")
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(c))
	}
}
