package publicdemo_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/apidoc"
	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/publicdemo"
)

// fakeDaemon stands in for the whole vnprox handler: it mints a distinct
// session per login and, for anything else, reports back what it received.
// That last part is what lets a test assert what the edge FORWARDED, which
// is a different question from what the edge answered.
type fakeDaemon struct {
	seen     []string
	sessions []string
	reached  atomic.Int64
	logins   atomic.Int64
	mu       sync.Mutex
}

func (d *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login" {
		n := d.logins.Add(1)
		id := fmt.Sprintf("session-%d", n)
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: id, Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookieName, Value: fmt.Sprintf("csrf-%d", n), Path: "/"})
		d.mu.Lock()
		d.sessions = append(d.sessions, id)
		d.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}

	d.reached.Add(1)
	d.mu.Lock()
	d.seen = append(d.seen, r.Method+" "+r.URL.Path)
	d.mu.Unlock()

	session := ""
	if c, err := r.Cookie(auth.SessionCookieName); err == nil {
		session = c.Value
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"session": session, "path": r.URL.Path})
}

func (d *fakeDaemon) sawSession(t *testing.T, body string) string {
	t.Helper()
	var out struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decoding the fake daemon's echo %q: %v", body, err)
	}
	return out.Session
}

func newEdge(t *testing.T, next http.Handler, caps publicdemo.Caps, now func() time.Time) *publicdemo.Edge {
	t.Helper()
	e, err := publicdemo.New(next, publicdemo.Options{
		Login:  publicdemo.Login{Username: "root", Password: "vnprox-mock", Realm: "pam"},
		Caps:   caps,
		Now:    now,
		Logger: testLogger(),
	})
	if err != nil {
		t.Fatalf("building the edge: %v", err)
	}
	return e
}

// visit is one browser: it keeps its cookie jar across requests, exactly as
// a real visitor's browser does, so "two visitors" in these tests means two
// cookie jars and not two header values.
type visit struct {
	edge    *publicdemo.Edge
	cookies map[string]string
}

func newVisit(edge *publicdemo.Edge) *visit {
	return &visit{edge: edge, cookies: map[string]string{}}
}

func (v *visit) do(method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "203.0.113.7:41000"
	for name, value := range v.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	v.edge.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		v.cookies[c.Name] = c.Value
	}
	return rec
}

// --- AC1 -------------------------------------------------------------------
//
// The route list is READ from docs/openapi.json, the committed contract
// document, rather than written out here. A hardcoded list drifts the moment
// somebody adds a route; this one cannot, and an unclassifiable entry fails
// rather than being skipped.
//
// The end-to-end half of AC1 — the shipped daemon, started with the real
// flags, driven over TLS — is cmd/vnproxd/publicdemo_test.go. This is the
// same enumeration against the edge alone, where the control leg (the same
// list through the bare handler) costs nothing.

type openAPIDoc struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
}

// documentedRoutes flattens docs/openapi.json into (METHOD, path) pairs.
func documentedRoutes(t *testing.T) [][2]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil {
		t.Fatalf("reading docs/openapi.json: %v", err)
	}
	var doc openAPIDoc
	if unmarshalErr := json.Unmarshal(raw, &doc); unmarshalErr != nil {
		t.Fatalf("parsing docs/openapi.json: %v", unmarshalErr)
	}

	var out [][2]string
	for path, item := range doc.Paths {
		for method := range item {
			switch method {
			case "get", "put", "post", "delete", "patch", "head", "options":
				out = append(out, [2]string{strings.ToUpper(method), path})
			case "parameters", "summary", "description":
				// OpenAPI path-item keys that are not operations.
			default:
				t.Errorf("docs/openapi.json path %q has key %q, which this test does not know how to classify — "+
					"classify it rather than skipping it, or a mutating route could ship unrefused", path, method)
			}
		}
	}
	if len(out) < 200 {
		t.Fatalf("only %d routes parsed out of docs/openapi.json; the document is not being read (the daemon serves ~215)", len(out))
	}
	return out
}

// concretePath substitutes every {param} so the route is a real URL. The
// value is irrelevant — the edge refuses before routing, and the control leg
// only needs the request to arrive somewhere.
func concretePath(path string) string {
	out := path
	for strings.Contains(out, "{") {
		open := strings.Index(out, "{")
		closed := strings.Index(out[open:], "}")
		if closed < 0 {
			break
		}
		out = out[:open] + "demo-e2e" + out[open+closed+1:]
	}
	return out
}

func TestEdge_RefusesEveryMutatingRouteInTheCommittedDocument(t *testing.T) {
	routes := documentedRoutes(t)
	daemon := &fakeDaemon{}
	edge := newEdge(t, daemon, publicdemo.Caps{MaxVisitors: 4, RequestBurst: 10_000}, nil)

	v := newVisit(edge)
	mutating, safe := 0, 0
	for _, route := range routes {
		method, path := route[0], route[1]
		class, known := publicdemo.Classify(method)
		if !known {
			t.Errorf("%s %s: docs/openapi.json documents a method this edge does not classify; "+
				"an unclassified route must be a failure, not a skip", method, path)
			continue
		}
		rec := v.do(method, concretePath(path), "{}")

		if class == publicdemo.ClassMutating {
			mutating++
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s: status %d, want 403 — a mutating route reached the daemon in a public demo", method, path, rec.Code)
			}
			if got := rec.Header().Get(publicdemo.HeaderRefused); got != "public_demo_read_only" {
				t.Errorf("%s %s: %s = %q, want %q — the refusal must be attributable to the edge",
					method, path, publicdemo.HeaderRefused, got, "public_demo_read_only")
			}
			continue
		}

		// The control leg, and the reason the 403s above mean anything: the
		// same edge, the same visitor, forwards reads.
		safe++
		if rec.Header().Get(publicdemo.HeaderRefused) != "" {
			t.Errorf("%s %s: the edge refused a safe method (%s)", method, path, rec.Header().Get(publicdemo.HeaderRefused))
		}
	}

	if mutating < 50 || safe < 100 {
		t.Fatalf("drove %d mutating and %d safe routes; the document is not being enumerated", mutating, safe)
	}
	if got := int(daemon.reached.Load()); got != safe {
		t.Errorf("the daemon was reached %d times for %d safe routes — a mutating request got through, or a read did not", got, safe)
	}
}

// The other half of the control: without the edge, those same mutating
// routes are NOT refused. Without this, "every mutating route returns 403"
// could be satisfied by a daemon that refuses everything for some unrelated
// reason.
func TestEdge_WithoutTheEdgeNothingIsRefused(t *testing.T) {
	routes := documentedRoutes(t)
	daemon := &fakeDaemon{}

	for _, route := range routes {
		method, path := route[0], route[1]
		if class, _ := publicdemo.Classify(method); class != publicdemo.ClassMutating {
			continue
		}
		req := httptest.NewRequest(method, concretePath(path), strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		daemon.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden || rec.Header().Get(publicdemo.HeaderRefused) != "" {
			t.Fatalf("%s %s was refused with no edge in front of it; the control leg is broken", method, path)
		}
	}
}

// Not even the login route. A public demo has no login screen at all.
func TestEdge_RefusesLoginItself(t *testing.T) {
	daemon := &fakeDaemon{}
	edge := newEdge(t, daemon, publicdemo.Caps{}, nil)
	rec := newVisit(edge).do(http.MethodPost, "/api/v1/auth/login", `{"username":"root","password":"x","realm":"pam"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /auth/login: status %d, want 403", rec.Code)
	}
	if daemon.logins.Load() != 0 {
		t.Error("a visitor's login reached the daemon's login handler")
	}
}

// A method nobody enumerated is refused, not forwarded.
func TestEdge_RefusesAnUnrecognisedMethod(t *testing.T) {
	daemon := &fakeDaemon{}
	edge := newEdge(t, daemon, publicdemo.Caps{}, nil)
	rec := newVisit(edge).do("PROPFIND", "/api/v1/topology", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("PROPFIND: status %d, want 403", rec.Code)
	}
	if daemon.reached.Load() != 0 {
		t.Error("an unrecognised method reached the daemon")
	}
}

// --- AC3 -------------------------------------------------------------------

func TestEdge_EachVisitorGetsItsOwnSession(t *testing.T) {
	daemon := &fakeDaemon{}
	edge := newEdge(t, daemon, publicdemo.Caps{}, nil)

	a, b := newVisit(edge), newVisit(edge)
	bodyA := a.do(http.MethodGet, "/api/v1/topology", "").Body.String()
	bodyB := b.do(http.MethodGet, "/api/v1/topology", "").Body.String()

	sessionA, sessionB := daemon.sawSession(t, bodyA), daemon.sawSession(t, bodyB)
	if sessionA == "" || sessionB == "" {
		t.Fatalf("a visitor reached the daemon with no session (a=%q b=%q)", sessionA, sessionB)
	}
	if sessionA == sessionB {
		t.Errorf("both visitors were forwarded as session %q; a public demo must mint one per visitor", sessionA)
	}
	if got := daemon.logins.Load(); got != 2 {
		t.Errorf("%d logins for 2 visitors; sessions are not per-visitor", got)
	}

	// And the same visitor keeps theirs, rather than minting per request.
	if again := daemon.sawSession(t, a.do(http.MethodGet, "/api/v1/topology", "").Body.String()); again != sessionA {
		t.Errorf("visitor A's second request was session %q, first was %q", again, sessionA)
	}
	if got := daemon.logins.Load(); got != 2 {
		t.Errorf("%d logins after 3 requests from 2 visitors; the session is not being reused", got)
	}
	if got, want := a.cookies[auth.SessionCookieName], ""; got != want {
		t.Errorf("the session cookie reached the browser (%q); it must never leave the server", got)
	}
}

func TestEdge_OneVisitorsLayoutIsInvisibleToAnother(t *testing.T) {
	edge := newEdge(t, &fakeDaemon{}, publicdemo.Caps{}, nil)
	a, b := newVisit(edge), newVisit(edge)

	const key = publicdemo.VisitorStatePrefix + "topology"
	if rec := a.do(http.MethodPut, key, `{"state":{"nodes":{"pve1":{"x":10,"y":20}}}}`); rec.Code != http.StatusOK {
		t.Fatalf("visitor A saving a layout: status %d, body %s", rec.Code, rec.Body.String())
	}

	// The assertion.
	if rec := b.do(http.MethodGet, key, ""); rec.Code != http.StatusNotFound {
		t.Errorf("visitor B read visitor A's layout: status %d, body %s", rec.Code, rec.Body.String())
	}
	// The control leg, without which the line above is satisfied by a
	// surface that simply stores nothing.
	rec := a.do(http.MethodGet, key, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("visitor A could not read back their own layout: status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"pve1"`) {
		t.Errorf("visitor A's layout came back as %s", rec.Body.String())
	}

	// B's own save under the same key does not disturb A's.
	if rec = b.do(http.MethodPut, key, `{"state":{"nodes":{"pve1":{"x":99,"y":99}}}}`); rec.Code != http.StatusOK {
		t.Fatalf("visitor B saving a layout: status %d", rec.Code)
	}
	if body := a.do(http.MethodGet, key, "").Body.String(); !strings.Contains(body, `"x":10`) {
		t.Errorf("visitor B's save changed visitor A's layout: %s", body)
	}
}

// A visitor cannot present a session the edge did not mint for them.
func TestEdge_StripsAnInboundSessionCookie(t *testing.T) {
	daemon := &fakeDaemon{}
	edge := newEdge(t, daemon, publicdemo.Caps{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "stolen-from-a-real-operator"})
	rec := httptest.NewRecorder()
	edge.ServeHTTP(rec, req)

	if got := daemon.sawSession(t, rec.Body.String()); got == "stolen-from-a-real-operator" {
		t.Error("an inbound session cookie was forwarded to the daemon")
	} else if got == "" {
		t.Error("the request reached the daemon with no session at all; the edge should have replaced it with its own")
	}
}

// --- AC4 -------------------------------------------------------------------

func TestEdge_RequestCapDegradesOnlyThatVisitor(t *testing.T) {
	frozen := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return frozen }
	edge := newEdge(t, &fakeDaemon{}, publicdemo.Caps{RequestBurst: 5, RequestRefill: time.Minute}, clock)

	a, b := newVisit(edge), newVisit(edge)

	// The control leg: A is served up to its budget.
	for i := range 5 {
		if rec := a.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusOK {
			t.Fatalf("visitor A request %d: status %d, want 200", i+1, rec.Code)
		}
	}
	// And then it is not.
	rec := a.do(http.MethodGet, "/api/v1/topology", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("visitor A over budget: status %d, want 429", rec.Code)
	}
	if got := rec.Header().Get(publicdemo.HeaderRefused); got != "public_demo_rate_limited" {
		t.Errorf("%s = %q, want public_demo_rate_limited", publicdemo.HeaderRefused, got)
	}

	// The assertion AC4 actually makes: B is unaffected.
	if rec = b.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusOK {
		t.Errorf("visitor B while visitor A is throttled: status %d, want 200 — a cap that degrades the instance is the failure this exists to prevent", rec.Code)
	}
}

// Proven with a clock, not a wait.
func TestEdge_RequestBudgetRefillsOverTime(t *testing.T) {
	at := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }
	edge := newEdge(t, &fakeDaemon{}, publicdemo.Caps{RequestBurst: 2, RequestRefill: time.Second}, clock)
	a := newVisit(edge)

	a.do(http.MethodGet, "/api/v1/topology", "")
	a.do(http.MethodGet, "/api/v1/topology", "")
	if rec := a.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: status %d, want 429", rec.Code)
	}
	at = at.Add(5 * time.Second)
	if rec := a.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusOK {
		t.Errorf("after 5s of refill: status %d, want 200", rec.Code)
	}
}

func TestEdge_StateCapDegradesOnlyThatVisitor(t *testing.T) {
	edge := newEdge(t, &fakeDaemon{}, publicdemo.Caps{MaxStateBytes: 200, MaxStateEntries: 3}, nil)
	a, b := newVisit(edge), newVisit(edge)

	small := `{"state":{"k":"` + strings.Repeat("x", 20) + `"}}`
	if rec := a.do(http.MethodPut, publicdemo.VisitorStatePrefix+"tour", small); rec.Code != http.StatusOK {
		t.Fatalf("a small value: status %d", rec.Code)
	}

	big := `{"state":{"k":"` + strings.Repeat("x", 4000) + `"}}`
	rec := a.do(http.MethodPut, publicdemo.VisitorStatePrefix+"fat", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an over-cap value: status %d, want 413", rec.Code)
	}
	if got := rec.Header().Get(publicdemo.HeaderRefused); got != "public_demo_state_too_large" {
		t.Errorf("%s = %q, want public_demo_state_too_large", publicdemo.HeaderRefused, got)
	}
	// Nothing already stored was disturbed...
	if rec = a.do(http.MethodGet, publicdemo.VisitorStatePrefix+"tour", ""); rec.Code != http.StatusOK {
		t.Errorf("visitor A's earlier value after hitting the cap: status %d, want 200", rec.Code)
	}
	// ...and B can still store.
	if rec = b.do(http.MethodPut, publicdemo.VisitorStatePrefix+"tour", small); rec.Code != http.StatusOK {
		t.Errorf("visitor B while visitor A is over its state cap: status %d, want 200", rec.Code)
	}

	// The cap is on the visitor's TOTAL, not on one request's size. Without
	// this case the byte accounting is never exercised at all: a single
	// over-cap body is caught by the request reader's own limit, so removing
	// the accounting entirely still passes the assertion above. (Found by
	// mutating exactly that.)
	half := `{"state":"` + strings.Repeat("y", 90) + `"}`
	if rec = b.do(http.MethodPut, publicdemo.VisitorStatePrefix+"one", half); rec.Code != http.StatusOK {
		t.Fatalf("visitor B's first half-cap value: status %d", rec.Code)
	}
	if rec = b.do(http.MethodPut, publicdemo.VisitorStatePrefix+"two", half); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("visitor B's second half-cap value: status %d, want 413 — the cap is on the visitor's total, not on one body", rec.Code)
	}
	if rec = b.do(http.MethodGet, publicdemo.VisitorStatePrefix+"one", ""); rec.Code != http.StatusOK {
		t.Errorf("visitor B's first value after the second was refused: status %d, want 200 — a refused write must change nothing", rec.Code)
	}

	// The entry cap is its own limit, not a byte count in disguise.
	for i := range 3 {
		a.do(http.MethodPut, fmt.Sprintf("%sk%d", publicdemo.VisitorStatePrefix, i), `{"state":1}`)
	}
	if rec = a.do(http.MethodPut, publicdemo.VisitorStatePrefix+"one-too-many", `{"state":1}`); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("past the entry cap: status %d, want 413", rec.Code)
	}
	// Overwriting an existing key is not a new entry.
	if rec = a.do(http.MethodPut, publicdemo.VisitorStatePrefix+"k0", `{"state":2}`); rec.Code != http.StatusOK {
		t.Errorf("overwriting an existing key at the entry cap: status %d, want 200", rec.Code)
	}
}

func TestEdge_VisitorCapRefusesArrivalsAndKeepsTheSeated(t *testing.T) {
	at := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }
	edge := newEdge(t, &fakeDaemon{}, publicdemo.Caps{MaxVisitors: 2, VisitorIdleTTL: 10 * time.Minute}, clock)

	a, b, c := newVisit(edge), newVisit(edge), newVisit(edge)
	if rec := a.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusOK {
		t.Fatalf("visitor A: status %d", rec.Code)
	}
	if rec := b.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusOK {
		t.Fatalf("visitor B: status %d", rec.Code)
	}

	rec := c.do(http.MethodGet, "/api/v1/topology", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("visitor C at capacity: status %d, want 503", rec.Code)
	}
	if got := rec.Header().Get(publicdemo.HeaderRefused); got != "public_demo_at_capacity" {
		t.Errorf("%s = %q, want public_demo_at_capacity", publicdemo.HeaderRefused, got)
	}
	// The assertion that matters: the visitors already seated keep their
	// seats. A cap that evicted them would hand a flood of arrivals the
	// power to end everyone else's session.
	if rec = a.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusOK {
		t.Errorf("visitor A after C was refused: status %d, want 200", rec.Code)
	}

	// An idle visitor is reclaimed, so the instance recovers on its own.
	at = at.Add(11 * time.Minute)
	if rec = c.do(http.MethodGet, "/api/v1/topology", ""); rec.Code != http.StatusOK {
		t.Errorf("visitor C after the idle TTL: status %d, want 200", rec.Code)
	}
	if got := edge.VisitorCount(); got > 2 {
		t.Errorf("%d visitors tracked with a cap of 2", got)
	}
}

// An idle visitor is dropped even when nobody is waiting for their seat.
//
// Not (only) housekeeping: the daemon session the edge minted has its own
// idle timeout, and a visitor kept past it would be forwarded with a session
// the daemon has forgotten — a 401 on every read, and no login screen to
// recover through, because a public demo has none. Discarding the visitor
// first turns that into a fresh visitor with a fresh session.
func TestEdge_IdleVisitorsAreDroppedWithoutPressure(t *testing.T) {
	at := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }
	daemon := &fakeDaemon{}
	edge := newEdge(t, daemon, publicdemo.Caps{MaxVisitors: 100, VisitorIdleTTL: 20 * time.Minute}, clock)

	a := newVisit(edge)
	a.do(http.MethodGet, "/api/v1/topology", "")
	if got := edge.VisitorCount(); got != 1 {
		t.Fatalf("%d visitors after one arrival, want 1", got)
	}

	// The control leg: still inside the TTL, the visitor and their session
	// are exactly the ones they had.
	at = at.Add(10 * time.Minute)
	body := a.do(http.MethodGet, "/api/v1/topology", "").Body.String()
	if daemon.sawSession(t, body) != "session-1" {
		t.Errorf("visitor A was re-minted inside the idle TTL: %s", body)
	}

	// Past it, with nobody else arriving and the registry nowhere near full.
	at = at.Add(30 * time.Minute)
	body = a.do(http.MethodGet, "/api/v1/topology", "").Body.String()
	if got := daemon.sawSession(t, body); got != "session-2" {
		t.Errorf("session after the idle TTL = %q, want a freshly minted one — the visitor was kept past their session's own idle timeout", got)
	}
	if got := edge.VisitorCount(); got != 1 {
		t.Errorf("%d visitors tracked; the idle one was not reclaimed", got)
	}
}

// --- The visitor surface itself --------------------------------------------

func TestEdge_VisitorSessionRouteAnnouncesThePublicDemo(t *testing.T) {
	edge := newEdge(t, &fakeDaemon{}, publicdemo.Caps{RequestBurst: 42, MaxStateBytes: 4096, MaxStateEntries: 7}, nil)
	rec := newVisit(edge).do(http.MethodGet, publicdemo.VisitorSessionPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var got publicdemo.VisitorSession
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !got.PublicDemo || got.Visitor == "" {
		t.Errorf("body = %+v, want publicDemo true and a visitor id", got)
	}
	if got.Caps.RequestBurst != 42 || got.Caps.MaxStateBytes != 4096 || got.Caps.MaxStateEntries != 7 {
		t.Errorf("caps = %+v, want the configured limits", got.Caps)
	}
}

func TestEdge_VisitorStateRejectsAnUnusableKey(t *testing.T) {
	edge := newEdge(t, &fakeDaemon{}, publicdemo.Caps{}, nil)
	v := newVisit(edge)
	for _, name := range []string{"", "with/slash", strings.Repeat("k", 65), "space%20bar"} {
		rec := v.do(http.MethodGet, publicdemo.VisitorStatePrefix+name, "")
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("key %q: status %d, want 400 or 404", name, rec.Code)
		}
	}
}

// The edge's own surface is not the daemon's: nothing under it is forwarded.
func TestEdge_VisitorSurfaceNeverReachesTheDaemon(t *testing.T) {
	daemon := &fakeDaemon{}
	edge := newEdge(t, daemon, publicdemo.Caps{}, nil)
	v := newVisit(edge)
	v.do(http.MethodGet, publicdemo.VisitorSessionPath, "")
	v.do(http.MethodPut, publicdemo.VisitorStatePrefix+"tour", `{"state":{"step":1}}`)
	v.do(http.MethodGet, publicdemo.VisitorStatePrefix+"tour", "")
	v.do(http.MethodGet, publicdemo.VisitorPathPrefix+"nonsense", "")
	if got := daemon.reached.Load(); got != 0 {
		t.Errorf("the daemon was reached %d times by the edge's own surface", got)
	}
}

func TestEdge_RequiresACredentialToMintWith(t *testing.T) {
	if _, err := publicdemo.New(&fakeDaemon{}, publicdemo.Options{}); err == nil {
		t.Error("an edge with no login credential was accepted; it could only ever serve a login screen whose POST it refuses")
	}
}

// A guard on the enumeration itself: apidoc.Operations is the table that
// generates docs/openapi.json, so the two must describe the same routes. If
// they ever diverge, the AC1 enumeration above is reading a stale list and
// this says so directly rather than by a mysterious count.
func TestEdge_TheCommittedDocumentMatchesTheRouteTable(t *testing.T) {
	documented := map[string]bool{}
	for _, route := range documentedRoutes(t) {
		documented[apidoc.Key(route[0], route[1])] = true
	}
	var missing []string
	for key := range apidoc.Operations {
		if !documented[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d route(s) in internal/apidoc's Operations table are absent from docs/openapi.json: %s\n"+
			"Run `make openapi`. Until then the AC1 enumeration is driving a stale list.", len(missing), strings.Join(missing, ", "))
	}
}
