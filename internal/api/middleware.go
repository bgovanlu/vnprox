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

// cspPolicy renders the daemon's one CSP with the given frame-ancestors
// source list — the single directive that differs between the app (never
// framed: 'none', set by securityHeadersMiddleware below) and the /embed/*
// views (framed on purpose: 'self' plus [server] embed_frame_ancestors, set
// by embed.go's embedFrameHeadersMiddleware). Every other directive is
// shared by construction, so the embed relaxation can never silently drift
// the rest of the policy.
func cspPolicy(frameAncestors string) string {
	return "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; worker-src 'self'; manifest-src 'self'; form-action 'self'; frame-ancestors " + frameAncestors + "; base-uri 'self'"
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
// The fetch directives beyond default-src are pinned explicitly (T-604)
// rather than left at the implicit default-src 'self' value, each set to
// the minimum the SPA actually uses:
//
//   - object-src/frame-src 'none': the SPA renders no <object>/<embed> and
//     no iframes, so both surfaces stay closed. (elkjs's layout runs via
//     elk.bundled.js on the main thread, not inside a frame or plugin.)
//   - worker-src/manifest-src 'self': T-2005's installable PWA registers
//     web/public/sw.js as a service worker on every load and links
//     web/public/manifest.webmanifest — both strictly same-origin, so 'self'
//     is exact. (T-604 originally pinned both 'none', which was correct
//     before T-2005 and wrong after it: a production browser refused the
//     service worker and the manifest outright, so the PWA could neither
//     install nor receive push. Fixed by T-2901.)
//   - form-action 'self': every mutation is a fetch() through TanStack
//     Query per docs/architecture.md §8, never an HTML <form> submit, but
//     pinning it stops an injected <form> from ever exfiltrating to a
//     third-party action= origin.
//   - frame-ancestors 'none' (+ X-Frame-Options: DENY, its legacy
//     equivalent): the app itself is never framed. The three /embed/* views
//     exist to be framed and get a per-route relaxation instead — see
//     embed.go's embedFrameHeadersMiddleware, driven by
//     [server] embed_frame_ancestors.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	appCSP := cspPolicy("'none'")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		h.Set("Content-Security-Policy", appCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
