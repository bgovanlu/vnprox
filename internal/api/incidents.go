// SPDX-License-Identifier: Apache-2.0

// incidents.go serves T-2804's incident view: one timeline stitching the
// diagnosis ladder, captures, findings, recent flows, the changeset history
// and the T-2704 point-in-time diff into a single chronological account of a
// window an operator is investigating.
//
// Capability: every route here requires `audit`, the same gate GET /audit and
// GET /history/events use. The timeline re-exposes real audit_log rows (the
// changeset and diagnosis halves), so it can never be gated more loosely than
// the route that already serves them — and the export carries the same
// redacted diagnostic material `vnproxctl support-bundle` produces.
//
// The mutating routes additionally require CSRF, per docs/api.md's
// conventions. What they mutate is only vnprox's own record of the
// investigation: an incident row and its annotations. Nothing here reaches
// the change engine, and IncidentService's method set is where that is
// enforced rather than stated — there is no Apply, Stage or Confirm on it.

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/incident"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxIncidentBodyBytes bounds a request body here — the same order as the
// annotations route, for the same reason.
const maxIncidentBodyBytes = 64 << 10

// maxIncidentTextLen bounds a title or an annotation body.
const maxIncidentTextLen = 4000

// IncidentService is the subset of *incident.Service these routes need.
//
// Read methods plus the incident record's own lifecycle. No mutation of
// network state is reachable through it, which is structural rather than
// conventional: the interface has nowhere to say so.
type IncidentService interface {
	List(ctx context.Context) ([]incident.Incident, error)
	Open(ctx context.Context, req incident.OpenRequest) (incident.Incident, error)
	Get(ctx context.Context, id string) (incident.Incident, error)
	Timeline(ctx context.Context, id string) (*incident.Timeline, error)
	Annotate(ctx context.Context, id string, req incident.AnnotateRequest) (incident.Annotation, error)
	Close(ctx context.Context, id string) (incident.Incident, error)
	Reopen(ctx context.Context, id string) (incident.Incident, error)
	Export(ctx context.Context, id string, opts incident.ExportOptions) (*incident.ExportResult, error)
}

// incidentAuditor is the minimal audit-log seam this file needs. Only the
// export is audited: opening or annotating an incident changes nothing about
// the cluster, but producing a redacted archive of this install is an event
// worth being able to account for later.
type incidentAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

type incidentListResponse struct {
	Items []incident.Incident `json:"items"`
}

// incidentOpenRequest is POST /incidents' body.
type incidentOpenRequest struct {
	Title string `json:"title"`
	// StartedAt/EndedAt are unix seconds. Both omitted means "from now,
	// still unfolding"; both supplied is the retroactive case, which is an
	// ordinary request rather than a mode.
	StartedAt int64 `json:"startedAt,omitempty"`
	EndedAt   int64 `json:"endedAt,omitempty"`
}

// incidentAnnotateRequest is POST /incidents/{id}/annotations' body.
type incidentAnnotateRequest struct {
	Body string `json:"body"`
	At   int64  `json:"at,omitempty"`
}

// mountIncidentRoutes registers docs/api.md's Incidents section.
//
// A nil service leaves every route unmounted; an auth backend with no
// UsernameLookup leaves the mutating ones unmounted (there would be no way to
// record who opened an incident), matching every other write route in this
// package.
func mountIncidentRoutes(r chi.Router, svc IncidentService, audit incidentAuditor, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capAudit))
		r.Get("/incidents", handleListIncidents(svc))
		r.Get("/incidents/{id}", handleGetIncident(svc))
		r.Get("/incidents/{id}/timeline", handleIncidentTimeline(svc))
		r.Get("/incidents/{id}/postmortem", handleIncidentPostmortem(svc))
	})

	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capAudit))
		r.Post("/incidents", handleOpenIncident(svc, lookup))
		r.Post("/incidents/{id}/annotations", handleAnnotateIncident(svc, lookup))
		r.Post("/incidents/{id}/close", handleCloseIncident(svc))
		r.Post("/incidents/{id}/reopen", handleReopenIncident(svc))
	})

	// The export is a GET so a browser can download it directly, exactly
	// like GET /captures/{id}/download and GET /export/compliance/{profile}.
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capAudit))
		r.Get("/incidents/{id}/export", handleExportIncident(svc, audit, lookup))
	})
}

func handleListIncidents(svc IncidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := svc.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list incidents")
			return
		}
		if items == nil {
			items = []incident.Incident{}
		}
		writeJSON(w, http.StatusOK, incidentListResponse{Items: items})
	}
}

func handleGetIncident(svc IncidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inc, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeIncidentError(w, err, "could not read the incident")
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}

func handleIncidentTimeline(svc IncidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tl, err := svc.Timeline(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeIncidentError(w, err, "could not assemble the incident timeline")
			return
		}
		writeJSON(w, http.StatusOK, tl)
	}
}

// handleIncidentPostmortem renders the SAME Timeline GET
// /incidents/{id}/timeline serves, through internal/docexport's Markdown/HTML
// machinery (T-4102) — a readable document alongside, not instead of,
// GET /incidents/{id}/export's redacted support-bundle archive. It gathers
// nothing new: docexport.PostmortemDataOf is a pure projection of the same
// *incident.Timeline this route already knows how to fetch.
func handleIncidentPostmortem(svc IncidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		if format != "md" && format != "html" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `format must be "md" or "html"`)
			return
		}

		id := chi.URLParam(r, "id")
		tl, err := svc.Timeline(r.Context(), id)
		if err != nil {
			writeIncidentError(w, err, "could not assemble the incident timeline")
			return
		}

		data := docexport.PostmortemDataOf(tl, time.Now().Unix())
		stamp := time.Unix(data.GeneratedAt, 0).UTC().Format("20060102-150405")

		switch format {
		case "md":
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="vnprox-postmortem-`+id+`-`+stamp+`.md"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(docexport.PostmortemMarkdown(data)))
		case "html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="vnprox-postmortem-`+id+`-`+stamp+`.html"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(docexport.PostmortemHTML(data)))
		}
	}
}

func handleOpenIncident(svc IncidentService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req incidentOpenRequest
		if err := decodeIncidentBody(w, r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				"request body must be {\"title\": \"...\", \"startedAt\"?: unix, \"endedAt\"?: unix}")
			return
		}
		if len(req.Title) > maxIncidentTextLen {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				"title must be 1.."+strconv.Itoa(maxIncidentTextLen)+" characters")
			return
		}
		inc, err := svc.Open(r.Context(), incident.OpenRequest{
			Title: req.Title, Actor: username, StartedAt: req.StartedAt, EndedAt: req.EndedAt,
		})
		if err != nil {
			writeIncidentError(w, err, "could not open the incident")
			return
		}
		writeJSON(w, http.StatusCreated, inc)
	}
}

func handleAnnotateIncident(svc IncidentService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req incidentAnnotateRequest
		if err := decodeIncidentBody(w, r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				"request body must be {\"body\": \"...\", \"at\"?: unix}")
			return
		}
		if len(req.Body) > maxIncidentTextLen {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				"body must be 1.."+strconv.Itoa(maxIncidentTextLen)+" characters")
			return
		}
		note, err := svc.Annotate(r.Context(), chi.URLParam(r, "id"), incident.AnnotateRequest{
			Body: req.Body, Author: username, At: req.At,
		})
		if err != nil {
			writeIncidentError(w, err, "could not annotate the incident")
			return
		}
		writeJSON(w, http.StatusCreated, note)
	}
}

func handleCloseIncident(svc IncidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inc, err := svc.Close(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeIncidentError(w, err, "could not close the incident")
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}

func handleReopenIncident(svc IncidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inc, err := svc.Reopen(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeIncidentError(w, err, "could not reopen the incident")
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}

// handleExportIncident streams the one artifact: the timeline plus a support
// bundle, produced through internal/backup's existing redaction path.
//
// The archive is written to a fresh temporary directory and removed once it
// has been served. It is never left in the backup directory, where retention
// would eventually treat it as one — see incident.ExportName.
func handleExportIncident(svc IncidentService, audit incidentAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		res, err := svc.Export(r.Context(), id, incident.ExportOptions{})
		if err != nil {
			if errors.Is(err, incident.ErrExportUnavailable) {
				writeJSONError(w, http.StatusServiceUnavailable, "export_unavailable",
					"exporting an incident is not configured on this node")
				return
			}
			writeIncidentError(w, err, "could not export the incident")
			return
		}
		defer func() { _ = os.RemoveAll(filepath.Dir(res.Path)) }()

		auditIncidentExport(r.Context(), audit, lookup, id, res)

		f, openErr := os.Open(res.Path)
		if openErr != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read the exported artifact")
			return
		}
		defer func() { _ = f.Close() }()

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+res.Filename+`"`)
		w.Header().Set("Content-Length", strconv.FormatInt(res.Bytes, 10))
		w.WriteHeader(http.StatusOK)
		// Streamed rather than read into memory: an artifact is tens of
		// kilobytes today, but nothing about a bundle guarantees that. The
		// headers are already written, so a copy failure has nowhere left to
		// be reported.
		_, _ = io.Copy(w, f)
	}
}

// auditIncidentExport records one `incident.export` row. A failed audit write
// never fails the request it is auditing, the same treatment every other
// audit call site in this package gives it.
func auditIncidentExport(ctx context.Context, audit incidentAuditor, lookup UsernameLookup, id string, res *incident.ExportResult) {
	if audit == nil {
		return
	}
	username := ""
	if lookup != nil {
		if u, ok := lookup.Username(ctx); ok {
			username = u
		}
	}
	events := 0
	if res.Timeline != nil {
		events = len(res.Timeline.Events)
	}
	_, _ = audit.Append(ctx, store.AuditEntry{
		At: time.Now().Unix(), Username: username, Action: "incident.export", Result: "success",
		Target: sql.NullString{String: id, Valid: true},
		DetailJSON: sql.NullString{
			String: `{"bytes":` + strconv.FormatInt(res.Bytes, 10) + `,"events":` + strconv.Itoa(events) + `}`,
			Valid:  true,
		},
	})
}

// decodeIncidentBody decodes a bounded request body, rejecting unknown fields
// the same way every other typed body in this package does.
func decodeIncidentBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIncidentBodyBytes))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// writeIncidentError maps the service's refusals onto docs/api.md's error
// envelope.
func writeIncidentError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case incident.IsNotFound(err):
		writeJSONError(w, http.StatusNotFound, "not_found", "no such incident")
	case errors.Is(err, incident.ErrTitleRequired),
		errors.Is(err, incident.ErrWindowInverted),
		errors.Is(err, incident.ErrAnnotationEmpty):
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
	case errors.Is(err, incident.ErrAlreadyClosed):
		writeJSONError(w, http.StatusConflict, "incident_closed", err.Error())
	case errors.Is(err, incident.ErrAlreadyOpen):
		writeJSONError(w, http.StatusConflict, "incident_open", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", fallback)
	}
}
