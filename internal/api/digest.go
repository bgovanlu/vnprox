// digest.go exposes T-2807's digest schedule over the API.
//
//	GET /digest/schedule   the current schedule and the last run's outcome
//	PUT /digest/schedule   change the cadence, recipients, or enablement
//
// T-2807 landed the runner, the renderer and the store repository, but no
// route: three cards were in flight against docs/openapi.json at the time and
// the card itself names no API acceptance criterion. The consequence was a
// feature configurable only by writing to SQLite by hand, which is not
// "configurable schedule and recipients" in any sense an operator would
// recognise. These two routes close that.
//
// The write takes the same gate as every other configuration-changing route
// (session + netWrite + CSRF). Reading takes netRead: a schedule names
// alert_rules ids, which are already netRead-visible, and nothing here carries
// a target's credentials — the rule ids are a filter over T-2407's existing
// targets, not an address book.
//
// PUT is a full replace rather than a patch, matching how the store models a
// schedule (one row, upserted). A caller that wants to flip `enabled` alone
// reads first and writes the whole object back, which is the same contract the
// other single-object configuration routes in this package use.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// digestScheduleID is the single schedule the daemon runs. The store is keyed
// so a later per-tenant schedule is an INSERT rather than a migration; the API
// pins the daemon's one until there is a second.
const digestScheduleID = "default"

// minDigestEverySec floors the cadence. A digest is a summary of a period, and
// a period shorter than an hour produces a report with nothing in it and a
// delivery attempt per tick — the failure mode T-2807 AC1 exists to prevent,
// arrived at from the other direction.
const minDigestEverySec = 3600

// DigestScheduleService is the seam these routes are served from;
// *store.DigestRepo satisfies it directly.
//
// It carries no send, generate, or deliver verb: changing a schedule must not
// be a way to trigger a delivery, or the route becomes an unauthenticated
// amplifier pointed at whatever targets the rules name.
type DigestScheduleService interface {
	Schedule(ctx context.Context, id string) (store.DigestSchedule, error)
	UpsertSchedule(ctx context.Context, s store.DigestSchedule) error
	LatestRun(ctx context.Context, scheduleID string) (store.DigestRun, error)
}

type digestScheduleResponse struct {
	LastRun   *digestRunResponse `json:"lastRun"`
	UpdatedBy string             `json:"updatedBy"`
	RuleIDs   []string           `json:"ruleIds"`
	EverySec  int64              `json:"everySec"`
	UpdatedAt int64              `json:"updatedAt"`
	Enabled   bool               `json:"enabled"`
}

type digestRunResponse struct {
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	PeriodStart int64  `json:"periodStart"`
	PeriodEnd   int64  `json:"periodEnd"`
	GeneratedAt int64  `json:"generatedAt"`
	Quiet       bool   `json:"quiet"`
}

type digestScheduleRequest struct {
	RuleIDs  *[]string `json:"ruleIds"`
	EverySec *int64    `json:"everySec"`
	Enabled  *bool     `json:"enabled"`
}

func mountDigestRoutes(r chi.Router, svc DigestScheduleService, auth AuthService) {
	if auth == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/digest/schedule", handleGetDigestSchedule(svc))
	})

	lookup, ok := auth.(UsernameLookup)
	if !ok {
		// No safe way to attribute the change to a user, so no write route —
		// the same reasoning every other mutating route in this package uses.
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Put("/digest/schedule", handlePutDigestSchedule(svc, lookup))
	})
}

func handleGetDigestSchedule(svc DigestScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeJSONError(w, http.StatusNotImplemented, "not_implemented", "scheduled digests are not available on this deployment")
			return
		}
		sched, err := svc.Schedule(r.Context(), digestScheduleID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// A daemon that has never had a schedule written still has a
				// schedule: the disabled one. Reporting 404 here would make a
				// client special-case "not configured yet" for no gain.
				writeJSON(w, http.StatusOK, digestScheduleResponse{RuleIDs: []string{}, EverySec: 0, Enabled: false})
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal", "reading the digest schedule failed")
			return
		}
		writeJSON(w, http.StatusOK, toDigestScheduleResponse(r.Context(), svc, sched))
	}
}

func handlePutDigestSchedule(svc DigestScheduleService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeJSONError(w, http.StatusNotImplemented, "not_implemented", "scheduled digests are not available on this deployment")
			return
		}
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req digestScheduleRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "request body is not valid JSON")
			return
		}

		// Start from what is stored so a field the caller omits keeps its
		// value rather than silently resetting to zero — a PUT that turned an
		// omitted everySec into "every 0 seconds" would be a foot-gun.
		current, err := svc.Schedule(r.Context(), digestScheduleID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusInternalServerError, "internal", "reading the digest schedule failed")
			return
		}
		current.ID = digestScheduleID

		if req.EverySec != nil {
			current.EverySec = *req.EverySec
		}
		if req.Enabled != nil {
			current.Enabled = *req.Enabled
		}
		if req.RuleIDs != nil {
			current.RuleIDs = *req.RuleIDs
		}

		// An enabled schedule must have a workable cadence. A disabled one may
		// carry any cadence, including none: disabling is how an operator
		// silences a digest without losing the cadence they chose.
		if current.Enabled && current.EverySec < minDigestEverySec {
			writeJSONError(w, http.StatusBadRequest, "invalid_request",
				"everySec must be at least 3600 (one hour) for an enabled digest schedule")
			return
		}

		current.UpdatedBy = username
		current.UpdatedAt = time.Now().Unix()
		if current.RuleIDs == nil {
			current.RuleIDs = []string{}
		}

		if err := svc.UpsertSchedule(r.Context(), current); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal", "saving the digest schedule failed")
			return
		}
		writeJSON(w, http.StatusOK, toDigestScheduleResponse(r.Context(), svc, current))
	}
}

func toDigestScheduleResponse(ctx context.Context, svc DigestScheduleService, s store.DigestSchedule) digestScheduleResponse {
	out := digestScheduleResponse{
		RuleIDs:   s.RuleIDs,
		UpdatedBy: s.UpdatedBy,
		EverySec:  s.EverySec,
		UpdatedAt: s.UpdatedAt,
		Enabled:   s.Enabled,
	}
	if out.RuleIDs == nil {
		out.RuleIDs = []string{}
	}
	if run, err := svc.LatestRun(ctx, s.ID); err == nil {
		out.LastRun = &digestRunResponse{
			Status:      run.Status,
			Detail:      run.Detail,
			PeriodStart: run.PeriodStart,
			PeriodEnd:   run.PeriodEnd,
			GeneratedAt: run.GeneratedAt,
			Quiet:       run.Quiet,
		}
	}
	return out
}
