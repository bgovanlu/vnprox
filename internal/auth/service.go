package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Default session lifetimes per docs/security.md "Authentication": "vnprox
// sessions idle out at 2h, hard cap 12h."
const (
	DefaultIdleTimeout = 2 * time.Hour
	DefaultHardTimeout = 12 * time.Hour

	// DefaultCapRefreshInterval is how often a live session's capabilities
	// are re-derived from PVE, per docs/security.md "re-derived hourly".
	DefaultCapRefreshInterval = time.Hour

	// SessionCookieName and CSRFCookieName are docs/api.md's documented
	// cookie names ("session cookie vnprox_session ... + X-VNPROX-CSRF
	// header") and the double-submit cookie name web/src/api/auth.ts (T-005)
	// already assumes.
	SessionCookieName = "vnprox_session"
	CSRFCookieName    = "vnprox_csrf"
	// CSRFHeaderName is the header mutating requests must echo the CSRF
	// cookie's value back on, per docs/api.md's conventions section.
	CSRFHeaderName = "X-VNPROX-CSRF"

	sessionIDBytes = 32 // 256 bits, per docs/security.md.
)

// Config configures a Service. All durations/limits default sensibly when
// zero; tests override them to exercise renewal/expiry/rate-limiting on a
// short, fast timescale (T-105 acceptance criteria 4 and 5).
type Config struct {
	Sessions                 *store.SessionRepo
	Audit                    *store.AuditRepo
	NewIdentity              IdentityFactory
	Now                      func() time.Time
	Logger                   *slog.Logger
	RateLimit                RateLimitConfig
	IdleTimeout              time.Duration
	HardTimeout              time.Duration
	CapRefreshInterval       time.Duration
	TicketRenewCheckInterval time.Duration
}

// Service implements the login/session/CSRF/capability machinery described
// by T-105's task card: PVE ticket bridge, encrypted session storage,
// double-submit CSRF, capability derivation, login rate limiting, and the
// middleware/route registrations other tasks build on. See doc.go for the
// pvemock fidelity gaps its own tests work around.
type Service struct {
	sessions      *store.SessionRepo
	audit         *store.AuditRepo
	newIdentity   IdentityFactory
	limiter       *loginLimiter
	now           func() time.Time
	log           *slog.Logger
	live          map[string]*liveSession
	idleTimeout   time.Duration
	hardTimeout   time.Duration
	capRefresh    time.Duration
	renewInterval time.Duration
	mu            sync.Mutex
}

// liveSession is the in-memory-only counterpart to a persisted
// store.Session: the PVEIdentity (holding the user's plaintext password,
// needed for ticket renewal — see internal/pve/auth.go) that only exists
// for the process lifetime of the login that created it. See doc.go for
// what this means across a daemon restart.
type liveSession struct {
	identity       PVEIdentity
	lastCapRefresh time.Time
}

// NewService constructs a Service. Sessions, Audit, and NewIdentity are
// required; everything else defaults per the constants above.
func NewService(cfg Config) (*Service, error) {
	if cfg.Sessions == nil {
		return nil, fmt.Errorf("auth: Config.Sessions is required")
	}
	if cfg.Audit == nil {
		return nil, fmt.Errorf("auth: Config.Audit is required")
	}
	if cfg.NewIdentity == nil {
		return nil, fmt.Errorf("auth: Config.NewIdentity is required")
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	hard := cfg.HardTimeout
	if hard <= 0 {
		hard = DefaultHardTimeout
	}
	capRefresh := cfg.CapRefreshInterval
	if capRefresh <= 0 {
		capRefresh = DefaultCapRefreshInterval
	}
	renewInterval := cfg.TicketRenewCheckInterval
	if renewInterval <= 0 {
		renewInterval = time.Minute
	}

	return &Service{
		sessions:      cfg.Sessions,
		audit:         cfg.Audit,
		newIdentity:   cfg.NewIdentity,
		idleTimeout:   idle,
		hardTimeout:   hard,
		capRefresh:    capRefresh,
		renewInterval: renewInterval,
		limiter:       newLoginLimiter(cfg.RateLimit, now),
		now:           now,
		log:           logger,
		live:          make(map[string]*liveSession),
	}, nil
}

// newSessionID generates a random 256-bit session id, base64url-encoded
// (docs/security.md: "random 256-bit id").
func newSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generating session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Identity is the safe-to-expose (no PVE ticket/CSRF secret) subset of an
// authenticated session, attached to each request's context by
// SessionMiddleware. Handlers in this package and in later route
// registrations (T-106 and beyond) read it via IdentityFromContext.
type Identity struct {
	Caps      map[string]Capabilities
	SessionID string
	Username  string
	Realm     string
}

// HasCap reports whether node (or, if node has no specific entry, any node
// in the map — see caps.go's BuildCapabilities empty-nodes fallback) grants
// cap. Passing an empty node checks the single cluster-wide fallback entry
// (key "") if that's all the map has, or otherwise requires ANY node to
// grant it — appropriate for cluster-wide reads/writes that aren't scoped
// to one node.
func (id Identity) HasCap(node string, cap Cap) bool {
	if node != "" {
		if c, ok := id.Caps[node]; ok {
			return c.Has(cap)
		}
		// No entry for this specific node (e.g. the cluster-wide fallback
		// case) — fall through to "any node" below.
	}
	for _, c := range id.Caps {
		if c.Has(cap) {
			return true
		}
	}
	return false
}

type contextKey int

const (
	sessionContextKey contextKey = iota
)

// sessionRecord bundles what request-scoped middleware needs internally:
// the safe-to-expose Identity plus the CSRF secret CSRFMiddleware compares
// against. It is never exported directly — CSRFTokenFromContext exists
// instead for the one legitimate external need (T-005's frontend already
// expects a readable vnprox_csrf cookie, which this package sets at login;
// nothing downstream should need the value again after that).
type sessionRecord struct {
	Identity  Identity
	CSRFToken string
}

func contextWithSession(ctx context.Context, rec sessionRecord) context.Context {
	return context.WithValue(ctx, sessionContextKey, rec)
}

func sessionFromContext(ctx context.Context) (sessionRecord, bool) {
	rec, ok := ctx.Value(sessionContextKey).(sessionRecord)
	return rec, ok
}

// IdentityFromContext returns the authenticated Identity attached by
// SessionMiddleware, for handlers registered behind it (this package's own
// /auth/logout and /auth/me, and later tasks' capability-gated routes).
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	rec, ok := sessionFromContext(ctx)
	if !ok {
		return Identity{}, false
	}
	return rec.Identity, true
}

// PVEClientFor returns the live *pve.Client backing sessionID, for
// downstream tasks (T-106 topology, and eventually the change-engine) that
// need to make PVE API calls under the logged-in user's own ticket
// (docs/security.md: "all PVE API writes use the user's own ticket"). ok is
// false if no such client is alive in this process — either the session id
// is unknown/expired, or (see doc.go) the daemon restarted since that
// session was created, since the plaintext password ticket renewal needs
// is never persisted.
func (s *Service) PVEClientFor(sessionID string) (*pve.Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live, ok := s.live[sessionID]
	if !ok {
		return nil, false
	}
	ci, ok := live.identity.(clientIdentity)
	if !ok {
		return nil, false
	}
	return ci.c, true
}

func capsJSON(caps map[string]Capabilities) (string, error) {
	b, err := json.Marshal(caps)
	if err != nil {
		return "", fmt.Errorf("auth: marshaling capabilities: %w", err)
	}
	return string(b), nil
}

func decodeCaps(capsJSON string) (map[string]Capabilities, error) {
	var caps map[string]Capabilities
	if err := json.Unmarshal([]byte(capsJSON), &caps); err != nil {
		return nil, fmt.Errorf("auth: decoding stored capabilities: %w", err)
	}
	return caps, nil
}
