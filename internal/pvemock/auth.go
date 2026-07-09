package pvemock

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
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
	mu       sync.RWMutex
}

func newSessionStore() *sessionStore {
	return &sessionStore{byTicket: make(map[string]*session)}
}

func (s *sessionStore) create(userID string, privs []string) *session {
	sess := &session{
		Ticket:   "PVE:" + userID + ":" + randHex(8),
		CSRF:     randHex(16),
		UserID:   userID,
		Privs:    append([]string(nil), privs...),
		IssuedAt: time.Now(),
	}
	s.mu.Lock()
	s.byTicket[sess.Ticket] = sess
	s.mu.Unlock()
	return sess
}

func (s *sessionStore) lookup(ticket string) (*session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.byTicket[ticket]
	return sess, ok
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
	if !ok || user.Password != req.Password {
		// Real PVE returns 401 with an empty data payload on bad
		// credentials (never distinguishing "no such user" from "wrong
		// password").
		writeError(w, http.StatusUnauthorized, "authentication failure")
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
// does: a ticket via the PVEAuthCookie cookie (or Authorization: PVEAPIToken
// is out of scope for the mock — session-ticket auth only, matching what
// vnprox's client uses), and for mutating methods, a matching
// CSRFPreventionToken header.
func (srv *Server) authenticate(r *http.Request) (*session, error) {
	cookie, err := r.Cookie("PVEAuthCookie")
	if err != nil || cookie.Value == "" {
		return nil, fmt.Errorf("%w: missing PVEAuthCookie", ErrAuthFailed)
	}
	sess, ok := srv.state.sessions.lookup(cookie.Value)
	if !ok {
		return nil, fmt.Errorf("%w: unknown ticket", ErrAuthFailed)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Header.Get("CSRFPreventionToken") != sess.CSRF {
			return nil, fmt.Errorf("%w: CSRF token mismatch", ErrAuthFailed)
		}
	}
	return sess, nil
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
