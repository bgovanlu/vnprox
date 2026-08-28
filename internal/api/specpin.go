// SPDX-License-Identifier: Apache-2.0

// specpin.go implements T-1102's pinned-spec routes (docs/api.md's Spec
// section): GET/POST/DELETE /spec/pin. Pinning is the GitOps reconciler's
// declared desired state — app-owned data (docs/architecture.md §7), never a
// shadow copy of PVE config — that internal/drift's spec_drift check family
// reads back every drift cycle (internal/drift/specdrift.go) to diff live
// state against via T-1101's spec.Import, unchanged. This file never applies
// anything itself: pinning only ever stores a document; reconciling it is
// the normal "create fixing changeset" drift-fix flow every other check
// family already uses (POST /drift/{id}/fix, internal/api/drift.go).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/spec"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxSpecPinBodyBytes bounds POST /spec/pin's body — same generous headroom
// as spec.go's maxSpecBodyBytes (a whole-cluster spec is at most a few tens
// of KB even for a large cluster).
const maxSpecPinBodyBytes = 4 << 20 // 4 MiB

// PinnedSpecStore is the subset of *store.PinnedSpecRepo the router needs.
// *store.PinnedSpecRepo already has exactly this signature, so cmd/vnproxd
// wires the concrete repo in directly, the same "small interface, real type
// satisfies it for free" shape AuditService/LayoutStore use.
type PinnedSpecStore interface {
	Get(ctx context.Context) (store.PinnedSpec, error)
	Set(ctx context.Context, ps store.PinnedSpec) error
	Clear(ctx context.Context) error
}

// specPinAuditor is the minimal audit-log seam this route family needs —
// *store.AuditRepo satisfies it directly, the same one-method seam
// lldpInstallAuditor/simulateVerifyAuditor already declare rather than
// depending on the fuller read-oriented AuditService interface.
type specPinAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// specPinRequest is POST /spec/pin's body.
type specPinRequest struct {
	Content string `json:"content"`
}

// specPinResponse is GET/POST /spec/pin's response shape: pinned is false
// with every other field omitted when nothing is currently pinned.
//
//nolint:govet // fieldalignment: response DTO; field order is the JSON shape, not packing.
type specPinResponse struct {
	Content  string `json:"content,omitempty"`
	PinnedBy string `json:"pinnedBy,omitempty"`
	PinnedAt int64  `json:"pinnedAt,omitempty"`
	Pinned   bool   `json:"pinned"`
}

// mountSpecPinRoutes registers docs/api.md's `GET/POST/DELETE /spec/pin`.
// GET is netRead-gated like every other read route; POST/DELETE are
// netWrite + CSRF-gated like every other mutating route, since they change
// what internal/drift's spec_drift check reconciles against. Nil-safe:
// store == nil or auth == nil skips mounting every route in this family,
// matching every other optional Options field's degraded-mode convention.
func mountSpecPinRoutes(r chi.Router, pinStore PinnedSpecStore, audit specPinAuditor, auth AuthService) {
	if pinStore == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/spec/pin", handleGetSpecPin(pinStore))
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
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/spec/pin", handlePostSpecPin(pinStore, audit, lookup))
		r.Delete("/spec/pin", handleDeleteSpecPin(pinStore, audit, lookup))
	})
}

func toSpecPinResponse(ps store.PinnedSpec) specPinResponse {
	return specPinResponse{Pinned: true, Content: ps.Content, PinnedBy: ps.PinnedBy, PinnedAt: ps.PinnedAt}
}

func handleGetSpecPin(pinStore PinnedSpecStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ps, err := pinStore.Get(r.Context())
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, specPinResponse{Pinned: false})
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read pinned spec")
			return
		}
		writeJSON(w, http.StatusOK, toSpecPinResponse(ps))
	}
}

func handlePostSpecPin(pinStore PinnedSpecStore, audit specPinAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req specPinRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSpecPinBodyBytes))
		if err := dec.Decode(&req); err != nil || req.Content == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"content\": \"<yaml>\"}")
			return
		}
		// Validate before storing (specVersion must parse) — an operator
		// never pins a document internal/drift's spec_drift check can't
		// later reconcile against, mirroring POST /spec/import's own
		// up-front spec.Parse validation.
		if _, err := spec.Parse([]byte(req.Content)); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}

		ps := store.PinnedSpec{Content: req.Content, PinnedBy: username, PinnedAt: time.Now().Unix()}
		if err := pinStore.Set(r.Context(), ps); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save pinned spec")
			return
		}
		auditSpecPin(r.Context(), audit, username, "spec.pin")
		writeJSON(w, http.StatusOK, toSpecPinResponse(ps))
	}
}

func handleDeleteSpecPin(pinStore PinnedSpecStore, audit specPinAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		if err := pinStore.Clear(r.Context()); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not clear pinned spec")
			return
		}
		auditSpecPin(r.Context(), audit, username, "spec.unpin")
		w.WriteHeader(http.StatusNoContent)
	}
}

// auditSpecPin appends one audit_log row for a successful pin/unpin
// (docs/api.md's Live path probe section documents the codebase-wide
// convention this follows: "a malformed/invalid request is not audited" —
// only a request that actually mutated the pin gets a row). audit == nil
// (no audit repo wired, e.g. a bare test router) simply skips logging
// rather than failing the request — the mutation itself already succeeded
// and must not be masked by a logging failure.
func auditSpecPin(ctx context.Context, audit specPinAuditor, username, action string) {
	if audit == nil {
		return
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: action, Result: "ok"}
	_, _ = audit.Append(ctx, entry)
}
