package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// SessionMiddleware resolves the vnprox_session cookie against the store,
// enforcing idle + hard expiry (docs/security.md: "vnprox sessions idle
// out at 2h, hard cap 12h"), and attaches the resolved Identity + CSRF
// secret to the request context for downstream handlers (this package's
// own /auth/logout, /auth/me, and later capability-gated route
// registrations). Missing/invalid/expired sessions get a 401
// "not_authenticated" and next is never called.
//
// On every successful lookup it slides the idle timeout forward (bounded
// by the hard cap from CreatedAt), per the same docs/security.md rule.
func (s *Service) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				s.log.Error("auth: sliding session expiry", "session_id", rec.ID, "error", updateErr)
			}
		}

		caps, err := decodeCaps(rec.CapsJSON)
		if err != nil {
			s.log.Error("auth: decoding session capabilities", "session_id", rec.ID, "error", err)
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

// mutatingMethods are the HTTP methods docs/api.md's CSRF rule applies to
// ("mutating requests"): everything except GET/HEAD.
func isMutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// CSRFMiddleware enforces the double-submit pattern for mutating requests:
// X-VNPROX-CSRF must match the session's stored CSRF token exactly
// (docs/api.md, docs/security.md). Must run after SessionMiddleware (it
// reads the session from context). Non-mutating requests (GET/HEAD) pass
// through unchecked.
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
