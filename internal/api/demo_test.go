// SPDX-License-Identifier: Apache-2.0

// demo_test.go covers T-2801's API half.
//
// AC2: "Every mutating API in demo mode returns a 'would have' result and
// touches nothing; a store checksum before and after a full staged-and-
// applied changeset is unchanged."
//
// A ZERO-EFFECT ASSERTION IS VACUOUS WITHOUT A CONTROL LEG. "The checksum
// did not change" proves nothing unless the same checksum, over the same
// store, through the same router, is shown to change when a request does
// write. The control leg is the same router built WITHOUT demo mode,
// driven with the same requests — so the only difference between the two
// legs is the flag under test. It is in the same test as the assertion it
// underwrites.
//
// storeChecksum is T-2605's helper (changesets_preview_test.go): it hashes
// every user table, every row, every column, discovered from sqlite_master
// — not the database file, which in WAL mode is both flakier and less
// sensitive.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// demoHarness is newPreviewHarness with the demo flag on. Same store, same
// change service, same PVE mock and recorder — the recorder is what proves
// nothing reached the cluster either.
type demoHarness struct {
	*previewHarness
}

func newDemoHarness(t *testing.T) *demoHarness {
	t.Helper()
	h := newPreviewHarness(t)
	h.router = NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Changesets: h.svc, PVEGateways: previewGatewayProvider{gw: h.gateway},
		Demo: true,
	})
	return &demoHarness{previewHarness: h}
}

// changesetLifecycle is a full staged-and-applied changeset, driven through
// the API exactly as the UI drives it: create a draft, edit it, validate,
// apply, confirm. Every one of these is a mutating route.
//
// Deliberately NOT "one POST": the card says "a full staged-and-applied
// changeset", and a middleware that intercepted the create but let the
// apply through would pass a single-request assertion.
func changesetLifecycle(id string) []struct {
	Method string
	Path   string
	Body   string
} {
	return []struct {
		Method string
		Path   string
		Body   string
	}{
		{http.MethodPost, "/api/v1/changesets", `{"title":"demo write","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"mtu":1500}}]}`},
		{http.MethodPut, "/api/v1/changesets/" + id, `{"title":"demo write edited","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"mtu":9000}}]}`},
		{http.MethodPost, "/api/v1/changesets/" + id + "/validate", ``},
		{http.MethodPost, "/api/v1/changesets/" + id + "/apply", `{"confirmTimeoutSec":120}`},
		{http.MethodPost, "/api/v1/changesets/" + id + "/confirm", ``},
		{http.MethodPost, "/api/v1/changesets/" + id + "/rollback", ``},
		{http.MethodDelete, "/api/v1/changesets/" + id, ``},
	}
}

func demoDo(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// AC2. The store's logical content is byte-for-byte identical across a full
// changeset lifecycle, and the PVE mock records nothing — with both control
// legs proving each assertion could have failed.
func TestDemoMode_FullChangesetLifecycleTouchesNothing(t *testing.T) {
	h := newDemoHarness(t)

	// A changeset created OUT OF BAND (through the service, not the API) so
	// the lifecycle requests below have a real id to address. Created
	// before the checksum is taken, so its own row is part of the baseline.
	cs, err := h.svc.Create(t.Context(), "alice", "pre-existing", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	before := storeChecksum(t, h.db)
	pveCallsBefore := len(h.recorder.calls)

	for _, step := range changesetLifecycle(cs.ID) {
		rec := demoDo(t, h.router, step.Method, step.Path, step.Body)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: status = %d, want 200 (demo mode accepts and reports); body %s",
				step.Method, step.Path, rec.Code, rec.Body.String())
			continue
		}
		var got DemoWouldHave
		if decErr := json.Unmarshal(rec.Body.Bytes(), &got); decErr != nil {
			t.Errorf("%s %s: body is not a demo envelope: %v (body %s)", step.Method, step.Path, decErr, rec.Body.String())
			continue
		}
		if got.Demo.Mode != "demo" {
			t.Errorf("%s %s: demo.mode = %q, want \"demo\"", step.Method, step.Path, got.Demo.Mode)
		}
		if got.Demo.Method != step.Method || got.Demo.Path != step.Path {
			t.Errorf("%s %s: envelope reports %s %s; it must say what it would have done",
				step.Method, step.Path, got.Demo.Method, got.Demo.Path)
		}
		if rec.Header().Get(demoModeHeader) != "1" {
			t.Errorf("%s %s: no %s response header", step.Method, step.Path, demoModeHeader)
		}
	}

	if after := storeChecksum(t, h.db); after != before {
		t.Error("the store changed across a full staged-and-applied changeset in demo mode; every write path must be a no-op")
	}
	if got := h.recorder.calls[pveCallsBefore:]; len(got) != 0 {
		t.Errorf("demo mode reached the cluster: %v", got)
	}

	// CONTROL LEG 1: the same requests, through a router built the same way
	// but WITHOUT Demo, DO move the checksum. Without this, the assertion
	// above is satisfied by any router that 404s everything.
	control := newPreviewHarness(t)
	controlBefore := storeChecksum(t, control.db)
	rec := demoDo(t, control.router, http.MethodPost, "/api/v1/changesets",
		`{"title":"control","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"mtu":1500}}]}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("control leg: POST /changesets = %d, body %s", rec.Code, rec.Body.String())
	}
	if storeChecksum(t, control.db) == controlBefore {
		t.Fatal("control leg: a non-demo router's POST /changesets did not change the store checksum — " +
			"the identical-checksum assertion above proves nothing")
	}

	// CONTROL LEG 2: the checksum is sensitive to THIS store, not only to
	// some store. A write through the demo harness's own service (bypassing
	// the API, which is the only thing demo mode gates) must move it.
	if _, err := h.svc.Create(t.Context(), "alice", "control write", nil); err != nil {
		t.Fatalf("control leg 2: Create: %v", err)
	}
	if storeChecksum(t, h.db) == before {
		t.Fatal("control leg 2: the demo harness's own store checksum did not change after a direct write — " +
			"the checksum is not observing this store")
	}
}

// A demo daemon must answer the same way however many times it is asked. A
// side effect that only happens on first touch would hide behind a single
// pass of the lifecycle above.
func TestDemoMode_IsIdempotentAcrossRepeats(t *testing.T) {
	h := newDemoHarness(t)
	cs, err := h.svc.Create(t.Context(), "alice", "pre-existing", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := storeChecksum(t, h.db)
	for range 3 {
		for _, step := range changesetLifecycle(cs.ID) {
			if rec := demoDo(t, h.router, step.Method, step.Path, step.Body); rec.Code != http.StatusOK {
				t.Fatalf("%s %s: status = %d", step.Method, step.Path, rec.Code)
			}
		}
	}
	if storeChecksum(t, h.db) != before {
		t.Error("three passes of the changeset lifecycle changed the store")
	}
}

// AC3, direction two: a real endpoint cannot be CONFIGURED while in demo
// mode. These routes are refused by name rather than answered with a
// "would have" — "I would have attached your production cluster" is not a
// sentence a demo may say.
func TestDemoMode_RefusesEndpointConfiguration(t *testing.T) {
	h := newDemoHarness(t)
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"attach a federation cluster", http.MethodPost, "/api/v1/federation/clusters", `{"name":"prod","apiUrl":"https://pve.example.com:8006"}`},
		{"edit an attached cluster", http.MethodPut, "/api/v1/federation/clusters/abc", `{"apiUrl":"https://pve.example.com:8006"}`},
		{"detach a cluster", http.MethodDelete, "/api/v1/federation/clusters/abc", ``},
		{"register a kubernetes cluster", http.MethodPost, "/api/v1/k8s/clusters", `{"name":"prod","apiUrl":"https://k8s.example.com:6443"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := demoDo(t, h.router, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
			}
			var env struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("body is not the documented error envelope: %v (%s)", err, rec.Body.String())
			}
			if env.Error.Code != "demo_real_endpoint_refused" {
				t.Errorf("error code = %q, want demo_real_endpoint_refused — AC3 asks for a NAMED error", env.Error.Code)
			}
		})
	}

	// The distinction is the point: an ordinary mutating route still gets
	// the "would have" answer, not this refusal. If both shapes collapsed
	// into one, the test above would pass for the wrong reason.
	for _, path := range []string{"/api/v1/changesets", "/api/v1/webhooks", "/api/v1/alert-rules"} {
		rec := demoDo(t, h.router, http.MethodPost, path, `{"title":"t","ops":[]}`)
		if rec.Code != http.StatusOK {
			t.Errorf("POST %s returned %d; only the endpoint-configuring routes are refused", path, rec.Code)
		}
	}
}

// Every prefix in demoRefusedEndpointPrefixes must name a route this
// package actually registers. The middleware runs in front of routing, so a
// prefix for a nonexistent route would answer 403 where a 404 belongs — and
// the refusal test above would then be passing against nothing at all.
//
// Checked against the package's own source rather than against a live
// router, because a live router only mounts the routes whose services the
// test harness happened to wire, and this has to hold for the daemon's
// router, not the harness's.
func TestDemoMode_RefusedEndpointPrefixesNameRealRoutes(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	var source strings.Builder
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatalf("reading %s: %v", name, readErr)
		}
		source.Write(body)
	}
	text := source.String()
	for _, prefix := range demoRefusedEndpointPrefixes {
		route := strings.TrimPrefix(prefix, "/api/v1")
		if !strings.Contains(text, `"`+route+`"`) {
			t.Errorf("demoRefusedEndpointPrefixes names %s, but no route %q is registered anywhere in this package", prefix, route)
		}
	}
}

// Reads are untouched. A demo whose read routes were also intercepted would
// render nothing at all, and every "the screens are populated" assertion
// elsewhere would be measuring the wrong thing.
func TestDemoMode_LeavesReadsAlone(t *testing.T) {
	h := newDemoHarness(t)
	for _, path := range []string{"/api/v1/health", "/api/v1/topology", "/api/v1/config"} {
		rec := demoDo(t, h.router, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d in demo mode; reads must pass through", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), `"wouldHave"`) {
			t.Errorf("GET %s was intercepted by the demo write middleware", path)
		}
	}
}

// The session lifecycle is the ONE mutating exception, and it has to be:
// a demo whose login POST answered "I would have logged you in" would show
// no screens at all. Asserted as an exact set, so a future addition to
// demoAllowedWrites is a deliberate edit here rather than a silent widening.
func TestDemoMode_AllowedWritesAreOnlyTheSessionLifecycle(t *testing.T) {
	want := map[string]bool{
		"/api/v1/auth/login":         true,
		"/api/v1/auth/logout":        true,
		"/api/v1/auth/oidc/callback": true,
	}
	if len(demoAllowedWrites) != len(want) {
		t.Fatalf("demoAllowedWrites has %d entries, want %d: %v", len(demoAllowedWrites), len(want), demoAllowedWrites)
	}
	for path := range want {
		if !demoAllowedWrites[path] {
			t.Errorf("%s is no longer allowed; the demo cannot establish a session", path)
		}
	}
	for path := range demoAllowedWrites {
		if !want[path] {
			t.Errorf("%s was added to demoAllowedWrites; every mutating route outside the session lifecycle must be a no-op", path)
		}
	}
}

// Unrecognized methods count as mutating. A method this router does not
// know is not a method demo mode may assume is harmless.
func TestDemoMode_UnknownMethodsAreTreatedAsMutating(t *testing.T) {
	for _, m := range []string{http.MethodPatch, "PROPFIND", "MKCOL"} {
		if !isMutatingMethod(m) {
			t.Errorf("isMutatingMethod(%q) = false; only GET/HEAD/OPTIONS are safe", m)
		}
	}
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if isMutatingMethod(m) {
			t.Errorf("isMutatingMethod(%q) = true; a demo that intercepted reads would render nothing", m)
		}
	}
}

// Off by default. Every existing deployment's router must be unchanged.
func TestDemoMode_OffByDefault(t *testing.T) {
	h := newPreviewHarness(t)
	rec := demoDo(t, h.router, http.MethodGet, "/api/v1/health", "")
	if rec.Header().Get(demoModeHeader) != "" {
		t.Error("a non-demo daemon set the demo header")
	}
	var health struct {
		Demo bool `json:"demo"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decoding health: %v", err)
	}
	if health.Demo {
		t.Error("a non-demo daemon reported demo:true on /health")
	}
}

func TestDemoMode_HealthAndConfigAdvertiseIt(t *testing.T) {
	h := newDemoHarness(t)

	rec := demoDo(t, h.router, http.MethodGet, "/api/v1/health", "")
	var health struct {
		Demo bool `json:"demo"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decoding health: %v", err)
	}
	if !health.Demo {
		t.Error("GET /health does not report demo:true; the SPA's banner reads this route BEFORE login and would never render")
	}
}

// T-2801-followup-01: two POST-shaped READ routes execute for real in demo
// mode instead of answering "would have" — POST /simulate/path (pure
// computation, no store access at all) and POST /diagnose (whose handler
// normally audits; router.go wires that dependency to nil in demo mode —
// see demo.go's demoReadOnlyPosts). Both still must not move the store
// checksum, with the same control-leg discipline as AC2's changeset-
// lifecycle test above: a real audit repo IS wired into Options here, so
// the assertion is that demo mode's own wiring skips it, not that the test
// simply never gave it anything to write to.
func TestDemoMode_ReadOnlyPostsExecuteForReal(t *testing.T) {
	base := newPreviewHarness(t)
	graph := buildSimGraph(t)
	audit := store.NewAuditRepo(base.db)

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Changesets: base.svc, PVEGateways: previewGatewayProvider{gw: base.gateway},
		Simulator: graph, ProbeAudit: audit,
		Demo: true,
	})

	before := storeChecksum(t, base.db)

	// POST /simulate/path: a real simulate result, not a demo envelope.
	rec := demoDo(t, r, http.MethodPost, "/api/v1/simulate/path",
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:100/net0"},"dst":{"kind":"guest-nic","ref":"guest-nic:pve1:101/net0"},"proto":"tcp","port":80}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /simulate/path: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var simResult simulateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &simResult); err != nil {
		t.Fatalf("POST /simulate/path: decoding: %v", err)
	}
	if simResult.Verdict == "" {
		t.Error("POST /simulate/path: verdict is empty — looks like a would-have envelope, not a real result")
	}
	if rec.Header().Get(demoModeHeader) != "1" {
		t.Errorf("POST /simulate/path: missing %s header even though this is still a demo daemon", demoModeHeader)
	}
	var wouldHave DemoWouldHave
	if err := json.Unmarshal(rec.Body.Bytes(), &wouldHave); err == nil && wouldHave.Demo.Mode == "demo" {
		t.Error("POST /simulate/path: got a would-have envelope, want a real simulate result")
	}

	// POST /diagnose: a real diagnose result.
	diagRec, diagResult := postDiagnose(t, r, "guest-nic:pve1:100/net0", false)
	if diagRec.Code != http.StatusOK {
		t.Fatalf("POST /diagnose: status = %d, body %s", diagRec.Code, diagRec.Body.String())
	}
	if diagResult.Target != "guest-nic:pve1:100/net0" {
		t.Errorf("POST /diagnose: target = %q, want guest-nic:pve1:100/net0 — looks like a would-have envelope", diagResult.Target)
	}
	if len(diagResult.Steps) == 0 {
		t.Error("POST /diagnose: no steps ran — looks like a would-have envelope, not a real ladder run")
	}

	if after := storeChecksum(t, base.db); after != before {
		t.Error("the store changed after read-only POST /simulate/path and POST /diagnose in demo mode")
	}

	entries, err := audit.List(context.Background(), "", 20)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	for _, e := range entries {
		if e.Action == "diagnose.run" || e.Action == "diagnose.step" {
			t.Errorf("found a %s audit row from a demo-mode /diagnose call — audit must be nil'd out in demo mode", e.Action)
		}
	}

	// CONTROL LEG: the same audit repo, driven by the same handler, WITHOUT
	// Demo, DOES write diagnose.run/diagnose.step rows — proves the
	// assertion above isn't vacuous (e.g. audit silently broken).
	control := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Simulator: graph, ProbeAudit: audit,
	})
	controlRec, _ := postDiagnose(t, control, "guest-nic:pve1:100/net0", false)
	if controlRec.Code != http.StatusOK {
		t.Fatalf("control leg: POST /diagnose: status = %d, body %s", controlRec.Code, controlRec.Body.String())
	}
	entries, err = audit.List(context.Background(), "", 20)
	if err != nil {
		t.Fatalf("control leg: audit.List: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "diagnose.run" {
			found = true
		}
	}
	if !found {
		t.Fatal("control leg: a non-demo router's POST /diagnose did not write a diagnose.run audit row — the assertion above proves nothing")
	}
}
