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

	// DefaultBearerRateLimitCapacity/DefaultBearerRateLimitRefill are
	// T-1104's per-token rate limit defaults: a burst of 60 requests, then
	// one token refilled per second (steady-state 1 req/s) — generous
	// enough for CI/automation polling loops while still bounding a
	// misbehaving or compromised token's blast radius, distinct from (and
	// independent of) DefaultRateLimitConfig's login-attempt limiter.
	DefaultBearerRateLimitCapacity = 60
	DefaultBearerRateLimitRefill   = 1 * time.Second

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
	Tokens      *store.APITokenRepo
	Audit       *store.AuditRepo
	NewIdentity IdentityFactory
	Now         func() time.Time
	Logger      *slog.Logger
	Sessions    *store.SessionRepo
	// OIDC is T-1207's optional OIDC SSO service. nil disables the
	// /auth/oidc/* routes entirely (a deployment with no [oidc] config), the
	// same nil-safe convention Tokens uses to disable bearer auth.
	OIDC                     *OIDCService
	RateLimit                RateLimitConfig
	BearerRateLimit          RateLimitConfig
	IdleTimeout              time.Duration
	TicketRenewCheckInterval time.Duration
	CapRefreshInterval       time.Duration
	HardTimeout              time.Duration
	ReadOnly                 bool
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
	tokens        *store.APITokenRepo
	bearerLimiter *tokenBucket
	tokenUse      *tokenUseAggregator
	oidc          *OIDCService
	now           func() time.Time
	log           *slog.Logger
	live          map[string]*liveSession
	idleTimeout   time.Duration
	hardTimeout   time.Duration
	capRefresh    time.Duration
	renewInterval time.Duration
	readOnly      bool
	mu            sync.Mutex
}

// liveSession is the in-memory-only counterpart to a persisted
// store.Session: the PVEIdentity (holding the live PVE ticket, and — only
// until the first ticket-as-password renewal succeeds — the user's
// plaintext password as a renewal fallback; see internal/pve/auth.go)
// that only exists for the process lifetime of the login that created it.
// See doc.go for what this means across a daemon restart.
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
	bearerLimit := cfg.BearerRateLimit
	if bearerLimit.Capacity <= 0 {
		bearerLimit.Capacity = DefaultBearerRateLimitCapacity
	}
	if bearerLimit.RefillEvery <= 0 {
		bearerLimit.RefillEvery = DefaultBearerRateLimitRefill
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
		tokens:        cfg.Tokens,
		bearerLimiter: newTokenBucket(bearerLimit, now),
		tokenUse:      newTokenUseAggregator(),
		oidc:          cfg.OIDC,
		now:           now,
		log:           logger,
		live:          make(map[string]*liveSession),
		readOnly:      cfg.ReadOnly,
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

// redactedIDPrefixLen is how much of a session id logSessionID exposes.
const redactedIDPrefixLen = 8

// logSessionID returns a truncated, log-safe stand-in for a session id: the
// id itself *is* the bearer credential presented in the vnprox_session
// cookie (docs/security.md: "random 256-bit id in an HttpOnly; Secure;
// SameSite=Strict cookie"), so logging it verbatim would hand anyone with
// read access to the daemon's logs (a materially weaker privilege than
// shell/root access to the host — e.g. a centralized log aggregator, or
// `journalctl` group membership) everything needed to directly hijack that
// session, bypassing HttpOnly/Secure/SameSite entirely (T-604 security
// hardening pass; those cookie flags only defend against browser-side/
// network attackers, never a log reader).
//
// Truncating to the first redactedIDPrefixLen base64url characters keeps
// enough of the id for operators to correlate multiple log lines about the
// "same session" (the practical reason every call site below logs the id
// at all) while discarding the ~250 bits of remaining entropy that would
// make the value replayable — i.e. this is a correlation handle, not a
// secret, once truncated this short.
func logSessionID(id string) string {
	if len(id) <= redactedIDPrefixLen {
		return id
	}
	return id[:redactedIDPrefixLen] + "…"
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
	// TokenID is set (to the minting api_tokens.id) only for a bearer-
	// token-authenticated request's Identity; empty for a cookie-session
	// Identity. Consumers (WS "events" subscription gating, force-close on
	// revoke — internal/topology.Hub) use it to correlate a live
	// connection back to the token that authenticated it, without needing
	// the whole Identity/Capabilities machinery threaded through.
	TokenID string
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
	// Bearer marks this request as authenticated via T-1104's bearer-token
	// middleware rather than the cookie-session path — CSRFMiddleware uses
	// it to skip the double-submit check (docs/api.md's Tokens section:
	// "bearer skips CSRF (not cookie-based)"), the same precedent
	// docs/security.md's metrics scrape token already sets for a
	// non-cookie credential.
	Bearer bool
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

// ContextWithIdentity attaches id to ctx in the same place SessionMiddleware
// does (IdentityFromContext(ContextWithIdentity(ctx, id)) always round-
// trips id back out). Exported for the rare composition-root/test need to
// simulate an authenticated request context without a full HTTP round
// trip through this package's own middleware — e.g.
// internal/topology.Hub's ServeWS (T-1104's WS "events" automation-scope
// gating) needs a real auth.Identity in r.Context() ahead of the upgrade,
// and its own tests construct one directly via this function rather than
// standing up a whole login flow.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return contextWithSession(ctx, sessionRecord{Identity: id})
}

// PVEClientFor returns the live *pve.Client backing sessionID, for
// downstream tasks (T-106 topology, and eventually the change-engine) that
// need to make PVE API calls under the logged-in user's own ticket
// (docs/security.md: "all PVE API writes use the user's own ticket"). ok is
// false if no such client is alive in this process — either the session id
// is unknown/expired, or the daemon restarted since that session was
// created (live *pve.Client state — renewal credentials included — is
// memory-only, never persisted).
func (s *Service) PVEClientFor(sessionID string) (*pve.Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live, ok := s.live[sessionID]
	if !ok {
		return nil, false
	}
	if ci, isClient := live.identity.(clientIdentity); isClient {
		return ci.c, true
	}
	// T-1207: an OIDC session has no PVE ticket of its own — cluster-scoped
	// PVE calls go out on the mapped PVE identity its groups linked to.
	if oid, isOIDC := live.identity.(*oidcIdentity); isOIDC {
		return oid.linkedClient()
	}
	return nil, false
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
