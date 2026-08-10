// compliance.go is the HTTP surface of T-2706's compliance profiles and
// evidence export: list the installed profiles, report one profile's
// controls with the evidence behind each, and export that report as a
// timestamped document.
//
// Every route here is read-only and netRead-gated. There is no write path:
// a compliance report is a derived artifact assembled from surfaces that
// already exist (the findings stream, the posture score, the installed
// policy set), never something staged, applied or persisted.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/compliance"
	"github.com/bgovanlu/vnprox/internal/docexport"
)

// ComplianceService is the router's seam onto the compliance reporter
// (*compliance.Service satisfies it).
type ComplianceService interface {
	ListProfiles() []compliance.ProfileSummary
	Report(ctx context.Context, profileID string, asOf time.Time) (compliance.Report, error)
}

// complianceProfilesResponse is GET /compliance's envelope.
type complianceProfilesResponse struct {
	Items []compliance.ProfileSummary `json:"items"`
}

// mountComplianceRoutes registers GET /compliance, GET
// /compliance/{profile}, and GET /export/compliance/{profile}. svc/auth
// nil-safe (routes not mounted), the standard degraded-mode convention.
func mountComplianceRoutes(r chi.Router, svc ComplianceService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/compliance", handleComplianceProfiles(svc))
		r.Get("/compliance/{profile}", handleComplianceReport(svc))
		r.Get("/export/compliance/{profile}", handleComplianceExport(svc))
	})
}

func handleComplianceProfiles(svc ComplianceService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		items := svc.ListProfiles()
		if items == nil {
			items = []compliance.ProfileSummary{}
		}
		writeJSON(w, http.StatusOK, complianceProfilesResponse{Items: items})
	}
}

func handleComplianceReport(svc ComplianceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		asOf, ok := parseComplianceAsOf(w, r)
		if !ok {
			return
		}
		report, err := svc.Report(r.Context(), chi.URLParam(r, "profile"), asOf)
		if err != nil {
			writeComplianceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

// handleComplianceExport renders the report in any format
// docexport.ComplianceRenderers() offers. The supported-format list comes
// from the registry rather than a literal here, so adding a format cannot
// leave this route rejecting it.
func handleComplianceExport(svc ComplianceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		renderer, known := docexport.ComplianceRendererFor(format)
		if !known {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				"format must be one of "+strings.Join(docexport.ComplianceFormats(), ", "))
			return
		}
		asOf, ok := parseComplianceAsOf(w, r)
		if !ok {
			return
		}

		report, err := svc.Report(r.Context(), chi.URLParam(r, "profile"), asOf)
		if err != nil {
			writeComplianceError(w, err)
			return
		}

		stamp := time.Unix(report.GeneratedAt, 0).UTC().Format("20060102-150405")
		w.Header().Set("Content-Type", renderer.ContentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vnprox-compliance-%s-%s.%s"`,
			report.ProfileID, stamp, renderer.Extension))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderer.Render(report)))
	}
}

// parseComplianceAsOf reads `?asOf=`, which accepts either an RFC3339
// timestamp or unix seconds. Absent means "live".
func parseComplianceAsOf(w http.ResponseWriter, r *http.Request) (time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("asOf"))
	if raw == "" {
		return time.Time{}, true
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, true
	}
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil && secs > 0 {
		return time.Unix(secs, 0), true
	}
	writeJSONError(w, http.StatusBadRequest, "validation_failed",
		"asOf must be an RFC3339 timestamp or unix seconds")
	return time.Time{}, false
}

// writeComplianceError maps the reporter's errors onto the documented error
// envelope.
//
// An out-of-window request is a 400 carrying the earliest date the evidence
// reaches, in `details` — not a 200 with a thinner report. The caller must
// be able to correct the request; it must NOT be able to mistake a report
// built from absent evidence for a report about that date.
func writeComplianceError(w http.ResponseWriter, err error) {
	var unknown *compliance.ErrUnknownProfile
	if errors.As(err, &unknown) {
		writeJSONErrorDetails(w, http.StatusNotFound, "not_found", err.Error(), map[string]any{
			"profileId": unknown.ID, "availableProfiles": unknown.Available,
		})
		return
	}
	var outside *compliance.ErrOutsideRetention
	if errors.As(err, &outside) {
		details := map[string]any{
			"requested":       outside.Requested.UTC().Format(time.RFC3339),
			"hasRetainedData": outside.HasEarliest,
		}
		if outside.HasEarliest {
			details["earliestAvailable"] = outside.Earliest.UTC().Format(time.RFC3339)
			details["earliestAvailableUnix"] = outside.Earliest.Unix()
		}
		writeJSONErrorDetails(w, http.StatusBadRequest, "outside_retention_window", err.Error(), details)
		return
	}
	var future *compliance.ErrFutureAsOf
	if errors.As(err, &future) {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not build the compliance report")
}
