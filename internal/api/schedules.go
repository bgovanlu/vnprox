package api

// schedules.go implements T-1103's scheduled-changeset routes: POST/DELETE
// /changesets/{id}/schedule (session-gated, netWrite+CSRF, same convention
// as every other changeset mutation) and POST /changesets/{id}/schedule/ack
// (the webhook ack path — deliberately NOT session-gated, since a webhook
// caller carries no browser cookie; auth is the single-use, changeset-
// scoped signed callback token itself, verified by change.Service.
// AckSchedule against the row's stored hash).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxScheduleBodyBytes bounds a schedule/ack request body — these carry at
// most a handful of int64/string fields, so this is generous headroom
// against an abusive/buggy client, not a realistic limit (mirrors
// maxChangesetBodyBytes's own reasoning, just tighter since there's no ops
// array here).
const maxScheduleBodyBytes = 1 << 16

// ScheduleService is the optional ChangesetService extension backing T-1103
// (change.Service already implements it). Checked with a type assertion —
// the same pattern CSRFEnforcer/MgmtAckRecorder above use — so an existing
// ChangesetService test double that doesn't care about scheduling doesn't
// have to grow four new methods just because this feature exists; if the
// concrete Changesets service doesn't implement it, the schedule routes
// simply aren't mounted (mountScheduleRoutes' own nil/not-ok check).
type ScheduleService interface {
	Schedule(ctx context.Context, changesetID, author string, params change.ScheduleParams) (change.Schedule, error)
	GetSchedule(ctx context.Context, changesetID string) (change.Schedule, error)
	CancelSchedule(ctx context.Context, changesetID, author string) error
	AckSchedule(ctx context.Context, changesetID, token string) (change.Changeset, error)
}

// mountScheduleRoutes registers docs/api.md's schedule routes onto the same
// router mountChangesetsRoutes already builds the rest of the changesets
// family on. svc is the same ChangesetService NewRouter was given;
// scheduling only mounts if it also implements ScheduleService (in
// production, *change.Service always does).
func mountScheduleRoutes(r chi.Router, svc ChangesetService, auth AuthService, lookup UsernameLookup) {
	sched, ok := svc.(ScheduleService)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/changesets/{id}/schedule", handleCreateSchedule(sched, lookup))
		r.Delete("/changesets/{id}/schedule", handleCancelSchedule(sched, lookup))
	})

	// The webhook ack path deliberately carries no session/CSRF requirement
	// at all — see this file's package doc comment.
	r.Post("/changesets/{id}/schedule/ack", handleAckSchedule(sched))
}

// scheduleRequest is docs/api.md's POST /changesets/{id}/schedule body:
// `{windowStart, windowEnd, confirmTimeoutSec?, missedWindowPolicy?}`.
type scheduleRequest struct {
	MissedWindowPolicy string `json:"missedWindowPolicy,omitempty"`
	WindowStart        int64  `json:"windowStart"`
	WindowEnd          int64  `json:"windowEnd"`
	ConfirmTimeoutSec  int    `json:"confirmTimeoutSec,omitempty"`
}

// scheduleResponse is docs/api.md's Schedule wire shape. CallbackToken is
// populated only on the POST .../schedule response that created it (the
// one-time delivery — change.Schedule's own doc comment); every other read
// of a schedule (there is currently no GET route — see the report's scope
// note) never carries it, since it is never persisted in plaintext.
type scheduleResponse struct {
	FiredAt            *int64 `json:"firedAt,omitempty"`
	CancelledAt        *int64 `json:"cancelledAt,omitempty"`
	ChangesetID        string `json:"changesetId"`
	Status             string `json:"status"`
	MissedWindowPolicy string `json:"missedWindowPolicy"`
	CreatedBy          string `json:"createdBy"`
	CallbackToken      string `json:"callbackToken,omitempty"`
	WindowStart        int64  `json:"windowStart"`
	WindowEnd          int64  `json:"windowEnd"`
	ConfirmTimeoutSec  int    `json:"confirmTimeoutSec"`
	CreatedAt          int64  `json:"createdAt"`
}

func toScheduleResponse(s change.Schedule) scheduleResponse {
	return scheduleResponse{
		ChangesetID: s.ChangesetID, WindowStart: s.WindowStart, WindowEnd: s.WindowEnd,
		ConfirmTimeoutSec: s.ConfirmTimeoutSec, MissedWindowPolicy: s.MissedWindowPolicy,
		Status: s.Status, CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt,
		FiredAt: s.FiredAt, CancelledAt: s.CancelledAt, CallbackToken: s.CallbackToken,
	}
}

func handleCreateSchedule(svc ScheduleService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		var req scheduleRequest
		if r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScheduleBodyBytes))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
				return
			}
		}

		sched, err := svc.Schedule(r.Context(), id, username, change.ScheduleParams{
			WindowStart: req.WindowStart, WindowEnd: req.WindowEnd,
			ConfirmTimeoutSec: req.ConfirmTimeoutSec, MissedWindowPolicy: req.MissedWindowPolicy,
		})
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toScheduleResponse(sched))
	}
}

func handleCancelSchedule(svc ScheduleService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.CancelSchedule(r.Context(), id, username); err != nil {
			writeScheduleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ackRequest is docs/api.md's POST /changesets/{id}/schedule/ack body:
// `{token}` — the single-use signed callback token delivered once in the
// POST /changesets/{id}/schedule response.
type ackRequest struct {
	Token string `json:"token"`
}

func handleAckSchedule(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req ackRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScheduleBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || req.Token == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}

		cs, err := svc.AckSchedule(r.Context(), id, req.Token)
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(cs))
	}
}

// writeScheduleError maps T-1103 schedule errors to docs/api.md's error
// envelope + stable codes.
func writeScheduleError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset or schedule")
		return
	}
	if errors.Is(err, store.ErrIllegalState) {
		writeJSONError(w, http.StatusConflict, "schedule_not_cancelable", "this schedule is no longer pending")
		return
	}

	var mgmtForbidden *change.ErrMgmtPathUnattendedForbidden
	if errors.As(err, &mgmtForbidden) {
		writeJSONError(w, http.StatusUnprocessableEntity, "mgmt_path_unattended_forbidden",
			"this changeset touches a management path and cannot be scheduled for unattended apply")
		return
	}
	var blocked *change.ErrValidationBlocked
	if errors.As(err, &blocked) {
		writeJSONErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed",
			"changeset has blocking validation errors", map[string]any{"findings": blocked.Findings})
		return
	}
	var badWindow *change.ErrInvalidScheduleWindow
	if errors.As(err, &badWindow) {
		writeJSONError(w, http.StatusUnprocessableEntity, "bad_window", err.Error())
		return
	}
	var badPolicy *change.ErrInvalidMissedWindowPolicy
	if errors.As(err, &badPolicy) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_missed_window_policy", err.Error())
		return
	}
	var alreadyExists *change.ErrScheduleAlreadyExists
	if errors.As(err, &alreadyExists) {
		writeJSONError(w, http.StatusConflict, "schedule_already_exists", err.Error())
		return
	}
	var invalidToken *change.ErrInvalidCallbackToken
	if errors.As(err, &invalidToken) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_callback_token", err.Error())
		return
	}
	var illegal *change.ErrIllegalTransition
	if errors.As(err, &illegal) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	var notConfirmable *change.ErrNotConfirmable
	if errors.As(err, &notConfirmable) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	var notConfigured *change.ErrApplyNotConfigured
	if errors.As(err, &notConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "apply_unavailable", "the scheduling feature is not available on this node")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "schedule operation failed")
}
