package api

// embed.go implements T-1706's embeddable, read-only, token-scoped views
// (docs/api.md's Embeds section, docs/security.md's embed-token model):
//
//   - POST /embed/tokens        — mint a read-only embed token (session +
//                                 CSRF gated); any write scope is rejected
//                                 400 regardless of the minting user's own
//                                 capabilities.
//   - GET  /embed/map?token=       ┐  read-only "shell" routes for wikis /
//   - GET  /embed/dashboard?token= ├  NOC screens / status pages: validate
//   - GET  /embed/posture?token=   ┘  the embed token from the *query
//                                     string only* (never a session cookie)
//                                     and serve the SPA shell, which then
//                                     boots the matching read-only embed
//                                     view and calls the ordinary read APIs
//                                     with the same token as a bearer.
//
// Security invariants (docs/security.md's "Embed tokens" section):
//   - An embed token is an ordinary api_tokens row (T-1104) whose scopes are
//     all read-only. It is minted through POST /embed/tokens, which rejects
//     any non-read-only scope (400) *before* the ordinary ceiling check, and
//     rejects a scope the minting user does not itself hold (403, via the
//     shared TokenMinter.ValidateTokenScopes seam) — so an embed token can
//     never exceed its minting user's own capabilities and never carries a
//     write surface. There is deliberately no persisted `embed` column and
//     therefore no migration: "embed" is defined structurally by the
//     read-only scope set, which the view-route auth re-checks on every
//     request (a token that carries any write scope is refused at an embed
//     route, 403), keeping the forward-only migration guarantee intact.
//   - The view routes are a *distinct middleware path* from every
//     session-cookie route: embedViewAuth reads the token only from the
//     `?token=` query parameter and never inspects the session cookie, so an
//     embed route can never be satisfied by a logged-in browser session in
//     place of an embed token (AC6).
//   - Read APIs the embedded view calls (GET /topology, /posture, ...) are
//     the existing netRead/sdnRead/... -gated routes, reached with the embed
//     token as an ordinary `Authorization: Bearer` credential (T-1104's
//     bearer middleware) — so an embed token scoped narrower than its minting
//     user never surfaces data above its own scopes (AC3), with no parallel
//     read path invented here.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// embedReadOnlyScopes is the closed set of capability names an embed token
// may carry — the read-only subset of caps.go's vocabulary. netWrite,
// sdnWrite, fwWrite, guestNet (VM.Config.Network is a write), capture (raw
// payload bytes — a materially stronger read than a config counter, and
// never something a wiki/NOC embed should expose) and automation (not a
// data scope at all) are all deliberately excluded: minting an embed token
// with any of them is a 400.
var embedReadOnlyScopes = map[string]bool{
	"netRead": true,
	"sdnRead": true,
	"fwRead":  true,
	"audit":   true,
}

// embedViews is the closed set of shell views the view routes serve — the
// map, the home dashboard, and the posture report (docs/api.md's Embeds
// section). An unknown view name is a 404.
var embedViews = map[string]bool{
	"map":       true,
	"dashboard": true,
	"posture":   true,
}

// embedTokenReader is the one method embedViewAuth needs from the api_tokens
// store: a hash lookup, the same read the T-1104 bearer middleware performs.
// *store.APITokenRepo satisfies it directly; mountEmbedViewRoutes obtains it
// from the same Options.Tokens value POST /embed/tokens writes through, via a
// type assertion, so no new Options field is needed.
type embedTokenReader interface {
	GetByHash(ctx context.Context, hash string) (store.APIToken, error)
}

// hashEmbedToken computes the hex SHA-256 of a raw bearer token — the exact
// form api_tokens.token_hash stores (must match internal/auth.HashAPIToken;
// duplicated here rather than imported to keep this package's decoupling
// from internal/auth, the same plain-string-in/out seam every other token
// route in this package uses).
func hashEmbedToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// embedTokenCreateResponse is POST /embed/tokens' one-time-reveal body: the
// ordinary token fields plus the always-true `embed` marker and the
// (read-only) scopes the caller can rely on.
type embedTokenCreateResponse struct {
	Token string `json:"token"`
	tokenResponse
	Embed bool `json:"embed"`
}

// mountEmbedTokenRoute registers POST /embed/tokens under /api/v1. Nil-safe
// exactly like mountTokenRoutes: any missing dependency (or an Auth backend
// that isn't a TokenMinter) skips mounting, the standard degraded-mode
// convention.
func mountEmbedTokenRoute(r chi.Router, tokens APITokenStore, audit tokenAuditor, auth AuthService) {
	if tokens == nil || audit == nil || auth == nil {
		return
	}
	minter, ok := auth.(TokenMinter)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Post("/embed/tokens", handleCreateEmbedToken(tokens, audit, minter))
	})
}

// handleCreateEmbedToken mints a read-only embed token. It enforces the
// read-only ceiling (400 for any write scope) before the ordinary
// per-user ceiling (403 via ValidateTokenScopes), so a caller who is
// themselves an admin still cannot mint a write-capable embed token (AC1).
func handleCreateEmbedToken(tokens APITokenStore, audit tokenAuditor, minter TokenMinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := minter.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req tokenCreateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTokenBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed embed token request body: "+err.Error())
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "name is required")
			return
		}
		if len(req.Scopes) == 0 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "at least one read-only scope is required")
			return
		}
		// Read-only ceiling first: an embed token carries no write surface,
		// regardless of the minting user's own capabilities.
		for _, s := range req.Scopes {
			if !embedReadOnlyScopes[s] {
				writeJSONError(w, http.StatusBadRequest, "validation_failed",
					"embed tokens are read-only; scope not permitted for an embed token: "+s)
				return
			}
		}

		// Ordinary per-user ceiling + vocabulary normalization (shared seam).
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

		auditTokenAction(r.Context(), audit, username, "token.create", t.ID, map[string]any{
			"name": t.Name, "scopes": scopes, "embed": true,
		})

		writeJSON(w, http.StatusCreated, embedTokenCreateResponse{
			tokenResponse: toTokenResponse(t, scopes),
			Token:         raw,
			Embed:         true,
		})
	}
}

// mountEmbedViewRoutes registers the top-level GET /embed/{view} shell
// routes (outside /api/v1, like the SPA fallback and /api/ws — these serve
// HTML, not the JSON API). Nil-safe: a nil distFS or a tokens store that
// cannot look up by hash skips mounting the whole family. postureAvailable
// reflects whether T-1607's posture service is wired (opts.Posture != nil):
// when false, /embed/posture serves the documented "wired but dark" state
// instead of the shell (AC5). frameAncestors is [server]
// embed_frame_ancestors, validated at startup (internal/config) — the
// origins allowed to iframe these views beyond same-origin (T-2901).
func mountEmbedViewRoutes(r chi.Router, tokens APITokenStore, distFS fs.FS, postureAvailable bool, frameAncestors []string) {
	if tokens == nil || distFS == nil {
		return
	}
	reader, ok := tokens.(embedTokenReader)
	if !ok {
		return
	}
	shell := newSPAHandler(distFS)
	r.Group(func(r chi.Router) {
		r.Use(embedFrameHeadersMiddleware(frameAncestors))
		r.Use(embedViewAuth(reader))
		r.Get("/embed/{view}", handleEmbedView(shell, postureAvailable))
	})
}

// embedFrameHeadersMiddleware relaxes the two anti-framing headers for the
// /embed/* views only (T-2901). The router-wide securityHeadersMiddleware
// has already set `frame-ancestors 'none'` and `X-Frame-Options: DENY` by
// the time this group middleware runs — correct for the app, which is never
// framed, and exactly wrong for these three views, which exist to be
// iframed into wikis / NOC screens / status pages (docs/security.md's embed
// section). This middleware overwrites the CSP with the same policy
// (middleware.go's cspPolicy — no other directive changes) carrying
// `frame-ancestors 'self' [origins...]` and removes X-Frame-Options: that
// header cannot express an origin list, frame-ancestors supersedes it in
// every browser that supports CSP2, and leaving DENY in place would forbid
// precisely the embedding these routes are for.
//
// origins is [server] embed_frame_ancestors, already validated as
// scheme://host[:port] origins at startup (internal/config). Empty means
// same-origin embedding only — the operator opts into external origins,
// never gets them by default.
func embedFrameHeadersMiddleware(origins []string) func(http.Handler) http.Handler {
	sources := "'self'"
	for _, o := range origins {
		sources += " " + o
	}
	embedCSP := cspPolicy(sources)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", embedCSP)
			h.Del("X-Frame-Options")
			next.ServeHTTP(w, r)
		})
	}
}

// embedViewAuth is the embed routes' dedicated authentication middleware —
// a distinct path from SessionMiddleware. It reads the token only from the
// `?token=` query parameter (an <iframe> cannot set an Authorization header
// or carry a first-party session cookie into a cross-site embed), never from
// the session cookie, so an embed route can never be satisfied by a browser
// session in place of an embed token (AC6). It rejects a missing/unknown/
// revoked token (401) and a token that carries any non-read-only scope (403
// — a write-capable automation token is not an embed token).
func embedViewAuth(reader embedTokenReader) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.URL.Query().Get("token")
			if raw == "" {
				writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "missing embed token")
				return
			}
			rec, err := reader.GetByHash(r.Context(), hashEmbedToken(raw))
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "invalid embed token")
				return
			}
			if rec.RevokedAt.Valid {
				writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "embed token revoked")
				return
			}
			var scopeNames []string
			if err := json.Unmarshal([]byte(rec.ScopesJSON), &scopeNames); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not decode token scopes")
				return
			}
			for _, s := range scopeNames {
				if !embedReadOnlyScopes[s] {
					// A token carrying a write scope is not an embed token,
					// even if presented at an embed route.
					writeJSONError(w, http.StatusForbidden, "forbidden", "token is not a read-only embed token")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// embedPostureDarkMarker is the documented body the "wired but dark"
// /embed/posture state serves (AC5) when T-1607's posture service is not
// wired. It is a self-contained HTML fragment carrying a stable
// data-embed-state attribute callers/tests can assert on, rather than a
// broken shell that would try (and fail) to fetch a posture score.
const embedPostureDarkMarker = "posture-unavailable"

// handleEmbedView serves the SPA shell for a valid, authenticated embed
// view — the React app boots at /embed/{view} and renders the matching
// read-only view. An unknown view is a 404; /embed/posture is served dark
// when posture scoring is not available on this instance.
func handleEmbedView(shell http.Handler, postureAvailable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view := chi.URLParam(r, "view")
		if !embedViews[view] {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such embed view")
			return
		}
		if view == "posture" && !postureAvailable {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<!doctype html><html><body>` +
				`<div data-embed-state="` + embedPostureDarkMarker + `">` +
				`Network posture scoring is not available on this instance.</div>` +
				`</body></html>`))
			return
		}
		shell.ServeHTTP(w, r)
	}
}
