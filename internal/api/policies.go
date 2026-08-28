// SPDX-License-Identifier: Apache-2.0

// policies.go is the HTTP surface of T-2601's policy-as-code guardrail:
// read the cluster's installed rule set, replace it (audited), and evaluate
// a candidate rule set against a real changeset without staging anything.
//
// Enforcement is deliberately NOT here. A policy `deny` blocks inside the
// change engine's validate stage (internal/change's policyValidate), which
// both the validate route and the pre-apply revalidation already run — so
// there is no way to reach apply through this package that skips it, and
// this file adds no gate of its own.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
)

// maxPolicyBodyBytes bounds a policy document accepted over the API. A rule
// set is human-authored and small; a megabyte is already far beyond any
// plausible one.
const maxPolicyBodyBytes = 1 << 20

// PolicyService is the seam this file needs from the change engine
// (*change.Service satisfies it).
type PolicyService interface {
	PolicyStatus(ctx context.Context) (change.PolicyStatus, error)
	SetPolicySet(ctx context.Context, author string, set change.PolicySet) (change.PolicyStatus, error)
	EvaluatePolicySet(ctx context.Context, set change.PolicySet, ops []change.Op) (change.PolicyResult, error)
	EvaluatePolicyForChangeset(ctx context.Context, set change.PolicySet, changesetID string) (change.PolicyResult, error)
	// Calendar (T-4006, extended T-4007) is the change-calendar view's read:
	// every declared freeze window and node maintenance window in the
	// installed policy set alongside every currently-pending scheduled
	// changeset — see change.Service.Calendar's own doc comment.
	Calendar(ctx context.Context) (change.CalendarView, error)
	// DeclareMaintenanceWindow/MaintenanceWindows/DeleteMaintenanceWindow
	// (T-4007) back the node-maintenance-mode surface — see
	// change/maintenance.go's own doc comments.
	DeclareMaintenanceWindow(ctx context.Context, actor string, in change.MaintenanceWindowInput) (change.MaintenanceWindow, error)
	MaintenanceWindows(ctx context.Context) ([]change.MaintenanceWindow, error)
	DeleteMaintenanceWindow(ctx context.Context, actor, id string) error
	MaintenanceState(ctx context.Context, node string) (change.MaintenanceState, error)
}

// policyPutRequest is `PUT /policies`' body: a whole policy document,
// replacing the installed one wholesale (there is no per-rule patch — a
// rule set is reviewed and installed as a unit).
type policyPutRequest struct {
	Rules   []change.PolicyRule `json:"rules"`
	Version int                 `json:"version,omitempty"`
}

// policyTestRequest is `POST /policies/test`' body. Exactly one of
// changesetId / ops names what to evaluate; an omitted policy means "the
// installed one", so an operator can ask what the live rule set says about
// a draft without holding a copy of it.
type policyTestRequest struct {
	Policy      *policyPutRequest `json:"policy,omitempty"`
	ChangesetID string            `json:"changesetId,omitempty"`
	Ops         []change.Op       `json:"ops,omitempty"`
}

func mountPolicyRoutes(r chi.Router, svc PolicyService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/policies", handleGetPolicies(svc))
		// A pure evaluation of a client-supplied document against an
		// existing changeset: it stages nothing and mutates nothing, so it
		// sits with the reads and needs no CSRF — the same placement
		// POST /interfaces/lint already has.
		r.Post("/policies/test", handleTestPolicy(svc))
		// T-4006: the change-calendar view — freeze windows (derived from
		// the same policy set GET /policies serves) alongside pending
		// scheduled changesets. A read, like the two routes above.
		r.Get("/calendar", handleGetCalendar(svc))
		// T-4007: declared node maintenance windows and the per-node "is
		// this node in maintenance right now" read model. Reads, like
		// everything else in this group.
		r.Get("/maintenance-windows", handleListMaintenanceWindows(svc))
		r.Get("/nodes/{node}/maintenance", handleGetMaintenanceState(svc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Put("/policies", handlePutPolicies(svc, lookup))
		// T-4007: declaring/ending a maintenance window is a write to
		// vnprox's own app-owned state (never to the network, and never
		// through the change engine), so it takes netWrite + CSRF like
		// every other authenticated mutation on this surface.
		r.Post("/maintenance-windows", handleDeclareMaintenanceWindow(svc, lookup))
		r.Delete("/maintenance-windows/{id}", handleDeleteMaintenanceWindow(svc, lookup))
	})
}

// maintenanceWindowPutRequest is `POST /maintenance-windows`' body.
type maintenanceWindowPutRequest struct {
	Node       string `json:"node"`
	Reason     string `json:"reason,omitempty"`
	Zone       string `json:"zone"`
	StartLocal string `json:"startLocal"`
	EndLocal   string `json:"endLocal"`
}

// handleListMaintenanceWindows serves `GET /maintenance-windows` (T-4007):
// every declared window, expired ones included — the same "the caller
// decides what's still active" contract GET /calendar's own
// maintenanceWindows already renders (this route exists in addition to
// that one for a client that wants the list without the freeze/schedule
// half of the calendar).
func handleListMaintenanceWindows(svc PolicyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		windows, err := svc.MaintenanceWindows(r.Context())
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": windows})
	}
}

// handleGetMaintenanceState serves `GET /nodes/{node}/maintenance` (T-4007):
// the read-model TwoPersonState.breakGlass's visibility pattern generalizes
// to — whether this node is in a declared maintenance window right now, and
// which one.
func handleGetMaintenanceState(svc PolicyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		state, err := svc.MaintenanceState(r.Context(), node)
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	}
}

// handleDeclareMaintenanceWindow serves `POST /maintenance-windows`
// (T-4007). Every statically-decidable defect (missing node, missing zone,
// unloadable zone, unparsable/backwards start-end) is refused with
// `400 validation_failed` naming the field, mirroring writePolicyError's
// existing *PolicyLoadError handling.
func handleDeclareMaintenanceWindow(svc PolicyService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req maintenanceWindowPutRequest
		if !decodePolicyBody(w, r, &req) {
			return
		}
		win, err := svc.DeclareMaintenanceWindow(r.Context(), username, change.MaintenanceWindowInput{
			Node: req.Node, Reason: req.Reason, Zone: req.Zone, StartLocal: req.StartLocal, EndLocal: req.EndLocal,
		})
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, win)
	}
}

// handleDeleteMaintenanceWindow serves `DELETE /maintenance-windows/{id}`
// (T-4007): end a declared window early, or correct a mistaken one. Not an
// error to delete an absent id, mirroring DELETE /findings/{id}/ack's
// identical "always clearable" contract.
func handleDeleteMaintenanceWindow(svc PolicyService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.DeleteMaintenanceWindow(r.Context(), username, id); err != nil {
			writePolicyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleGetPolicies(svc PolicyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := svc.PolicyStatus(r.Context())
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

// handleGetCalendar serves `GET /calendar` (T-4006): the change-calendar
// view. Read-only, like handleGetPolicies immediately above — enforcement
// still happens exactly once, inside the change engine's validate stage;
// this route only renders what is already declared/scheduled.
func handleGetCalendar(svc PolicyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view, err := svc.Calendar(r.Context())
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

func handlePutPolicies(svc PolicyService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req policyPutRequest
		if !decodePolicyBody(w, r, &req) {
			return
		}
		status, err := svc.SetPolicySet(r.Context(), username, change.PolicySet{Version: req.Version, Rules: req.Rules})
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

func handleTestPolicy(svc PolicyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req policyTestRequest
		if !decodePolicyBody(w, r, &req) {
			return
		}
		if (req.ChangesetID == "") == (len(req.Ops) == 0) {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "exactly one of changesetId or ops is required")
			return
		}
		var set change.PolicySet
		if req.Policy != nil {
			set = change.PolicySet{Version: req.Policy.Version, Rules: req.Policy.Rules}
		}

		var (
			result change.PolicyResult
			err    error
		)
		if req.ChangesetID != "" {
			result, err = svc.EvaluatePolicyForChangeset(r.Context(), set, req.ChangesetID)
		} else {
			result, err = svc.EvaluatePolicySet(r.Context(), set, req.Ops)
		}
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func decodePolicyBody(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPolicyBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed policy request body: "+err.Error())
		return false
	}
	return true
}

// writePolicyError maps the change engine's policy errors onto the
// documented error envelope. A *change.PolicyLoadError is reported in full
// — file, rule id, and field are exactly what the operator needs to fix it,
// and are what acceptance criterion 5 requires the message to name.
func writePolicyError(w http.ResponseWriter, err error) {
	var loadErr *change.PolicyLoadError
	if errors.As(err, &loadErr) {
		writeJSONErrorDetails(w, http.StatusBadRequest, "validation_failed", err.Error(), map[string]any{
			"file": loadErr.File, "ruleId": loadErr.RuleID, "field": loadErr.Field,
		})
		return
	}
	var notConfigured *change.ErrPolicyNotConfigured
	if errors.As(err, &notConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "policy_unavailable", err.Error())
		return
	}
	// T-4007: a maintenance-window declaration's statically-decidable
	// defects (missing node, missing/unloadable zone, unparsable or
	// backwards start/end) name the offending field the same way
	// *PolicyLoadError does above.
	var maintInvalid *change.ErrMaintenanceWindowInvalid
	if errors.As(err, &maintInvalid) {
		writeJSONErrorDetails(w, http.StatusBadRequest, "validation_failed", err.Error(), map[string]any{
			"field": maintInvalid.Field,
		})
		return
	}
	var maintNotConfigured *change.ErrMaintenanceWindowNotConfigured
	if errors.As(err, &maintNotConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "maintenance_window_unavailable", err.Error())
		return
	}
	writeApplyError(w, err)
}
