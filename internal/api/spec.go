// spec.go implements T-1101's declarative-spec routes (docs/api.md's Spec
// section):
//
//   - GET  /spec         — export the live cluster network as specVersion:1 YAML
//   - POST /spec/import   — diff a spec against live, produce a DRAFT changeset
//
// Import never applies: it hands the reconcile ops to the same
// ChangesetService.Create every other programmatic op-drafting path
// (blueprint instantiate, drift fix) already uses, and reports entities
// present live but absent from the spec in a distinct notInSpec list rather
// than deleting them — no implicit prune (see internal/spec's doc.go).

package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// maxSpecBodyBytes bounds a spec import body — same generous headroom as
// changesets.go's maxChangesetBodyBytes (a whole-cluster spec is at most a
// few tens of KB even for a large cluster).
const maxSpecBodyBytes = 4 << 20 // 4 MiB

// SpecInventory is the snapshot seam the spec routes read live state from:
// *inventory.Graph satisfies it via its Snapshot method, wired in cmd/vnproxd
// from the same graph collect populates — the same small-interface seam every
// other cross-package dependency in this package uses.
type SpecInventory interface {
	Snapshot() inventory.Snapshot
}

// specExportResponse is GET /spec's wire shape: the specVersion as a
// first-class field plus the rendered YAML document as content.
type specExportResponse struct {
	Content     string `json:"content"`
	SpecVersion int    `json:"specVersion"`
}

// specImportRequest is POST /spec/import's body.
type specImportRequest struct {
	Content string `json:"content"`
}

// specImportResponse embeds the created draft changeset (same shape as
// POST /changesets) and adds notInSpec: the Ref strings of managed-kind
// entities present live but absent from the imported spec — reported, never
// deleted. Field order is the JSON contract (changeset fields first, then
// notInSpec), not memory layout, so fieldalignment is suppressed.
//
//nolint:govet // fieldalignment: response DTO; field order is the JSON shape, not packing.
type specImportResponse struct {
	changesetResponse
	NotInSpec []string `json:"notInSpec"`
}

// mountSpecRoutes registers the spec routes. GET /spec is netRead-gated like
// every other read route; POST /spec/import is netWrite + CSRF like every
// other mutating route (it creates a draft changeset). Both are nil-safe:
// with no inventory source or no changeset service the relevant route simply
// isn't mounted, matching this package's degraded-mode convention.
func mountSpecRoutes(r chi.Router, inv SpecInventory, changesets ChangesetService, auth AuthService) {
	if inv == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/spec", handleExportSpec(inv))
	})

	lookup, ok := auth.(UsernameLookup)
	if !ok || changesets == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/spec/import", handleImportSpec(inv, changesets, lookup))
	})
}

func handleExportSpec(inv SpecInventory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := spec.Export(inv.Snapshot())
		b, err := spec.Marshal(s)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not render spec")
			return
		}
		writeJSON(w, http.StatusOK, specExportResponse{SpecVersion: s.SpecVersion, Content: string(b)})
	}
}

func handleImportSpec(inv SpecInventory, changesets ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req specImportRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSpecBodyBytes))
		if err := dec.Decode(&req); err != nil || req.Content == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"content\": \"<yaml>\"}")
			return
		}

		parsed, err := spec.Parse([]byte(req.Content))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		ops, notInSpec, err := spec.Import(parsed, inv.Snapshot())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}

		c, err := changesets.Create(r.Context(), username, specChangesetTitle(parsed), ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create changeset")
			return
		}

		refs := make([]string, len(notInSpec))
		for i, ref := range notInSpec {
			refs[i] = ref.String()
		}
		writeJSON(w, http.StatusCreated, specImportResponse{
			changesetResponse: toChangesetResponse(c),
			NotInSpec:         refs,
		})
	}
}

// specChangesetTitle names the draft an imported spec produces.
func specChangesetTitle(_ spec.Spec) string {
	return "Spec import"
}
