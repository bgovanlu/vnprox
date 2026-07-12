package api

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// requestLoggerMiddleware logs one structured slog line per request:
// method, path, status, duration, request id, and remote addr.
func requestLoggerMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
				"remote_addr", r.RemoteAddr,
			)
		})
	}
}

// recovererMiddleware recovers panics in handlers, logs them with a stack
// trace, and returns a 500 instead of crashing the daemon. Middleware order
// in NewRouter puts this after the logger so the eventual "http request"
// log line still fires (with a 500 status) even when a handler panics.
func recovererMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"panic", rec,
						"path", r.URL.Path,
						"request_id", middleware.GetReqID(r.Context()),
						"stack", string(debug.Stack()),
					)
					writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeadersMiddleware sets the headers mandated by docs/security.md
// "Transport": HSTS, a strict self-only/no-inline-script CSP, and the
// standard clickjacking/MIME-sniffing hardening headers.
//
// connect-src is 'self' only: modern browsers match same-origin wss:// under
// 'self' (the scheme-upgrade rule in CSP3), so the same-origin WebSocket
// (/api/ws, architecture.md §9) needs no scheme sources — and a bare wss:/ws:
// would allow connections to arbitrary hosts, contradicting docs/security.md's
// "WS to self".
//
// T-604 (security hardening pass) tightened this further, adding five
// directives the original policy left at the (safe, since default-src
// 'self' already covers them) implicit default rather than pinning
// explicitly: object-src/frame-src/worker-src/manifest-src 'none' (the SPA
// has no <object>/<embed>, no iframes, no Worker()/service worker — elkjs's
// layout runs via elk.bundled.js on the main thread, not a Worker — and no
// web app manifest, so all four attack surfaces are dead weight to leave
// open) and form-action 'self' (every mutation is a fetch() through
// TanStack Query per docs/architecture.md §8, never an HTML <form> submit,
// but pinning it stops an injected <form> from ever exfiltrating to a
// third-party action= origin). Verified against the full Playwright suite
// (web/e2e) with no regressions — see planning/reports/T-604.md.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; worker-src 'none'; manifest-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
