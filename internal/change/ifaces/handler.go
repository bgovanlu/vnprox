package ifaces

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bgovanlu/vnprox/internal/host"
)

// ChangesetLookup is the one method NewDiffHandler needs from a changeset
// store: the node-file-affecting ops of one changeset, in the order the
// user added them. T-201's changeset store (built concurrently in a
// separate worktree) is expected to satisfy this directly, or via a small
// adapter that filters its full Op tagged union down to the
// iface/bond/bridge/vlan groups and converts each to this package's
// concrete Op types (or round-trips through DecodeOps if it already
// serializes ops as the docs/api.md {op,target,params} envelope — see
// op.go's doc comment). It is declared here, not depended on from
// internal/change, so this package has zero compile-time dependency on
// T-201's still-in-progress types.
type ChangesetLookup interface {
	Ops(id string) ([]Op, error)
}

// ErrChangesetNotFound is the sentinel a ChangesetLookup should wrap when
// asked for an id it doesn't have, so NewDiffHandler can map it onto
// docs/api.md's 404 shape.
var ErrChangesetNotFound = errors.New("ifaces: changeset not found")

// NewDiffHandler builds the GET /changesets/{id}/diff handler (docs/api.md)
// for the ops this package understands. It is deliberately a bare
// http.HandlerFunc, not wired into any router, because the real route also
// needs auth/capability middleware and T-201's changeset CRUD surface,
// neither of which exists in this package's worktree; the integrator
// mounts it (e.g. `r.Get("/changesets/{id}/diff",
// ifaces.NewDiffHandler(store, hostReader))`) once those land. idParam
// extracts the "{id}" path parameter's value from the request — callers
// using chi pass chi.URLParam directly (`func(r *http.Request) string {
// return chi.URLParam(r, "id") }`); this package does not import chi
// itself to keep this handler router-agnostic.
func NewDiffHandler(lookup ChangesetLookup, reader host.Reader, idParam func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := idParam(r)
		ops, err := lookup.Ops(id)
		if err != nil {
			if errors.Is(err, ErrChangesetNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		diff, err := DiffChangeset(r.Context(), reader, ops, id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "diff_failed", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(diff)
	}
}

// writeJSONError writes docs/api.md's error envelope
// {"error":{"code","message"}}. Mirrors internal/api's unexported
// writeJSONError; duplicated rather than imported since that helper isn't
// exported and this package intentionally has no dependency on
// internal/api.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}
