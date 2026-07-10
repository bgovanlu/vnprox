package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// capAudit is docs/api.md's documented "viewing vnprox's own audit log"
// capability flag (internal/auth.CapAudit's underlying string; see that
// package's doc comment: "audit reuses Sys.Audit: viewing vnprox's own
// audit log is itself an audit-level read"), spelled out as a plain string
// for the same reason capNetRead/capNetWrite are (keeping this package's
// auth dependency to the AuthService method-seam interface).
const capAudit = "audit"

// defaultAuditPageLimit and maxAuditPageLimit bound GET /audit's ?limit=
// query param, mirroring the snapshots list route's convention.
const (
	defaultAuditPageLimit = 50
	maxAuditPageLimit     = 200
)

// AuditService is the subset of *store.AuditRepo the router needs for
// docs/features/change-management.md §8's Audit UI: a single filtered,
// cursor-paginated query. *store.AuditRepo's ListPage already has exactly
// this signature, so cmd/vnproxd wires the concrete repo in directly with no
// adapter needed (the same "small interface, real type satisfies it for
// free" shape LayoutStore uses for *store.LayoutRepo).
type AuditService interface {
	ListPage(ctx context.Context, filter store.AuditFilter, cursor string, limit int) ([]store.AuditEntry, string, error)
}

// auditEntryResponse is one row of GET /audit's response, per
// docs/features/change-management.md §8: "each row expands to op summaries
// and links to the changeset and its snapshots" — changesetId is exactly
// that link; the frontend fetches the changeset (and, transitively, its
// snapshots) by id to expand a row.
type auditEntryResponse struct {
	Username    string          `json:"username"`
	Action      string          `json:"action"`
	Target      string          `json:"target,omitempty"`
	ChangesetID string          `json:"changesetId,omitempty"`
	Result      string          `json:"result"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	ID          int64           `json:"id"`
	At          int64           `json:"at"`
}

// auditListResponse is GET /audit's response envelope, matching
// snapshotListResponse's {items, nextCursor} shape.
type auditListResponse struct {
	NextCursor string               `json:"nextCursor,omitempty"`
	Items      []auditEntryResponse `json:"items"`
}

func toAuditEntryResponse(e store.AuditEntry) auditEntryResponse {
	resp := auditEntryResponse{
		ID: e.ID, At: e.At, Username: e.Username, Action: e.Action, Result: e.Result,
		Target: e.Target.String, ChangesetID: e.ChangesetID.String,
	}
	if e.DetailJSON.Valid {
		resp.Detail = json.RawMessage(e.DetailJSON.String)
	}
	return resp
}

// mountAuditRoutes registers docs/api.md's audit route: a single filterable,
// paginated GET, gated on the audit capability (a read of vnprox's own audit
// log, distinct from netRead's live-network-state reads).
func mountAuditRoutes(r chi.Router, svc AuditService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capAudit))
		r.Get("/audit", handleListAudit(svc))
	})
}

func handleListAudit(svc AuditService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := defaultAuditPageLimit
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "limit must be a positive integer")
				return
			}
			if n > maxAuditPageLimit {
				n = maxAuditPageLimit
			}
			limit = n
		}

		filter := store.AuditFilter{
			User: q.Get("user"), Action: q.Get("action"), Target: q.Get("target"),
			Result: q.Get("result"), ChangesetID: q.Get("changesetId"),
		}
		if v := q.Get("from"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "from must be a unix-seconds integer")
				return
			}
			filter.From = n
		}
		if v := q.Get("to"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "to must be a unix-seconds integer")
				return
			}
			filter.To = n
		}

		entries, next, err := svc.ListPage(r.Context(), filter, q.Get("cursor"), limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list audit entries")
			return
		}
		items := make([]auditEntryResponse, len(entries))
		for i, e := range entries {
			items[i] = toAuditEntryResponse(e)
		}
		writeJSON(w, http.StatusOK, auditListResponse{Items: items, NextCursor: next})
	}
}
