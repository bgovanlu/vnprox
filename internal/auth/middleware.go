package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// bearerAuthPrefix is the documented `Authorization: Bearer <token>`
// scheme (docs/api.md's Tokens section) SessionMiddleware checks for
// before falling back to the cookie-session path.
const bearerAuthPrefix = "Bearer "

// bearerToken extracts the raw token value from r's Authorization header,
// if present. A request with no such header (or a scheme other than
// "Bearer") is not a bearer-auth attempt at all — SessionMiddleware falls
// through to its existing cookie logic unchanged in that case, exactly as
// if this function didn't exist, so every non-automation caller (the SPA)
// is entirely unaffected by T-1104.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerAuthPrefix) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, bearerAuthPrefix))
	if tok == "" {
		return "", false
	}
	return tok, true
}

// SessionMiddleware resolves either an `Authorization: Bearer <token>`
// header (T-1104's automation-token path, checked first) or the
// vnprox_session cookie (the original T-105 path) against the store,
// attaching the resolved Identity (+ CSRF secret, cookie path only) to the
// request context for downstream handlers — both paths converge on the
// exact same context shape (contextWithSession/sessionRecord), so every
// RequireCap-gated handler written against IdentityFromContext works
// unchanged regardless of which path authenticated the request (docs/
// api.md's Tokens section: "converging on the same capability-derived
// request context"). Missing/invalid/expired/revoked credentials of either
// kind get a 401 "not_authenticated" and next is never called.
//
// Cookie-session lookups additionally enforce idle + hard expiry
// (docs/security.md: "vnprox sessions idle out at 2h, hard cap 12h") and
// slide the idle timeout forward on every successful lookup, per the same
// docs/security.md rule — bearer tokens have no such sliding-idle concept
// (they expire only via explicit DELETE /tokens/{id} revocation).
func (s *Service) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, ok := bearerToken(r); ok {
			s.authenticateBearer(w, r, next, raw)
			return
		}
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil || cookie.Value == "" {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in", nil)
			return
		}

		rec, err := s.sessions.Get(r.Context(), cookie.Value)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Error("auth: looking up session", "error", err)
			}
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in", nil)
			return
		}

		now := s.now()
		hardDeadline := now.Unix() - rec.CreatedAt
		if now.Unix() > rec.ExpiresAt || hardDeadline > int64(s.hardTimeout.Seconds()) {
			// Idle or hard timeout elapsed: invalidate cleanly (delete,
			// don't just ignore) so a stale cookie can never be replayed
			// back into validity by a clock/skew quirk.
			_ = s.sessions.Delete(r.Context(), rec.ID)
			s.mu.Lock()
			delete(s.live, rec.ID)
			s.mu.Unlock()
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "session expired", nil)
			return
		}

		newExpiry := now.Add(s.idleTimeout).Unix()
		if hardCap := rec.CreatedAt + int64(s.hardTimeout.Seconds()); newExpiry > hardCap {
			newExpiry = hardCap
		}
		if newExpiry != rec.ExpiresAt {
			rec.ExpiresAt = newExpiry
			if updateErr := s.sessions.Update(r.Context(), rec); updateErr != nil {
				s.log.Error("auth: sliding session expiry", "session_id", logSessionID(rec.ID), "error", updateErr)
			}
		}

		caps, err := decodeCaps(rec.CapsJSON)
		if err != nil {
			s.log.Error("auth: decoding session capabilities", "session_id", logSessionID(rec.ID), "error", err)
			caps = map[string]Capabilities{}
		}

		sr := sessionRecord{
			Identity: Identity{
				SessionID: rec.ID,
				Username:  rec.Username,
				Realm:     rec.Realm,
				Caps:      caps,
			},
			CSRFToken: rec.CSRFToken,
		}
		next.ServeHTTP(w, r.WithContext(contextWithSession(r.Context(), sr)))
	})
}

// authenticateBearer implements the T-1104 half of SessionMiddleware:
// looks raw up in the api_tokens table by its hash, rejects a missing/
// revoked token, enforces the per-token rate limit, then builds the exact
// same sessionRecord/Identity shape the cookie path builds (Bearer: true,
// no CSRF secret since there is nothing to double-submit) so every
// downstream RequireCap/handler is unaware which path authenticated the
// request. A successful authentication stamps last_used_at and appends a
// "token.use" audit entry (docs/api.md's Tokens section: "audited per
// use") — both best-effort (logged, never failing the request) exactly
// like every other non-critical side effect in this package.
func (s *Service) authenticateBearer(w http.ResponseWriter, r *http.Request, next http.Handler, raw string) {
	if s.tokens == nil {
		writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "bearer tokens are not enabled", nil)
		return
	}

	rec, err := s.tokens.GetByHash(r.Context(), HashAPIToken(raw))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("auth: looking up bearer token", "error", err)
		}
		writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "invalid bearer token", nil)
		return
	}
	if rec.RevokedAt.Valid {
		writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "token revoked", nil)
		return
	}
	if !s.bearerLimiter.allow(rec.ID) {
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many requests for this token", nil)
		return
	}

	var scopeNames []string
	if err := json.Unmarshal([]byte(rec.ScopesJSON), &scopeNames); err != nil {
		s.log.Error("auth: decoding token scopes", "token_id", rec.ID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not decode token scopes", nil)
		return
	}
	scopes := make([]Cap, len(scopeNames))
	for i, sn := range scopeNames {
		scopes[i] = Cap(sn)
	}

	now := s.now()
	if err := s.tokens.UpdateLastUsed(r.Context(), rec.ID, now.Unix()); err != nil {
		s.log.Warn("auth: updating token last_used_at", "token_id", rec.ID, "error", err)
	}
	s.appendAudit(r.Context(), rec.CreatedBy, "token.use", "allowed", clientIP(r), map[string]any{
		"tokenId": rec.ID, "path": r.URL.Path, "method": r.Method,
	})

	sr := sessionRecord{
		Identity: Identity{
			SessionID: "token:" + rec.ID,
			Username:  rec.CreatedBy,
			TokenID:   rec.ID,
			Caps:      map[string]Capabilities{"": CapabilitiesFromScopes(scopes)},
		},
		Bearer: true,
	}
	next.ServeHTTP(w, r.WithContext(contextWithSession(r.Context(), sr)))
}

// mutatingMethods are the HTTP methods docs/api.md's CSRF rule applies to
// ("mutating requests"): everything except GET/HEAD.
func isMutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// CSRFMiddleware enforces the double-submit pattern for mutating requests:
// X-VNPROX-CSRF must match the session's stored CSRF token exactly
// (docs/api.md, docs/security.md). Must run after SessionMiddleware (it
// reads the session from context). Non-mutating requests (GET/HEAD) pass
// through unchecked, and so does any bearer-token-authenticated request
// regardless of method (docs/api.md's Tokens section: "bearer skips CSRF
// (not cookie-based)" — there is no cookie for a header-carried credential
// to be tricked into resubmitting, the same reasoning docs/security.md's
// metrics scrape token precedent already applies).
func (s *Service) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutating(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		rec, ok := sessionFromContext(r.Context())
		if !ok {
			// SessionMiddleware already rejected unauthenticated requests;
			// this is defense in depth for a misordered middleware chain.
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in", nil)
			return
		}
		if rec.Bearer {
			next.ServeHTTP(w, r)
			return
		}

		got := r.Header.Get(CSRFHeaderName)
		if got == "" || got != rec.CSRFToken {
			writeJSONError(w, http.StatusForbidden, "csrf_required", "missing or invalid "+CSRFHeaderName+" header", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCap returns middleware that 403s unless the current session's
// capability set grants cap. Must run after SessionMiddleware. If the
// route has a chi URL parameter named "node", the check is scoped to that
// node's capability entry; otherwise it passes if ANY node in the user's
// capability map grants cap (see Identity.HasCap) — appropriate for
// cluster-wide routes that aggregate across nodes and rely on PVE's own
// per-call ACL enforcement (docs/security.md's primary layer) for the
// fine-grained cut.
//
// This is the pattern later route registrations (T-106 topology, and
// eventually the change-engine) should use:
//
//	r.With(authSvc.SessionMiddleware, authSvc.CSRFMiddleware, authSvc.RequireCap(auth.CapNetWrite)).
//		Put("/nodes/{node}/network/{iface}", handler)
func (s *Service) RequireCap(cap Cap) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := IdentityFromContext(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in", nil)
				return
			}
			node := chi.URLParam(r, "node")
			if !id.HasCap(node, cap) {
				writeJSONError(w, http.StatusForbidden, "forbidden", "missing required capability: "+string(cap), nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
