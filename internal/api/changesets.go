package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/store"
)

// capNetWrite is docs/api.md's documented write-capability flag name
// (internal/auth.CapNetWrite's underlying string), spelled out as a plain
// string for the same reason topology.go's capNetRead is (see that
// constant's doc comment): keeping this package's auth dependency to the
// AuthService method-seam interface.
const capNetWrite = "netWrite"

// maxChangesetBodyBytes bounds a draft create/update request body. A
// changeset with, say, a hundred ops is at most a few tens of KB even with
// verbose params (fw rule lists, SDN objects); this ceiling is generous
// headroom against an abusive/buggy client, not a realistic limit.
const maxChangesetBodyBytes = 4 << 20 // 4 MiB

// ChangesetService is the subset of *change.Service the router needs: T-201's
// draft CRUD, T-202's Validate, and T-205's diff/apply/confirm/rollback.
// Declared as an interface (the same seam pattern as AuthService/
// TopologyService/LayoutStore above) so this package's dependency on the
// concrete change.Service stays small and testable without a real SQLite
// file.
type ChangesetService interface {
	List(ctx context.Context, status string) ([]change.Changeset, error)
	Get(ctx context.Context, id string) (change.Changeset, error)
	Create(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error)
	UpdateDraft(ctx context.Context, id, author string, title *string, ops []change.Op) (change.Changeset, error)
	Discard(ctx context.Context, id, author string) error
	Validate(ctx context.Context, id, author string) (change.Changeset, error)

	// T-205 apply engine.
	Diff(ctx context.Context, id string) (*ifaces.ChangesetDiff, error)
	Apply(ctx context.Context, id, author string, pveGW change.PVEGateway, confirmTimeout time.Duration) (change.Changeset, error)
	Confirm(ctx context.Context, id, author string) (change.Changeset, error)
	Rollback(ctx context.Context, id, author string) (change.Changeset, error)

	// T-208 raw editor: the current live file + hash the editor opens
	// against (see the raw-editor routes mounted alongside the changeset
	// CRUD routes below).
	ReadRawInterfaces(ctx context.Context, node string) (content, hash string, err error)
}

// PVEGatewayProvider supplies a change.PVEGateway bound to the requesting
// session's own PVE ticket (docs/architecture.md §6: writes use the user's
// ticket). cmd/vnproxd wires it from auth.Service.PVEClientFor; a nil provider
// (or one returning ok=false) means cluster-scope PVE steps (sdn.apply) can't
// run for this request — apply of a changeset needing them then fails clearly.
type PVEGatewayProvider interface {
	GatewayFor(ctx context.Context) (change.PVEGateway, bool)
}

// CSRFEnforcer is implemented by AuthService backends that can check the
// double-submit CSRF header (internal/auth.Service.CSRFMiddleware, per
// docs/api.md's conventions section: "X-VNPROX-CSRF header on mutating
// requests"). It is checked with a type assertion — the same pattern
// UsernameLookup uses just above in layouts.go — rather than folded into
// the AuthService interface itself, so existing AuthService test doubles
// that don't need CSRF behavior (this package's own fakeAuth) don't have
// to grow a method just because the changesets routes need one. If auth
// doesn't implement this, the mutating changesets routes still mount
// (unlike the UsernameLookup case, where there'd be no safe author to
// record at all) but skip CSRF enforcement — acceptable only for test
// doubles; cmd/vnproxd's real authServiceAdapter always implements it via
// the embedded *auth.Service.
type CSRFEnforcer interface {
	CSRFMiddleware(next http.Handler) http.Handler
}

// changesetResponse is the wire shape of a changeset, per docs/api.md's
// changesets section ("GET /changesets/{id} — full changeset incl.
// findings, plan, apply log"). Findings is never emitted as a JSON null
// (an empty array instead) so frontend code can always range over it
// without a nil check.
type changesetResponse struct {
	Plan            json.RawMessage  `json:"plan,omitempty"`
	ApplyLog        json.RawMessage  `json:"applyLog,omitempty"`
	ConfirmDeadline *int64           `json:"confirmDeadline,omitempty"`
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Author          string           `json:"author"`
	Status          string           `json:"status"`
	Ops             []change.Op      `json:"ops"`
	Findings        []change.Finding `json:"findings"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
}

func toChangesetResponse(c change.Changeset) changesetResponse {
	ops := c.Ops
	if ops == nil {
		ops = []change.Op{}
	}
	findings := c.Findings
	if findings == nil {
		findings = []change.Finding{}
	}
	return changesetResponse{
		ID: c.ID, Title: c.Title, Author: c.Author, Status: string(c.Status),
		Ops: ops, Findings: findings, Plan: c.Plan, ApplyLog: c.ApplyLog,
		ConfirmDeadline: c.ConfirmDeadline, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// mountChangesetsRoutes registers docs/api.md's changesets routes: the
// T-201 draft CRUD (list/create/get/update-draft/delete-draft), T-202's
// validate, and T-205's diff/apply/confirm/rollback — all backed by real
// service logic. Read routes require netRead; every mutating route requires
// netWrite plus (when the auth backend supports it — see CSRFEnforcer) a
// valid CSRF header.
//
// svc and auth are nil-safe to call with (routes simply aren't mounted),
// matching mountTopologyRoutes/mountLayoutsRoutes' pattern. If auth
// doesn't also implement UsernameLookup, the routes are likewise not
// mounted — same reasoning as mountLayoutsRoutes: there would be no safe
// way to attribute a created/discarded changeset to a user for the
// audit trail docs/security.md requires.
func mountChangesetsRoutes(r chi.Router, svc ChangesetService, auth AuthService, gateways PVEGatewayProvider) {
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
		r.Get("/changesets", handleListChangesets(svc))
		r.Get("/changesets/{id}", handleGetChangeset(svc))
		r.Get("/changesets/{id}/diff", handleDiffChangeset(svc))

		// T-208 raw editor: the "open" call and its live syntax-lint
		// round trip. Neither mutates server state (the lint endpoint
		// only parses a client-supplied string; it does not even name a
		// node), so both live in the netRead group with no CSRF
		// requirement, alongside every other read route above.
		r.Get("/nodes/{node}/interfaces/raw", handleGetRawInterfaces(svc))
		r.Post("/interfaces/lint", handleLintInterfaces())
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/changesets", handleCreateChangeset(svc, lookup))
		r.Put("/changesets/{id}", handleUpdateChangeset(svc, lookup))
		r.Delete("/changesets/{id}", handleDiscardChangeset(svc, lookup))
		r.Post("/changesets/{id}/validate", handleValidateChangeset(svc, lookup))
		r.Post("/changesets/{id}/apply", handleApplyChangeset(svc, lookup, gateways))
		r.Post("/changesets/{id}/confirm", handleConfirmChangeset(svc, lookup))
		r.Post("/changesets/{id}/rollback", handleRollbackChangeset(svc, lookup))
	})
}

// applyRequest is docs/api.md's POST /changesets/{id}/apply body:
// `{confirmTimeoutSec: 120}`.
type applyRequest struct {
	ConfirmTimeoutSec int `json:"confirmTimeoutSec"`
}

func handleApplyChangeset(svc ChangesetService, lookup UsernameLookup, gateways PVEGatewayProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		var req applyRequest
		if r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
				return
			}
		}

		var gw change.PVEGateway
		if gateways != nil {
			gw, _ = gateways.GatewayFor(r.Context())
		}

		c, err := svc.Apply(r.Context(), id, username, gw, time.Duration(req.ConfirmTimeoutSec)*time.Second)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, toChangesetResponse(c))
	}
}

func handleConfirmChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		c, err := svc.Confirm(r.Context(), chi.URLParam(r, "id"), username)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

func handleRollbackChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		c, err := svc.Rollback(r.Context(), chi.URLParam(r, "id"), username)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

func handleDiffChangeset(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		diff, err := svc.Diff(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeApplyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, diff)
	}
}

// rawInterfacesResponse is `GET /nodes/{node}/interfaces/raw`'s body: the
// raw Monaco editor's "open" call (T-208). SHA256 is the conflict-guard
// baseline the editor stamps into its eventual iface.raw.replace op's
// baseHash param.
type rawInterfacesResponse struct {
	Node    string `json:"node"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

func handleGetRawInterfaces(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		content, hash, err := svc.ReadRawInterfaces(r.Context(), node)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "peer_unreachable",
				fmt.Sprintf("could not read /etc/network/interfaces on node %s", node))
			return
		}
		writeJSON(w, http.StatusOK, rawInterfacesResponse{Node: node, Content: content, SHA256: hash})
	}
}

// lintInterfacesRequest is `POST /interfaces/lint`'s body: `{content}` —
// deliberately node-less, since this is a pure interfaces(5) syntax check
// (T-208 AC1's "syntax errors underline with line-precise messages as you
// type"), not a validation of any particular node's state.
type lintInterfacesRequest struct {
	Content string `json:"content"`
}

// lintInterfacesResponse is `POST /interfaces/lint`'s body: `{errors}`, one
// entry per host.ParseError the T-102 parser reports (today: at most one,
// since that parser stops at the first syntax error — see
// change.LintRawInterfaces's doc comment).
type lintInterfacesResponse struct {
	Errors []change.LintMarker `json:"errors"`
}

func handleLintInterfaces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
		dec.DisallowUnknownFields()
		var req lintInterfacesRequest
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}
		markers := change.LintRawInterfaces(req.Content)
		if markers == nil {
			markers = []change.LintMarker{}
		}
		writeJSON(w, http.StatusOK, lintInterfacesResponse{Errors: markers})
	}
}

// writeApplyError maps T-205 apply-engine errors to docs/api.md's error
// envelope + stable codes.
func writeApplyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
		return
	}

	var locked *change.ErrChangesetLocked
	if errors.As(err, &locked) {
		writeJSONError(w, http.StatusConflict, "changeset_locked", err.Error())
		return
	}
	var blocked *change.ErrValidationBlocked
	if errors.As(err, &blocked) {
		writeJSONErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed",
			"changeset has blocking validation errors", map[string]any{"findings": blocked.Findings})
		return
	}
	var unsupported *change.ErrUnsupportedOp
	if errors.As(err, &unsupported) {
		writeJSONError(w, http.StatusUnprocessableEntity, "unsupported_op", err.Error())
		return
	}
	var restoreUnsupported *change.ErrRestoreUnsupported
	if errors.As(err, &restoreUnsupported) {
		writeJSONError(w, http.StatusUnprocessableEntity, "unsupported_op", err.Error())
		return
	}
	var incompatiblePeer *change.ErrIncompatiblePeer
	if errors.As(err, &incompatiblePeer) {
		writeJSONErrorDetails(w, http.StatusConflict, "peer_incompatible", err.Error(), map[string]any{"node": incompatiblePeer.Node})
		return
	}
	var notConfirmable *change.ErrNotConfirmable
	if errors.As(err, &notConfirmable) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	var windowExpired *change.ErrRollbackWindowExpired
	if errors.As(err, &windowExpired) {
		writeJSONError(w, http.StatusConflict, "rollback_window_expired", err.Error())
		return
	}
	var illegal *change.ErrIllegalTransition
	if errors.As(err, &illegal) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	var notConfigured *change.ErrApplyNotConfigured
	if errors.As(err, &notConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "apply_unavailable", "the apply engine is not available on this node")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "apply operation failed")
}

func handleListChangesets(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		changesets, err := svc.List(r.Context(), status)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list changesets")
			return
		}
		out := make([]changesetResponse, len(changesets))
		for i, c := range changesets {
			out[i] = toChangesetResponse(c)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleGetChangeset(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		c, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load changeset")
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

// opsField decodes a JSON `ops` array one element at a time so any
// *change.OpDecodeError can be prefixed with the failing op's index —
// `ops[7].params.mtu` instead of a bare `params.mtu`, which is ambiguous
// in a multi-op body (audit-phase-2 F-19). Each element is still decoded by
// change.Op's own strict UnmarshalJSON; this wrapper only adds position.
type opsField []change.Op

func (o *opsField) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return &change.OpDecodeError{Path: "ops", Message: "ops must be an array of op objects"}
	}
	if raws == nil { // JSON null: preserve []change.Op's nil semantics
		*o = nil
		return nil
	}
	ops := make([]change.Op, len(raws))
	for i, raw := range raws {
		if err := json.Unmarshal(raw, &ops[i]); err != nil {
			path := fmt.Sprintf("ops[%d]", i)
			var opErr *change.OpDecodeError
			if errors.As(err, &opErr) {
				if opErr.Path != "" {
					path += "." + opErr.Path
				}
				return &change.OpDecodeError{Path: path, Message: opErr.Message}
			}
			return &change.OpDecodeError{Path: path, Message: err.Error()}
		}
	}
	*o = ops
	return nil
}

// createChangesetRequest is docs/api.md's POST /changesets body:
// `{title, ops:[Op]}`.
type createChangesetRequest struct {
	Title string   `json:"title"`
	Ops   opsField `json:"ops"`
}

func handleCreateChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		req, decErr := decodeChangesetRequest(w, r)
		if decErr != nil {
			writeOpDecodeError(w, decErr)
			return
		}

		c, err := svc.Create(r.Context(), username, req.Title, req.Ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create changeset")
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(c))
	}
}

// updateChangesetRequest is docs/api.md's PUT /changesets/{id} body:
// `{ops:[Op]}`. Title is an additional, optional field this package
// accepts so a parked draft can be renamed via the same endpoint
// (docs/features/change-management.md §1: "Multiple named drafts can be
// parked and resumed") without a second route docs/api.md doesn't
// document.
type updateChangesetRequest struct {
	Title *string  `json:"title,omitempty"`
	Ops   opsField `json:"ops"`
}

func handleUpdateChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
		dec.DisallowUnknownFields()
		var req updateChangesetRequest
		if err := dec.Decode(&req); err != nil {
			var opErr *change.OpDecodeError
			if errors.As(err, &opErr) {
				writeOpDecodeError(w, opErr)
				return
			}
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}

		c, err := svc.UpdateDraft(r.Context(), id, username, req.Title, req.Ops)
		if err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

func handleDiscardChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		if err := svc.Discard(r.Context(), id, username); err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleValidateChangeset backs `POST /changesets/{id}/validate`
// (docs/api.md: "re-run validation, returns findings") with T-202's real
// pipeline.
func handleValidateChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		c, err := svc.Validate(r.Context(), id, username)
		if err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

// decodeChangesetRequest strictly decodes a POST /changesets body,
// bounding it to maxChangesetBodyBytes.
func decodeChangesetRequest(w http.ResponseWriter, r *http.Request) (createChangesetRequest, *change.OpDecodeError) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
	dec.DisallowUnknownFields()
	var req createChangesetRequest
	if err := dec.Decode(&req); err != nil {
		var opErr *change.OpDecodeError
		if errors.As(err, &opErr) {
			return createChangesetRequest{}, opErr
		}
		return createChangesetRequest{}, &change.OpDecodeError{Path: "", Message: "malformed request body"}
	}
	return req, nil
}

// writeOpDecodeError translates a *change.OpDecodeError into docs/api.md's
// `validation_failed` error envelope, with the offending JSON path in
// `details.path` (T-201 acceptance criterion 1).
func writeOpDecodeError(w http.ResponseWriter, err *change.OpDecodeError) {
	writeJSONErrorDetails(w, http.StatusBadRequest, "validation_failed", err.Message, map[string]any{"path": err.Path})
}

// writeJSONErrorDetails is writeJSONError (router.go) plus docs/api.md's
// optional `details` object — router.go's own helper has no details
// parameter, and this package's other routes have never needed one, so
// this is kept local to the one handler set that does rather than
// widening every existing writeJSONError call site's signature.
func writeJSONErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

// writeChangesetMutationError maps UpdateDraft/Discard errors to HTTP
// status per docs/api.md's error envelope: a missing changeset is 404; an
// illegal status transition (e.g. discarding an already-applied
// changeset) is 409 — a stable code this package introduces since
// docs/api.md's error-code list is explicitly non-exhaustive ("...").
func writeChangesetMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
		return
	}
	var illegal *change.ErrIllegalTransition
	if errors.As(err, &illegal) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not update changeset")
}
