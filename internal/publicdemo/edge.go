// SPDX-License-Identifier: Apache-2.0

package publicdemo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
)

const (
	// HeaderPublicDemo is set on every response this edge produces,
	// refused or not. An operator running `curl -i` against a URL they were
	// given learns what they are talking to without parsing a body — the
	// same reasoning as T-2801's X-Vnprox-Demo, one layer out.
	HeaderPublicDemo = "X-Vnprox-Public-Demo"
	// HeaderRefused carries the machine-readable reason on a response this
	// edge produced instead of forwarding. Tests key their control legs on
	// its ABSENCE: "the daemon answered 403 for its own reasons" and "the
	// edge refused" are different facts, and a test that cannot tell them
	// apart is not asserting the edge exists.
	HeaderRefused = "X-Vnprox-Public-Demo-Refused"

	// VisitorPathPrefix is the edge's own visitor-scoped surface. Nothing
	// under it reaches the daemon; see the package doc.
	VisitorPathPrefix = "/demo/visitor/"
	// VisitorSessionPath answers "am I in a public demo, and who am I".
	VisitorSessionPath = VisitorPathPrefix + "session"
	// VisitorStatePrefix is the per-visitor scratch key space.
	VisitorStatePrefix = VisitorPathPrefix + "state/"

	// loginPath is the daemon route the edge mints visitor sessions
	// through. Deliberately the real handler and not a shortcut into
	// internal/auth: a visitor's session must be indistinguishable from one
	// an operator obtained by logging in, including its audit entry.
	loginPath = "/api/v1/auth/login"

	// codeReadOnly and friends are the error codes this edge answers with.
	codeReadOnly    = "public_demo_read_only"
	codeRateLimited = "public_demo_rate_limited"
	codeAtCapacity  = "public_demo_at_capacity"
	codeStateTooBig = "public_demo_state_too_large"
)

// Class is a request method's read/write classification.
type Class int

const (
	// ClassSafe is a method that cannot change anything: GET, HEAD,
	// OPTIONS.
	ClassSafe Class = iota
	// ClassMutating is everything else, INCLUDING methods this edge does
	// not recognise.
	ClassMutating
)

// Classify reports a method's class, and whether it is a method this edge
// recognises at all.
//
// An unrecognised method classifies as ClassMutating with ok=false: the
// edge refuses it (a method nobody enumerated is not one a public demo may
// assume is harmless), and a test enumerating docs/openapi.json fails on it
// rather than skipping it. That second half is the point of returning the
// bool at all — an unclassified route must be a failure, not a gap.
func Classify(method string) (Class, bool) {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return ClassSafe, true
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return ClassMutating, true
	default:
		return ClassMutating, false
	}
}

// Login is the credential the edge mints visitor sessions with. In a demo
// daemon this is the embedded fixture's own built-in superuser: there is no
// other credential store, and no real account exists to compromise.
type Login struct {
	Username string
	Password string
	Realm    string
}

// Options configures New.
type Options struct {
	// Now is the clock, injected so cap expiry is provable with a clock
	// rather than a sleep. nil means time.Now.
	Now    func() time.Time
	Logger *slog.Logger
	Login  Login
	Caps   Caps
}

// Edge is the public demo's front door: an http.Handler that wraps the
// whole daemon handler.
type Edge struct {
	next     http.Handler
	now      func() time.Time
	log      *slog.Logger
	visitors *registry
	login    Login
	caps     Caps
}

// New wraps next in a public-demo edge.
//
// It fails rather than defaults if there is no login credential: an edge
// that cannot mint sessions would serve every visitor a login screen whose
// POST it then refuses, which is a worse demo than no demo.
func New(next http.Handler, opts Options) (*Edge, error) {
	if next == nil {
		return nil, errors.New("publicdemo: no handler to wrap")
	}
	if opts.Login.Username == "" || opts.Login.Password == "" {
		return nil, errors.New("publicdemo: no visitor login credential; a public demo mints a session per visitor and cannot do so without one")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	caps := opts.Caps.withDefaults()
	return &Edge{
		next:     next,
		now:      now,
		log:      logger,
		visitors: newRegistry(caps),
		login:    opts.Login,
		caps:     caps,
	}, nil
}

// Caps reports the limits this edge enforces.
func (e *Edge) Caps() Caps { return e.caps }

// VisitorCount reports how many visitors are currently tracked.
func (e *Edge) VisitorCount() int { return e.visitors.count() }

// ServeHTTP is the whole edge, in the order the steps have to happen.
//
//  1. Identify the visitor (creating one, or refusing at capacity).
//  2. Charge their request budget.
//  3. Serve the edge's own visitor surface, if that is what was asked for.
//  4. Refuse anything mutating. Everything below this line is a read.
//  5. Attach the visitor's session and forward.
//
// Steps 1 and 2 precede the refusal deliberately: a visitor who floods the
// instance with POSTs is spending their own budget on 403s, not the
// instance's.
func (e *Edge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(HeaderPublicDemo, "1")
	now := e.now()

	v, created, err := e.visitors.lookupOrCreate(visitorIDFrom(r), now)
	if err != nil {
		if errors.Is(err, errAtCapacity) {
			e.refuse(w, http.StatusServiceUnavailable, codeAtCapacity,
				"this public demo is serving as many visitors as it can right now; try again in a few minutes. Nobody's session was ended to make room for yours, and nobody's will be.")
			return
		}
		e.log.Error("publicdemo: assigning a visitor", "error", err)
		e.refuse(w, http.StatusInternalServerError, "internal_error", "could not start a demo session")
		return
	}
	if created {
		setVisitorCookie(w, v.id)
	}

	if !v.allow(now, e.caps) {
		w.Header().Set("Retry-After", "1")
		e.refuse(w, http.StatusTooManyRequests, codeRateLimited,
			fmt.Sprintf("this demo session has exceeded its own request budget (%d requests, refilling one every %s). Every other visitor is unaffected.",
				e.caps.RequestBurst, e.caps.RequestRefill))
		return
	}

	if strings.HasPrefix(r.URL.Path, VisitorPathPrefix) {
		// The edge's own surface. It is reached BEFORE the write refusal
		// below and that is not a hole in it: nothing here is part of
		// docs/openapi.json, nothing here reaches the daemon or its store,
		// and a PUT writes to this visitor's own few kilobytes of memory
		// and nothing else. The visitor cookie is SameSite=Strict, so a
		// cross-site request arrives with no visitor at all and gets a
		// brand-new empty one.
		e.serveVisitor(w, r, v)
		return
	}

	if class, _ := Classify(r.Method); class == ClassMutating {
		e.refuse(w, http.StatusForbidden, codeReadOnly,
			"this is a public, read-only vnprox demo: every mutating request is refused at the edge, before it reaches the daemon. "+
				"Run `vnproxd --demo` locally for a demo that answers what it would have done, or install vnprox on a Proxmox VE node to manage a real cluster.")
		return
	}

	e.forward(w, r, v)
}

// forward attaches the visitor's session to a read request and hands it to
// the daemon.
//
// The session is minted lazily, on the visitor's first forwarded read,
// rather than when the visitor is created: a bot that only ever POSTs never
// causes a login, and the audit log therefore counts visitors who actually
// looked at something.
func (e *Edge) forward(w http.ResponseWriter, r *http.Request, v *visitor) {
	cookies := e.sessionFor(r, v)
	e.next.ServeHTTP(w, withSession(r, cookies))
}

// sessionFor returns this visitor's session cookies, minting them on first
// use. A mint failure is logged and answered with no cookies: the request
// then reaches the daemon unauthenticated and gets the daemon's own 401,
// which is a truthful answer, rather than a 500 invented here.
func (e *Edge) sessionFor(r *http.Request, v *visitor) []*http.Cookie {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.sessionCookies) > 0 {
		return v.sessionCookies
	}

	cookies, err := e.mint(r)
	if err != nil {
		e.log.Error("publicdemo: minting a visitor session", "error", err)
		return nil
	}
	v.sessionCookies = cookies
	return cookies
}

// mint drives the daemon's own login handler for one visitor.
//
// It reuses the visitor's RemoteAddr, so internal/auth's per-(IP, username)
// login limiter still applies to a host that manufactures visitors — the
// one place where a real IP is load-bearing rather than decorative.
func (e *Edge) mint(r *http.Request) ([]*http.Cookie, error) {
	body, err := json.Marshal(map[string]string{
		"username": e.login.Username,
		"password": e.login.Password,
		"realm":    e.login.Realm,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding the visitor login: %w", err)
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, loginPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building the visitor login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.RequestURI = loginPath
	req.RemoteAddr = r.RemoteAddr
	req.Host = r.Host
	req.TLS = r.TLS
	req.Proto, req.ProtoMajor, req.ProtoMinor = r.Proto, r.ProtoMajor, r.ProtoMinor

	rec := &capture{header: make(http.Header), status: http.StatusOK}
	e.next.ServeHTTP(rec, req)
	if rec.status != http.StatusOK {
		return nil, fmt.Errorf("the daemon refused the visitor login: status %d", rec.status)
	}

	var out []*http.Cookie
	for _, c := range (&http.Response{Header: rec.header}).Cookies() {
		if c.Value == "" {
			continue
		}
		if c.Name == auth.SessionCookieName || c.Name == auth.CSRFCookieName {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the daemon's login produced no session cookie")
	}
	return out, nil
}

// withSession returns a copy of r whose Cookie header carries exactly the
// edge's own session cookies.
//
// Any inbound session or CSRF cookie is DROPPED, whether or not the edge has
// one to replace it with. A public visitor must not be able to present a
// session this edge did not mint — including one harvested from somewhere
// else entirely.
func withSession(r *http.Request, cookies []*http.Cookie) *http.Request {
	out := r.Clone(r.Context())
	inbound := out.Cookies()
	out.Header.Del("Cookie")
	for _, c := range inbound {
		if c.Name == auth.SessionCookieName || c.Name == auth.CSRFCookieName {
			continue
		}
		out.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	for _, c := range cookies {
		out.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	// The double-submit CSRF header, for completeness rather than need: no
	// mutating request survives this edge, so nothing downstream checks it.
	for _, c := range cookies {
		if c.Name == auth.CSRFCookieName {
			out.Header.Set("X-VNPROX-CSRF", c.Value)
		}
	}
	return out
}

func visitorIDFrom(r *http.Request) string {
	c, err := r.Cookie(VisitorCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func setVisitorCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:  VisitorCookieName,
		Value: id,
		Path:  "/",
		// HttpOnly: the SPA never needs to read it, and a value no script
		// can read is one no injected script can exfiltrate.
		HttpOnly: true,
		Secure:   true,
		// Strict: a cross-site request arrives with no visitor cookie and
		// therefore cannot address anyone's scratch state but a new empty
		// one. This is what stands in for CSRF on the visitor surface.
		SameSite: http.SameSiteStrictMode,
	})
}

// refuse writes an edge refusal in docs/api.md's error envelope, marked so
// a test can tell it apart from the daemon's own refusals.
func (e *Edge) refuse(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set(HeaderRefused, code)
	writeJSONError(w, status, code, message)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// capture is a minimal http.ResponseWriter used to drive the daemon's login
// handler in-process. It keeps the headers (the Set-Cookie values are the
// whole point) and drops the body, which is the caps/user echo the edge has
// no use for.
type capture struct {
	header http.Header
	status int
}

func (c *capture) Header() http.Header         { return c.header }
func (c *capture) WriteHeader(status int)      { c.status = status }
func (c *capture) Write(p []byte) (int, error) { return len(p), nil }
