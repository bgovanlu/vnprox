package pvemock

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// session is one authenticated PVE ticket, mirroring what real pveproxy
// tracks server-side against a ticket: the user and their privilege set.
type session struct {
	IssuedAt time.Time
	Ticket   string
	CSRF     string
	UserID   string
	Privs    []string
}

func (s session) hasPrivilege(priv string) bool {
	for _, p := range s.Privs {
		if p == "*" || p == priv {
			return true
		}
	}
	return false
}

type sessionStore struct {
	byTicket map[string]*session
	now      func() time.Time
	// ttl bounds a ticket's server-side validity (real PVE: 2h). Zero
	// means "never expires" — the default, preserving pre-TTL behavior
	// for every existing test.
	ttl time.Duration
	mu  sync.RWMutex
}

func newSessionStore(now func() time.Time) *sessionStore {
	if now == nil {
		now = time.Now
	}
	return &sessionStore{byTicket: make(map[string]*session), now: now}
}

func (s *sessionStore) create(userID string, privs []string) *session {
	sess := &session{
		Ticket:   "PVE:" + userID + ":" + randHex(8),
		CSRF:     randHex(16),
		UserID:   userID,
		Privs:    append([]string(nil), privs...),
		IssuedAt: s.now(),
	}
	s.mu.Lock()
	s.byTicket[sess.Ticket] = sess
	s.mu.Unlock()
	return sess
}

// lookup resolves a ticket to its session, treating an expired ticket
// (per ttl) exactly like an unknown one — real PVE rejects both with 401
// without distinguishing them.
func (s *sessionStore) lookup(ticket string) (*session, bool) {
	s.mu.RLock()
	sess, ok := s.byTicket[ticket]
	ttl := s.ttl
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if ttl > 0 && s.now().Sub(sess.IssuedAt) > ttl {
		s.mu.Lock()
		delete(s.byTicket, ticket)
		s.mu.Unlock()
		return nil, false
	}
	return sess, true
}

func (s *sessionStore) setTTL(ttl time.Duration) {
	s.mu.Lock()
	s.ttl = ttl
	s.mu.Unlock()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read on the standard reader does not fail in
		// practice; if it ever does, a predictable-but-unique fallback
		// keeps the mock server usable rather than panicking mid-test.
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ticketRequest is the POST /access/ticket request body (PVE accepts both
// form-encoded and JSON; the mock accepts JSON and form).
type ticketRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Realm    string `json:"realm"`
	OTP      string `json:"otp"`
}

func (srv *Server) handleTicket(w http.ResponseWriter, r *http.Request) {
	var req ticketRequest
	if err := decodeRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	username := req.Username
	if req.Realm != "" && !containsAt(username) {
		username = username + "@" + req.Realm
	}

	user, ok := srv.findUser(username)
	if !ok || !srv.passwordAccepted(user, req.Password) {
		// Real PVE returns 401 with an empty data payload on bad
		// credentials (never distinguishing "no such user" from "wrong
		// password").
		writeError(w, http.StatusUnauthorized, "authentication failure")
		return
	}

	// TOTP: a user with a fixture-declared static code requires the exact
	// "otp" value on login. Real PVE's modern flow is a two-step
	// ticket-challenge (login returns a NeedTFA half-ticket, the client
	// answers with the code); the mock implements the simple single-step
	// variant — missing/wrong otp is a plain 401 — which is what vnprox's
	// single-shot OTP passthrough (docs/security.md) actually exercises.
	// The full challenge flow needs validation against real PVE.
	if user.TOTP != "" && req.OTP != user.TOTP {
		writeError(w, http.StatusUnauthorized, "authentication failure: missing or invalid second factor")
		return
	}

	sess := srv.state.sessions.create(user.UserID, user.Privileges)
	http.SetCookie(w, &http.Cookie{Name: "PVEAuthCookie", Value: sess.Ticket, Path: "/"})
	writeData(w, http.StatusOK, map[string]any{
		"ticket":              sess.Ticket,
		"CSRFPreventionToken": sess.CSRF,
		"username":            sess.UserID,
	})
}

// passwordAccepted implements the two password forms real PVE accepts on
// POST /access/ticket: the user's actual password, or a still-valid,
// previously issued ticket belonging to that same user ("ticket as
// password" — how PVE clients renew a ticket without retaining the
// plaintext password). An expired or foreign ticket is rejected like any
// wrong password.
func (srv *Server) passwordAccepted(user UserSpec, password string) bool {
	if password == "" {
		return false
	}
	if password == user.Password {
		return true
	}
	if strings.HasPrefix(password, "PVE:") {
		if sess, ok := srv.state.sessions.lookup(password); ok && sess.UserID == user.UserID {
			return true
		}
	}
	return false
}

func containsAt(s string) bool {
	for _, c := range s {
		if c == '@' {
			return true
		}
	}
	return false
}

func (srv *Server) findUser(userID string) (UserSpec, bool) {
	for _, u := range srv.state.fixture.Users {
		if u.UserID == userID {
			return u, true
		}
	}
	return UserSpec{}, false
}

// authenticate extracts the session from a request the way real pveproxy
// does, supporting both auth modes vnprox's client uses:
//
//   - API token: an "Authorization: PVEAPIToken=user@realm!tokenid=secret"
//     header, checked against the fixture users' declared tokens. Token
//     requests carry no cookie and need no CSRF header (matching real
//     PVE, where CSRF protection only applies to cookie-based sessions).
//   - Ticket: the PVEAuthCookie cookie, plus a matching
//     CSRFPreventionToken header on mutating methods.
func (srv *Server) authenticate(r *http.Request) (*session, error) {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "PVEAPIToken=") {
		return srv.authenticateToken(strings.TrimPrefix(auth, "PVEAPIToken="))
	}

	cookie, err := r.Cookie("PVEAuthCookie")
	if err != nil || cookie.Value == "" {
		return nil, fmt.Errorf("%w: missing PVEAuthCookie", ErrAuthFailed)
	}
	sess, ok := srv.state.sessions.lookup(cookie.Value)
	if !ok {
		return nil, fmt.Errorf("%w: unknown or expired ticket", ErrAuthFailed)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Header.Get("CSRFPreventionToken") != sess.CSRF {
			return nil, fmt.Errorf("%w: CSRF token mismatch", ErrAuthFailed)
		}
	}
	return sess, nil
}

// authenticateToken resolves a "user@realm!tokenid=secret" API-token value
// to an ephemeral session carrying the owning user's privileges. Real
// PVE's token privilege separation (a token restricted to a subset of its
// owner's privileges) is out of scope for the mock — see TokenSpec.
func (srv *Server) authenticateToken(value string) (*session, error) {
	userID, rest, ok := strings.Cut(value, "!")
	if !ok {
		return nil, fmt.Errorf("%w: malformed PVEAPIToken value", ErrAuthFailed)
	}
	tokenID, secret, ok := strings.Cut(rest, "=")
	if !ok {
		return nil, fmt.Errorf("%w: malformed PVEAPIToken value", ErrAuthFailed)
	}
	user, found := srv.findUser(userID)
	if !found {
		return nil, fmt.Errorf("%w: invalid API token", ErrAuthFailed)
	}
	for _, tok := range user.Tokens {
		if tok.TokenID == tokenID && tok.Secret == secret {
			return &session{
				UserID:   user.UserID + "!" + tokenID,
				Privs:    append([]string(nil), user.Privileges...),
				IssuedAt: srv.state.clock(),
			}, nil
		}
	}
	return nil, fmt.Errorf("%w: invalid API token", ErrAuthFailed)
}

// requirePrivilege wraps a handler, authenticating the request and checking
// that the session holds priv before calling next. On failure it writes the
// PVE-style 401/403 response itself.
func (srv *Server) requirePrivilege(priv string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := srv.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if priv != "" && !sess.hasPrivilege(priv) {
			writeError(w, http.StatusForbidden, fmt.Sprintf("permission check failed (%s)", priv))
			return
		}
		next(w, r)
	}
}

// handleAccessPermissions implements GET /access/permissions: the
// effective, resolved ACL privilege tree for the calling identity, shaped
// {path: {privilege: 0|1}}. The mock's fixture users hold a single flat,
// non-path-scoped privilege list, so the whole grant is reported at the
// root path "/" — which PVE ACL inheritance (and internal/auth's
// BuildCapabilities) treats as applying everywhere. The fixture list is
// echoed verbatim, including a literal "*" wildcard where a fixture uses
// one; real PVE enumerates concrete privilege names instead, so the exact
// response shape (wildcard vs. enumeration, path granularity) still needs
// validation against real PVE.
func (srv *Server) handleAccessPermissions(w http.ResponseWriter, r *http.Request) {
	sess, err := srv.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	privs := make(map[string]int, len(sess.Privs))
	for _, p := range sess.Privs {
		privs[p] = 1
	}
	writeData(w, http.StatusOK, map[string]map[string]int{"/": privs})
}
