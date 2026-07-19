// tokens.go implements T-1104's automation-token routes (docs/api.md's
// Tokens section):
//
//   - POST   /tokens       — mint a scoped bearer token, one-time-reveal
//   - GET    /tokens       — list the caller's own tokens (no secret)
//   - DELETE /tokens/{id}  — revoke one of the caller's own tokens
//
// A token is vnprox-local, capability-scoped, and explicitly NOT a second
// login/authentication path (docs/security.md: "No vnprox-local accounts"
// is about login; a token is a delegated credential a logged-in user
// mints). Minting is gated on nothing beyond being authenticated at all
// (any capability level) — what actually bounds a token's power is scope
// validation itself: a requested scope must both be a recognized
// capability name and one the minting session's own derived capabilities
// already grant (auth.Identity.ValidateScopeGrant, via the TokenMinter
// seam below), so "no capability required to call POST /tokens" does not
// mean "unbounded" — a user with zero PVE privileges can only ever mint an
// empty-scope (or automation-only, see caps.go's doc comment) token.
//
// Tokens/webhooks/the WS "events" topic have no frontend UI deliverable in
// this task (unlike, say, T-1107's blueprint bundle import dialog) — this
// is a deliberate automation-only surface for T-1105 (vnproxctl)/T-1106
// (Terraform/Ansible) to build on, not a Settings-page feature yet.
//
// GET/DELETE are scoped to the caller's own tokens (filtered/checked by
// created_by) rather than exposing every user's tokens cluster-wide — the
// task card doesn't specify visibility scoping explicitly, but returning
// other users' token names/scopes (and letting anyone revoke anyone
// else's token) is a needless information-disclosure/DoS surface a
// same-user restriction avoids for free; flagged in the task report as an
// interpretation call.

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

// maxTokenBodyBytes bounds a POST /tokens request body — a name plus a
// short scope list, generous headroom against an abusive/buggy client,
// matching maxAlertRuleBodyBytes' reasoning.
const maxTokenBodyBytes = 4 << 10 // 4 KiB

// APITokenStore is the subset of *store.APITokenRepo the router needs.
type APITokenStore interface {
	Create(ctx context.Context, t store.APIToken) error
	Get(ctx context.Context, id string) (store.APIToken, error)
	List(ctx context.Context) ([]store.APIToken, error)
	Revoke(ctx context.Context, id string, now int64) error
}

// tokenAuditor is the minimal audit-log seam this route needs — the same
// shape lldpInstallAuditor declares for its own route, *store.AuditRepo
// satisfies it directly.
type tokenAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// TokenWSCloser is the seam DELETE /tokens/{id} uses to force-close any
// live WS connection the revoked token authenticated
// (internal/topology.Service.CloseByTokenID satisfies this directly, the
// same concrete value Options.Topology already wires in).
type TokenWSCloser interface {
	CloseByTokenID(id string) int
}

// TokenMinter is implemented by AuthService backends that can validate and
// generate T-1104 automation tokens for the current session
// (internal/auth's concrete Service, via cmd/vnproxd's authServiceAdapter).
// Declared as its own narrow seam — like UsernameLookup/CSRFEnforcer above
// — so this package's dependency on internal/auth's capability/scope model
// stays plain-string-in, plain-string-out, the same decoupling
// AuthService.RequireCap's own doc comment describes ("takes the
// capability's plain string name ... rather than internal/auth's own Cap
// type").
type TokenMinter interface {
	UsernameLookup
	// ValidateTokenScopes normalizes rawScopes (POST /tokens' submitted
	// `scopes` field) against the full capability vocabulary and the
	// current session's own derived capabilities in one call. On success
	// it returns the normalized, de-duplicated scope-name list and
	// ok=true. On failure ok is false and status/code/message are exactly
	// what this package's writeJSONError should respond with: 400
	// validation_failed for an unrecognized scope name, 403 forbidden for
	// a scope the session doesn't itself hold (docs/api.md's Tokens
	// section: "a token's scopes can never exceed the creating user's own
	// derived capabilities at creation time").
	ValidateTokenScopes(ctx context.Context, rawScopes []string) (scopes []string, status int, code, message string, ok bool)
	// GenerateToken mints a new random bearer token value plus its stored
	// hash (internal/auth.GenerateAPIToken) — kept behind this seam for
	// the same reason ValidateTokenScopes is: this package never imports
	// internal/auth directly.
	GenerateToken() (raw, hash string, err error)
}

type tokenResponse struct {
	LastUsedAt *int64   `json:"lastUsedAt,omitempty"`
	RevokedAt  *int64   `json:"revokedAt,omitempty"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	CreatedBy  string   `json:"createdBy"`
	Scopes     []string `json:"scopes"`
	CreatedAt  int64    `json:"createdAt"`
}

type tokensListResponse struct {
	Items []tokenResponse `json:"items"`
}

// tokenCreateResponse is POST /tokens' one-time-reveal response: the only
// point in this token's lifetime its raw value is ever transmitted.
type tokenCreateResponse struct {
	Token string `json:"token"`
	tokenResponse
}

type tokenCreateRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func toTokenResponse(t store.APIToken, scopes []string) tokenResponse {
	resp := tokenResponse{ID: t.ID, Name: t.Name, Scopes: scopes, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt}
	if t.LastUsedAt.Valid {
		v := t.LastUsedAt.Int64
		resp.LastUsedAt = &v
	}
	if t.RevokedAt.Valid {
		v := t.RevokedAt.Int64
		resp.RevokedAt = &v
	}
	return resp
}

func decodeTokenScopes(scopesJSON string) []string {
	var scopes []string
	if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
		return []string{}
	}
	return scopes
}

// mountTokenRoutes registers docs/api.md's Tokens routes. Nil-safe: any
// missing dependency skips mounting the whole family, matching every other
// optional Options field's degraded-mode convention. minter is checked via
// a type assertion against auth (the same TokenMinter-implements-AuthService
// pattern UsernameLookup/CSRFEnforcer use) rather than a separate Options
// field, so a test double that doesn't implement it simply doesn't mount
// these routes rather than panicking.
func mountTokenRoutes(r chi.Router, tokens APITokenStore, audit tokenAuditor, wsCloser TokenWSCloser, auth AuthService) {
	if tokens == nil || audit == nil || auth == nil {
		return
	}
	minter, ok := auth.(TokenMinter)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Get("/tokens", handleListTokens(tokens, minter))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Post("/tokens", handleCreateToken(tokens, audit, minter))
		r.Delete("/tokens/{id}", handleDeleteToken(tokens, audit, wsCloser, minter))
	})
}

func handleListTokens(tokens APITokenStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		list, err := tokens.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list tokens")
			return
		}
		items := make([]tokenResponse, 0, len(list))
		for _, t := range list {
			if t.CreatedBy != username {
				continue
			}
			items = append(items, toTokenResponse(t, decodeTokenScopes(t.ScopesJSON)))
		}
		writeJSON(w, http.StatusOK, tokensListResponse{Items: items})
	}
}

func handleCreateToken(tokens APITokenStore, audit tokenAuditor, minter TokenMinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := minter.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req tokenCreateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTokenBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed token request body: "+err.Error())
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "name is required")
			return
		}

		scopes, status, code, message, ok := minter.ValidateTokenScopes(r.Context(), req.Scopes)
		if !ok {
			writeJSONError(w, status, code, message)
			return
		}

		raw, hash, err := minter.GenerateToken()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not generate token")
			return
		}
		scopesJSON, err := json.Marshal(scopes)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encode token scopes")
			return
		}

		now := time.Now().Unix()
		t := store.APIToken{
			ID: store.NewULID(), Name: req.Name, TokenHash: hash, ScopesJSON: string(scopesJSON),
			CreatedBy: username, CreatedAt: now,
		}
		if err := tokens.Create(r.Context(), t); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save token")
			return
		}

		auditTokenAction(r.Context(), audit, username, "token.create", t.ID, map[string]any{"name": t.Name, "scopes": scopes})

		writeJSON(w, http.StatusCreated, tokenCreateResponse{
			tokenResponse: toTokenResponse(t, scopes),
			Token:         raw,
		})
	}
}

func handleDeleteToken(tokens APITokenStore, audit tokenAuditor, wsCloser TokenWSCloser, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		existing, err := tokens.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such token")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up token")
			return
		}
		if existing.CreatedBy != username {
			// Do not distinguish "exists but isn't yours" from "doesn't
			// exist" in the response — a 404 rather than a 403 avoids
			// confirming another user's token id exists at all.
			writeJSONError(w, http.StatusNotFound, "not_found", "no such token")
			return
		}

		if err := tokens.Revoke(r.Context(), id, time.Now().Unix()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such token")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not revoke token")
			return
		}

		if wsCloser != nil {
			wsCloser.CloseByTokenID(id)
		}

		auditTokenAction(r.Context(), audit, username, "token.revoke", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func auditTokenAction(ctx context.Context, audit tokenAuditor, username, action, tokenID string, detail map[string]any) {
	full := map[string]any{"tokenId": tokenID}
	for k, v := range detail {
		full[k] = v
	}
	detailJSON, err := json.Marshal(full)
	if err != nil {
		return
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: action, Result: "success"}
	entry.Target.String, entry.Target.Valid = tokenID, true
	entry.DetailJSON.String, entry.DetailJSON.Valid = string(detailJSON), true
	_, _ = audit.Append(ctx, entry)
}
