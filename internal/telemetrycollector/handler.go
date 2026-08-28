// SPDX-License-Identifier: Apache-2.0

package telemetrycollector

// handler.go is the collector's HTTP surface: three routes plus a health
// check, sized to "an endpoint that accepts a small opt-in payload and
// stores it" (T-3710's own scoping) and nothing more.
//
// What makes it safe to expose, in the order a request meets them:
//  1. A hard body-size cap (http.MaxBytesReader) before anything is read.
//  2. A global, unkeyed rate limit — bounds total throughput without
//     reading anything about the request's source.
//  3. internal/telemetry.Guard, re-run server-side against the closed
//     schema and shape rules the client already applies, so this collector
//     never trusts a client's own Guard run. An unrecognised
//     payloadVersion is refused explicitly here (code
//     "unsupported_payload_version"), never guessed at.
//  4. A per-install rate limit, keyed on the payload's own InstallID —
//     the only "source" identifier this collector ever uses, because it is
//     the only one the payload carries.
//
// r.RemoteAddr is never read anywhere in this file. See doc.go.
import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/telemetry"
)

// Defaults for the two rate limiters and the body-size cap. All are
// deliberately generous for legitimate use (one `vnproxctl verify` run
// produces at most one submission a human would plausibly repeat a
// handful of times while debugging) and are configuration, not code —
// cmd/vnproxtelemetryd exposes flags for every one of them.
const (
	// DefaultMaxBodyBytes bounds one request body. internal/telemetry's
	// payloads are small fixed-shape JSON (payload_test.go's largest
	// fixture is under 4KB); this is generous headroom, not a tight fit.
	DefaultMaxBodyBytes = 64 * 1024
	// DefaultPerInstallCapacity/Refill: a burst of a dozen submissions,
	// refilling one every 5 minutes thereafter — enough for an operator
	// re-running `verify` repeatedly while chasing a failure.
	DefaultPerInstallCapacity = 12
	DefaultPerInstallRefill   = 5 * time.Minute
	// DefaultGlobalCapacity/Refill: a service-wide ceiling with no key at
	// all, so a flood of freshly-generated (but validly ULID-shaped)
	// install ids cannot bypass the per-install limiter by never reusing
	// one. 600 burst, refilling one token/second (~1 req/s steady state,
	// generous for an opt-in, low-volume collector; raise via flag for a
	// larger deployment).
	DefaultGlobalCapacity = 600
	DefaultGlobalRefill   = time.Second
)

// installIDPattern matches internal/telemetry/guard.go's ulidPattern
// exactly (Crockford base32, 26 chars). Duplicated rather than exported
// from internal/telemetry: that package's client surface is frozen for
// T-3710, and a regex this small is cheaper to keep in sync by inspection
// than to add an export for.
var installIDPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// Server is the collector's HTTP handler set.
type Server struct {
	store              *Store
	logger             *slog.Logger
	now                func() time.Time
	perInstall         *limiter
	global             *limiter
	maxBodyBytes       int64
	perInstallCapacity int
	perInstallRefill   time.Duration
	globalCapacity     int
	globalRefill       time.Duration
}

// Option configures a Server.
type Option func(*Server)

// WithLogger sets the structured logger. Defaults to a discarding one.
func WithLogger(l *slog.Logger) Option { return func(s *Server) { s.logger = l } }

// WithClock overrides time.Now, for tests and for ReceivedAt.
func WithClock(now func() time.Time) Option { return func(s *Server) { s.now = now } }

// WithMaxBodyBytes overrides DefaultMaxBodyBytes.
func WithMaxBodyBytes(n int64) Option { return func(s *Server) { s.maxBodyBytes = n } }

// WithPerInstallRateLimit overrides the per-install-id bucket.
func WithPerInstallRateLimit(capacity int, refillEvery time.Duration) Option {
	return func(s *Server) { s.perInstallCapacity, s.perInstallRefill = capacity, refillEvery }
}

// WithGlobalRateLimit overrides the service-wide bucket.
func WithGlobalRateLimit(capacity int, refillEvery time.Duration) Option {
	return func(s *Server) { s.globalCapacity, s.globalRefill = capacity, refillEvery }
}

// NewServer builds a Server backed by store. Call Router to get the
// http.Handler to serve.
func NewServer(store *Store, opts ...Option) *Server {
	s := &Server{
		store:              store,
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:                time.Now,
		maxBodyBytes:       DefaultMaxBodyBytes,
		perInstallCapacity: DefaultPerInstallCapacity,
		perInstallRefill:   DefaultPerInstallRefill,
		globalCapacity:     DefaultGlobalCapacity,
		globalRefill:       DefaultGlobalRefill,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.perInstall = newLimiter(s.perInstallCapacity, s.perInstallRefill, s.now)
	s.global = newLimiter(s.globalCapacity, s.globalRefill, s.now)
	return s
}

// Router builds the collector's http.Handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.recoverer)
	r.Get("/healthz", s.handleHealthz)
	r.Route("/v1", func(r chi.Router) {
		r.Post("/submissions", s.handleSubmit)
		r.Get("/summary", s.handleSummary)
		r.Delete("/installs/{installID}", s.handleDelete)
	})
	return r
}

// --- handlers ----------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != "" && ct != telemetry.ContentType {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			fmt.Sprintf("Content-Type must be %s", telemetry.ContentType))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", ErrBodyTooLarge.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "read_failed", "could not read the request body")
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "empty_body", ErrEmptyBody.Error())
		return
	}

	// Global, IP-free throughput ceiling — checked before any parsing, so
	// a flood pays only for a token-bucket lookup, not a JSON decode.
	if !s.global.allow("") {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "the collector is at capacity; try again shortly")
		return
	}

	// Re-guard server-side. This collector does not trust that the
	// client's own internal/telemetry.Guard ran, or that it is running
	// the same version of the closed schema this build enforces.
	if guardErr := telemetry.Guard(raw, nil); guardErr != nil {
		s.logGuardViolations(r, guardErr)
		code := "invalid_payload"
		if ge, ok := telemetry.AsGuardError(guardErr); ok {
			for _, v := range ge.Violations {
				if v.Rule == "payload-version" {
					// AC: "rejecting an unknown payloadVersion explicitly
					// rather than guessing" — a distinct code, not folded
					// into the generic invalid-payload response.
					code = "unsupported_payload_version"
					break
				}
			}
		}
		writeError(w, http.StatusBadRequest, code, "this payload does not match the documented telemetry schema")
		return
	}

	var payload telemetry.Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		// Guard has already parsed raw as a single JSON document; reaching
		// this means Guard and encoding/json disagree, which should not
		// happen. Fail closed regardless.
		writeError(w, http.StatusBadRequest, "invalid_payload", "could not decode the payload")
		return
	}

	if !s.perInstall.allow(payload.InstallID) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", ErrRateLimited.Error())
		return
	}

	receivedAt := s.now()
	if err := s.store.Insert(r.Context(), payload, receivedAt); err != nil {
		s.logger.Error("telemetrycollector: storing submission failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "store_failed", "could not store the submission")
		return
	}

	// Logged fields are all payload fields already designed to carry no
	// identity (payload.go), plus the collector's own clock — nothing new
	// enters the log line that was not already leaving the operator's
	// machine.
	s.logger.Info("telemetrycollector: submission stored",
		"installId", payload.InstallID, "pveVersion", payload.PVEVersion, "suite", payload.Suite)
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":     "stored",
		"receivedAt": receivedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.store.BuildSummary(r.Context(), s.now())
	if err != nil {
		s.logger.Error("telemetrycollector: building summary failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "summary_failed", "could not build the summary")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "installID")
	if !installIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid_install_id", ErrInvalidInstallID.Error())
		return
	}
	n, err := s.store.DeleteByInstallID(r.Context(), id)
	if err != nil {
		s.logger.Error("telemetrycollector: revocation delete failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "delete_failed", "could not delete submissions")
		return
	}
	s.logger.Info("telemetrycollector: revocation processed", "installId", id, "deleted", n)
	writeJSON(w, http.StatusOK, map[string]any{"installId": id, "deleted": n})
}

// logGuardViolations logs which classes of violation a rejected submission
// tripped — never the offending Violation.Sample text. Logging the sample
// would persist, in this collector's own logs, exactly the kind of
// identifying value the guard exists to keep out of the store.
func (s *Server) logGuardViolations(r *http.Request, guardErr error) {
	ge, ok := telemetry.AsGuardError(guardErr)
	if !ok {
		s.logger.Warn("telemetrycollector: rejected a submission", "method", r.Method, "path", r.URL.Path, "error", guardErr.Error())
		return
	}
	classes := ge.Classes()
	classStrs := make([]string, len(classes))
	for i, c := range classes {
		classStrs[i] = string(c)
	}
	s.logger.Warn("telemetrycollector: rejected a submission", "method", r.Method, "path", r.URL.Path, "violationClasses", classStrs)
}

// recoverer is a minimal panic recoverer. Unlike chi/middleware.Recoverer
// it logs no request metadata beyond method and path — kept local rather
// than pulled in for one middleware, so this file stays the single place
// that has to be audited for "does this ever log something about the
// caller".
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("telemetrycollector: panic handling request", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprintf("%v", rec))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- response helpers ----------------------------------------------------

// errorEnvelope mirrors docs/api.md's `{"error": {"code", "message"}}`
// shape for consistency with the rest of vnprox's HTTP surface, though
// this collector is a separate service and not itself part of that
// document's contract.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
