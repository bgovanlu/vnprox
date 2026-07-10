// Package api implements the HTTP router, handlers, and middleware stack
// for vnproxd: request id / structured logging / panic recovery / security
// headers, the /api/v1/health endpoint, and embedded-SPA serving with
// SPA-fallback routing. The WS hub and the rest of docs/api.md's routes
// land in later tasks (auth, changesets, topology, ...); this package only
// implements what T-002 requires.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// AuthService is the subset of *auth.Service the router needs: route
// registration for docs/api.md's Auth endpoints, plus the session/
// capability middleware later capability-gated route registrations (T-106
// topology, and eventually the change engine) wrap themselves in. Declared
// as an interface here (rather than importing internal/auth's concrete
// type) so this package's dependency on T-105's auth package stays a small
// seam — internal/api does not otherwise know or care how login/session/
// CSRF/capability-derivation works.
//
// RequireCap takes the capability's plain string name (its JSON field name,
// e.g. "netRead") rather than internal/auth's own Cap type, so this
// interface doesn't need to import that package's types either;
// cmd/vnproxd's wiring (see its authServiceAdapter) is what bridges the
// concrete *auth.Service (whose RequireCap takes auth.Cap) to this shape.
type AuthService interface {
	MountRoutes(r chi.Router)
	SessionMiddleware(next http.Handler) http.Handler
	RequireCap(cap string) func(http.Handler) http.Handler
}

// Options configures the router built by NewRouter.
type Options struct {
	DistFS      fs.FS
	Auth        AuthService
	Collectors  CollectorHealth
	Topology    TopologyService
	Layouts     LayoutStore
	Changesets  ChangesetService
	PVEGateways PVEGatewayProvider
	Protected   ProtectedService
	Logger      *slog.Logger
	Version     string
}

// NewRouter builds the vnproxd HTTP handler: the full middleware stack,
// /api/v1/* routes, and SPA-fallback static serving for everything else.
func NewRouter(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()

	r.Use(requestIDMiddleware)
	r.Use(requestLoggerMiddleware(logger))
	r.Use(recovererMiddleware(logger))
	r.Use(securityHeadersMiddleware)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthHandler(opts.Version, opts.Collectors))
		if opts.Auth != nil {
			opts.Auth.MountRoutes(r)
		}
		mountTopologyRoutes(r, opts.Topology, opts.Auth, opts.Collectors)
		mountLayoutsRoutes(r, opts.Layouts, opts.Auth)
		mountChangesetsRoutes(r, opts.Changesets, opts.Auth, opts.PVEGateways)
		mountProtectedRoutes(r, opts.Protected, opts.Auth)
	})

	// /api/ws is intentionally not under /api/v1 (docs/api.md's WebSocket
	// section documents it at the bare /api/ws path).
	mountWSRoute(r, opts.Topology, opts.Auth)

	// Unmatched /api/* routes get a JSON 404 (per docs/api.md's error
	// envelope), not the SPA fallback; everything else falls back to the
	// embedded SPA's index.html so client-side routing works on refresh.
	spa := newSPAHandler(opts.DistFS)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such API route")
			return
		}
		spa.ServeHTTP(w, req)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) {
			writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed on this route")
			return
		}
		spa.ServeHTTP(w, req)
	})

	return r
}

func isAPIPath(p string) bool {
	return len(p) >= 5 && p[:5] == "/api/"
}

// requestIDMiddleware assigns a request id (reusing an inbound
// X-Request-Id if the caller/proxy supplied one), stores it under chi's
// middleware.RequestIDKey so downstream code can use middleware.GetReqID,
// and — unlike chi's own RequestID middleware, which only populates the
// context — echoes it back as a response header so operators can correlate
// a client-visible id with structured logs.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(middleware.RequestIDHeader)
		if id == "" {
			id = fmt.Sprintf("vnproxd-%d-%d", time.Now().UnixNano(), middleware.NextRequestID())
		}
		w.Header().Set(middleware.RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
