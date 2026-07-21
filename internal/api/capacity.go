package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// CapacityService is the subset of the capacity export machinery the router
// needs (T-1606): the aggregate history for one (ref, kind), plus the
// configured retention window so the handler can bound the export to exactly
// aggregate_retention_days. Declared as an interface (the same seam pattern as
// every other *Service in this package) so cmd/vnproxd wires the concrete
// store-backed service in and tests can substitute a fake.
type CapacityService interface {
	// ExportHistory returns (ref, kind)'s aggregates with bucket_at >= since,
	// ordered by bucket_at ascending.
	ExportHistory(ctx context.Context, ref, kind string, since int64) ([]store.CapacityAggregate, error)
	// RetentionDays is the configured [capacity] aggregate_retention_days, the
	// age bound the export is clamped to.
	RetentionDays() int
}

// capacityAggregateResponse is one item of GET /capacity/export's JSON body.
type capacityAggregateResponse struct {
	BucketAt       int64   `json:"bucketAt"`
	AvgUtilization float64 `json:"avgUtilization"`
	MaxUtilization float64 `json:"maxUtilization"`
	CreatedAt      int64   `json:"createdAt"`
}

// capacityExportResponse is GET /capacity/export's JSON envelope.
type capacityExportResponse struct {
	Ref        string                      `json:"ref"`
	Kind       string                      `json:"kind"`
	Aggregates []capacityAggregateResponse `json:"aggregates"`
}

// mountCapacityRoutes registers docs/api.md's `GET /capacity/export?ref=&kind=
// &format=csv|json` (T-1606). netRead-gated like every other read route in
// this package — an export is a read-only, derived artifact
// (docs/architecture.md's "an export is a derived read-only artifact, not
// something to persist"), bounded to the same retention window the store
// itself prunes to, never a live-data dump. svc/auth nil-safe (route not
// mounted), matching every other mountXRoutes function.
func mountCapacityRoutes(r chi.Router, svc CapacityService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/capacity/export", handleCapacityExport(svc, time.Now))
	})
}

func handleCapacityExport(svc CapacityService, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		format := q.Get("format")
		if format == "" {
			format = "json"
		}
		if format != "csv" && format != "json" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `format must be "csv" or "json"`)
			return
		}
		ref := q.Get("ref")
		if ref == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref is required")
			return
		}
		kind := q.Get("kind")
		if kind != store.CapacityKindLink && kind != store.CapacityKindIPAMPool {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `kind must be "link" or "ipam_pool"`)
			return
		}

		// Bound the export to exactly the retention window: rows older than
		// aggregate_retention_days are absent even in the gap between prune
		// ticks (T-1606 AC4).
		keepDays := svc.RetentionDays()
		if keepDays <= 0 {
			keepDays = store.DefaultCapacityRetentionDays
		}
		since := now().UTC().AddDate(0, 0, -keepDays).Unix()

		rows, err := svc.ExportHistory(r.Context(), ref, kind, since)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not export capacity history")
			return
		}

		if format == "csv" {
			writeCapacityCSV(w, ref, kind, rows)
			return
		}

		items := make([]capacityAggregateResponse, 0, len(rows))
		for _, a := range rows {
			items = append(items, capacityAggregateResponse{
				BucketAt:       a.BucketAt,
				AvgUtilization: a.AvgUtilization,
				MaxUtilization: a.MaxUtilization,
				CreatedAt:      a.CreatedAt,
			})
		}
		writeJSON(w, http.StatusOK, capacityExportResponse{Ref: ref, Kind: kind, Aggregates: items})
	}
}

// writeCapacityCSV renders the aggregate history as CSV with a header row —
// the machine-readable half of the export path this retention extension is
// required to carry (design §6 rule 4).
func writeCapacityCSV(w http.ResponseWriter, ref, kind string, rows []store.CapacityAggregate) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vnprox-capacity-%s.csv"`, kind))
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"ref", "kind", "bucket_at", "avg_utilization", "max_utilization", "created_at"})
	for _, a := range rows {
		_ = cw.Write([]string{
			ref,
			kind,
			strconv.FormatInt(a.BucketAt, 10),
			strconv.FormatFloat(a.AvgUtilization, 'f', -1, 64),
			strconv.FormatFloat(a.MaxUtilization, 'f', -1, 64),
			strconv.FormatInt(a.CreatedAt, 10),
		})
	}
	cw.Flush()
}
