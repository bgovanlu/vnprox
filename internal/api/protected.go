// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
)

// maxProtectedBodyBytes bounds the PUT /protected-interfaces request body —
// a protected-interface set is at most a handful of refs per node, so this
// ceiling (matching maxChangesetBodyBytes' reasoning) is generous headroom
// against an abusive/buggy client, not a realistic limit.
const maxProtectedBodyBytes = 1 << 20 // 1 MiB

// ProtectedService is the subset of *change.Service the router needs for
// T-203's onboarding-confirmed protected-interface set (docs/features/
// blueprints.md §3): read the current config, and persist an admin's
// confirmation/correction. Declared as an interface (the same seam pattern
// as ChangesetService above) so this package's dependency on the concrete
// change.Service stays small and testable.
type ProtectedService interface {
	GetProtected(ctx context.Context) (change.ProtectedConfig, error)
	SetProtected(ctx context.Context, author string, cfg change.ProtectedConfig) (change.ProtectedConfig, error)

	// SuggestProtected computes the detection-suggested set (inventory +
	// corosync.conf composed through change.DetectProtected) for the
	// onboarding "confirm or correct" flow — GET /protected-interfaces/
	// suggest (T-203's detection deliverable, wired per audit-phase-2 F-14).
	SuggestProtected(ctx context.Context) change.ProtectedSet

	// MgmtStatus computes T-702's per-node management-path status: roles,
	// resolved physical path, and redundancy — GET /protected-interfaces/
	// status. Also consumed by handleTopology (topology.go) to paint the
	// same data as mgmt/corosync/mgmt-path badges on GET /topology, so the
	// two surfaces are always derived from one computation.
	MgmtStatus(ctx context.Context) (change.MgmtStatus, error)
}

// protectedResponse is the wire shape of GET/PUT /api/v1/protected-interfaces
// — not yet part of docs/api.md's frozen contract (T-203's card asks for "a
// minimal API to read/update it", without a documented route shape the way
// changesets/layouts are), so this is a new, additive route this package
// introduces; see the T-203 report for a note that docs/api.md should grow
// an entry for it.
type protectedResponse struct {
	Nodes     map[string][]string `json:"nodes"`
	UpdatedBy string              `json:"updatedBy,omitempty"`
	UpdatedAt int64               `json:"updatedAt"`
	Version   int                 `json:"version"`
}

func toProtectedResponse(cfg change.ProtectedConfig) protectedResponse {
	nodes := cfg.Nodes
	if nodes == nil {
		nodes = map[string][]string{}
	}
	return protectedResponse{Nodes: nodes, UpdatedBy: cfg.UpdatedBy, UpdatedAt: cfg.UpdatedAt, Version: cfg.Version}
}

// protectedPutRequest is the PUT request body: {"nodes": {"<node>": ["<ref>",
// ...]}} — the admin's onboarding confirmation/correction of the detected
// protected-interface set (docs/features/blueprints.md §3).
type protectedPutRequest struct {
	Nodes map[string][]string `json:"nodes"`
}

// mountProtectedRoutes registers GET/PUT /api/v1/protected-interfaces: read
// requires netRead (same as every other read route in this package); the
// write requires netWrite plus CSRF (T-203's card: "gated on netWrite"),
// matching mountChangesetsRoutes' pattern exactly.
//
// svc and auth are nil-safe to call with (routes simply aren't mounted). If
// auth doesn't also implement UsernameLookup, the route is likewise not
// mounted — same reasoning as mountLayoutsRoutes/mountChangesetsRoutes:
// there would be no safe way to attribute a saved correction to a user for
// the audit trail docs/security.md requires.
func mountProtectedRoutes(r chi.Router, svc ProtectedService, auth AuthService) {
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
		r.Get("/protected-interfaces", handleGetProtected(svc))
		r.Get("/protected-interfaces/suggest", handleSuggestProtected(svc))
		r.Get("/protected-interfaces/status", handleMgmtStatus(svc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Put("/protected-interfaces", handlePutProtected(svc, lookup))
	})
}

func handleGetProtected(svc ProtectedService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := svc.GetProtected(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load protected-interface config")
			return
		}
		writeJSON(w, http.StatusOK, toProtectedResponse(cfg))
	}
}

// handleSuggestProtected backs GET /protected-interfaces/suggest: the
// detection-suggested protected set, in the same {"nodes": {...}} shape the
// PUT accepts, so the onboarding UI can present it for confirmation and
// submit the (possibly corrected) result straight back.
func handleSuggestProtected(svc ProtectedService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		set := svc.SuggestProtected(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"nodes": set.ToConfig()})
	}
}

func handlePutProtected(svc ProtectedService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProtectedBodyBytes))
		dec.DisallowUnknownFields()
		var req protectedPutRequest
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"nodes\": {...}}")
			return
		}

		cfg, err := svc.SetProtected(r.Context(), username, change.ProtectedConfig{Nodes: req.Nodes})
		if err != nil {
			var invalidRef *change.ErrInvalidProtectedRef
			if errors.As(err, &invalidRef) {
				writeJSONErrorDetails(w, http.StatusBadRequest, "validation_failed", err.Error(), map[string]any{"refs": invalidRef.Refs})
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save protected-interface config")
			return
		}
		writeJSON(w, http.StatusOK, toProtectedResponse(cfg))
	}
}

// mgmtStatusResponse is the wire shape of GET /api/v1/protected-interfaces/
// status (T-702, docs/api.md): per-node resolved management paths plus
// which source fed them ("confirmed" | "detected" — change.MgmtStatus's own
// doc comment).
type mgmtStatusResponse struct {
	Source  string                        `json:"source"`
	Nodes   map[string][]mgmtPathResponse `json:"nodes"`
	BadRefs []string                      `json:"badRefs,omitempty"`
	// StaleProtected (T-703, docs/api.md): the confirmed protected set no
	// longer contains a carrier live detection currently finds — the
	// management path moved since onboarding confirmed it (e.g. the
	// dedicated-management-VLAN flow committed and the protected-set
	// refresh was declined). Omitted while false.
	StaleProtected bool `json:"staleProtected,omitempty"`
}

// mgmtPathResponse is one resolved protected ref: docs/features/topology.md
// §3's "each protected ref with roles..., the resolved physical path..., and
// a redundant bool".
type mgmtPathResponse struct {
	Ref       string   `json:"ref"`
	Roles     []string `json:"roles"`
	Path      []string `json:"path"`
	Redundant bool     `json:"redundant"`
}

func toMgmtStatusResponse(status change.MgmtStatus) mgmtStatusResponse {
	nodes := make(map[string][]mgmtPathResponse, len(status.Nodes))
	for node, paths := range status.Nodes {
		list := make([]mgmtPathResponse, 0, len(paths))
		for _, p := range paths {
			roles := make([]string, len(p.Roles))
			for i, r := range p.Roles {
				roles[i] = string(r)
			}
			path := make([]string, len(p.Path))
			for i, ref := range p.Path {
				path[i] = ref.String()
			}
			list = append(list, mgmtPathResponse{
				Ref: p.Ref.String(), Roles: roles, Path: path, Redundant: p.Redundant,
			})
		}
		nodes[node] = list
	}
	return mgmtStatusResponse{Source: status.Source, Nodes: nodes, BadRefs: status.BadRefs, StaleProtected: status.StaleProtected}
}

// handleMgmtStatus backs GET /protected-interfaces/status: docs/features/
// topology.md §3's management-path visibility, rendered as the same shape
// GET /topology's mgmt/corosync/mgmt-path badges are painted from (see
// topology.go's handleTopology -> paintMgmtStatus). Never a 404/empty-body
// special case: an unconfirmed cluster still gets an answer, source
// "detected" (change.Service.MgmtStatus's own fallback).
func handleMgmtStatus(svc ProtectedService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := svc.MgmtStatus(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not compute management-path status")
			return
		}
		writeJSON(w, http.StatusOK, toMgmtStatusResponse(status))
	}
}
