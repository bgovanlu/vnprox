package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/diagnose"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
)

// --- test doubles --------------------------------------------------------

// fakeDiagnoseAuth extends fakeAuthWithCaps with DiagnoseCapabilityChecker
// (POST /diagnose's capture-escalation gate), so this file's tests can
// exercise "capability held" vs. "capability not held" without a real
// internal/auth.Service.
type fakeDiagnoseAuth struct {
	fakeAuthWithCaps
	hasCapture bool
}

func (f fakeDiagnoseAuth) HasCap(_ context.Context, cap string) bool {
	if cap == capCapture {
		return f.hasCapture
	}
	return f.caps[cap]
}

func diagnoseTestAuth(caps map[string]bool, hasCapture bool) fakeDiagnoseAuth {
	return fakeDiagnoseAuth{
		fakeAuthWithCaps: fakeAuthWithCaps{
			caps: caps, csrf: true,
			fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
		},
		hasCapture: hasCapture,
	}
}

// fakeGuestInteriorToggleStore is a minimal in-memory
// GuestInteriorToggleStore double.
type fakeGuestInteriorToggleStore struct{ enabled map[string]bool }

func (f *fakeGuestInteriorToggleStore) Get(_ context.Context, ref string) (bool, error) {
	return f.enabled[ref], nil
}

func (f *fakeGuestInteriorToggleStore) Set(_ context.Context, ref string, enabled bool, _ string, _ int64) error {
	f.enabled[ref] = enabled
	return nil
}

// spyCaptureService is a CaptureService double whose Start call count AC2's
// "escalateToCapture: false never starts a capture session" regression
// asserts against directly.
type spyCaptureService struct {
	lastReq capture.StartRequest
	calls   int
}

func (s *spyCaptureService) Start(_ context.Context, req capture.StartRequest) (capture.Group, error) {
	s.calls++
	s.lastReq = req
	return capture.Group{ID: "grp-1", Status: capture.StatusRunning, StartedBy: req.StartedBy, Sessions: []capture.Session{{ID: "sess-1", GroupID: "grp-1", TargetRef: req.TargetRef}}}, nil
}
func (s *spyCaptureService) StopGroup(context.Context, string, string) (capture.Group, error) {
	return capture.Group{}, nil
}
func (s *spyCaptureService) Get(context.Context, string) (capture.Group, error) {
	return capture.Group{}, nil
}
func (s *spyCaptureService) List(context.Context) ([]capture.Group, error) { return nil, nil }

// --- request helper -------------------------------------------------------

func postDiagnose(t *testing.T, r http.Handler, targetRef string, escalate bool) (*httptest.ResponseRecorder, diagnose.Result) {
	t.Helper()
	body, err := json.Marshal(diagnoseRequest{TargetRef: targetRef, EscalateToCapture: escalate})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnose", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var res diagnose.Result
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
	}
	return rec, res
}

func stepByName(res diagnose.Result, name string) diagnose.StepResult {
	for _, s := range res.Steps {
		if s.Name == name {
			return s
		}
	}
	return diagnose.StepResult{}
}

// --- tests -----------------------------------------------------------

// TestDiagnose_AllStepsEligible_GuestNicTarget is T-1307 AC1's "target
// eligible for every step" half: a guest-nic target with a configured
// gateway, an opted-in interior toggle, and an escalated+capability-held
// capture request runs all five steps in order, each StatusRan.
func TestDiagnose_AllStepsEligible_GuestNicTarget(t *testing.T) {
	h := newSimLabHarness(t)
	toggles := &fakeGuestInteriorToggleStore{enabled: map[string]bool{"guest:pve1:300": true}}
	conntrackSrc := &fakeConntrackLocalSource{byNode: map[string][]host.ConntrackEntry{
		"": {{SrcIP: "10.20.0.11", DstIP: "10.20.0.1", Proto: 1}},
	}}
	capSvc := &spyCaptureService{}
	changesets := newChangesetTestService(t)
	fSvc := fakeFindingsService{
		findings: []findings.Finding{
			{ID: "health:bondslave|bridge:pve1:vmbr0", Source: findings.SourceHealth, Severity: findings.SeverityError, Fixable: true, Refs: []string{"bridge:pve1:vmbr0"}},
		},
		fixOps:   map[string][]change.Op{"health:bondslave|bridge:pve1:vmbr0": {{Type: change.OpBridgeUpdate}}},
		fixTitle: map[string]string{"health:bondslave|bridge:pve1:vmbr0": "fix bond slave"},
	}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:      diagnoseTestAuth(map[string]bool{"netRead": true, "netWrite": true}, true),
		Simulator: fakeInv{g: h.graph}, ProbeClients: h.fixedProbeClients(), ProbeAudit: h.audit, SimDivergence: h.divergence,
		GuestInteriorToggles: toggles, Conntrack: conntrackSrc, Captures: capSvc,
		Findings: fSvc, Changesets: changesets,
	})

	rec, res := postDiagnose(t, r, "guest-nic:pve1:300/net0", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if res.Target != "guest-nic:pve1:300/net0" {
		t.Errorf("target = %q", res.Target)
	}
	if len(res.Steps) != 5 {
		t.Fatalf("len(steps) = %d, want 5 (got %+v)", len(res.Steps), res.Steps)
	}
	wantOrder := []string{stepConfigCheck, stepLiveProbe, stepGuestInterior, stepConntrack, stepCapture}
	for i, name := range wantOrder {
		if res.Steps[i].Name != name {
			t.Errorf("steps[%d].Name = %q, want %q", i, res.Steps[i].Name, name)
		}
	}
	for _, name := range wantOrder {
		s := stepByName(res, name)
		if s.Status != diagnose.StatusRan {
			t.Errorf("step %s status = %q, want ran (summary: %q)", name, s.Status, s.Summary)
		}
	}

	if capSvc.calls != 1 {
		t.Errorf("capture Start calls = %d, want 1", capSvc.calls)
	}
	if capSvc.lastReq.TargetRef != "guest-nic:pve1:300/net0" {
		t.Errorf("capture StartRequest.TargetRef = %q", capSvc.lastReq.TargetRef)
	}

	if res.Verdict.SuggestedFixRef != "health:bondslave|bridge:pve1:vmbr0" {
		t.Errorf("verdict.suggestedFixRef = %q, want the fixable bridge finding's id", res.Verdict.SuggestedFixRef)
	}
	found := false
	for _, id := range res.Verdict.LinkedFindingIDs {
		if id == "health:bondslave|bridge:pve1:vmbr0" {
			found = true
		}
	}
	if !found {
		t.Errorf("verdict.linkedFindingIds = %v, want it to include the bridge finding", res.Verdict.LinkedFindingIDs)
	}

	entries, err := h.audit.List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	var diagnoseRows int
	for _, e := range entries {
		if e.Action == "diagnose.run" {
			diagnoseRows++
			if e.Result != "ok" {
				t.Errorf("diagnose.run audit result = %q, want ok", e.Result)
			}
		}
	}
	if diagnoseRows != 1 {
		t.Errorf("diagnose.run audit rows = %d, want exactly 1", diagnoseRows)
	}

	// AC5: the surfaced suggestedFixRef resolves through the SAME
	// POST /findings/{id}/fix route (no divergent fix-computation path).
	fixReq := httptest.NewRequest(http.MethodPost, "/api/v1/findings/"+res.Verdict.SuggestedFixRef+"/fix", bytes.NewReader(nil))
	fixReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	fixRec := httptest.NewRecorder()
	r.ServeHTTP(fixRec, fixReq)
	if fixRec.Code != http.StatusCreated {
		t.Fatalf("POST /findings/%s/fix status = %d, want 201, body: %s", res.Verdict.SuggestedFixRef, fixRec.Code, fixRec.Body.String())
	}
}

// TestDiagnose_BridgeTarget_SkipsGuestSpecificSteps is AC1's other half: a
// bare bridge target has no resolvable guest source, so config-check/
// live-probe/guest-interior are all StatusSkipped with a stated reason
// (never StatusError) — conntrack still runs, node-scoped.
func TestDiagnose_BridgeTarget_SkipsGuestSpecificSteps(t *testing.T) {
	h := newSimLabHarness(t)
	conntrackSrc := &fakeConntrackLocalSource{byNode: map[string][]host.ConntrackEntry{"": {{SrcIP: "10.20.0.11", DstIP: "10.20.0.20", Proto: 6}}}}
	capSvc := &spyCaptureService{}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:      diagnoseTestAuth(map[string]bool{"netRead": true}, true),
		Simulator: fakeInv{g: h.graph}, ProbeClients: h.fixedProbeClients(),
		GuestInteriorToggles: &fakeGuestInteriorToggleStore{enabled: map[string]bool{}},
		Conntrack:            conntrackSrc, Captures: capSvc,
	})

	rec, res := postDiagnose(t, r, "bridge:pve1:vmbr0", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	for _, name := range []string{stepConfigCheck, stepLiveProbe, stepGuestInterior} {
		s := stepByName(res, name)
		if s.Status != diagnose.StatusSkipped {
			t.Errorf("step %s status = %q, want skipped (summary: %q)", name, s.Status, s.Summary)
		}
		if s.Summary == "" {
			t.Errorf("step %s skipped with no reason stated", name)
		}
	}
	if s := stepByName(res, stepGuestInterior); s.Summary != "no guest interior for a non-guest target" {
		t.Errorf("guest-interior skip reason = %q", s.Summary)
	}
	if s := stepByName(res, stepConntrack); s.Status != diagnose.StatusRan {
		t.Errorf("conntrack status = %q, want ran for a node-scoped edge target", s.Status)
	}
	if s := stepByName(res, stepCapture); s.Status != diagnose.StatusSkipped {
		t.Errorf("capture status = %q, want skipped (escalation not requested)", s.Status)
	}
	if capSvc.calls != 0 {
		t.Errorf("capture Start calls = %d, want 0 (escalateToCapture was false)", capSvc.calls)
	}
}

// TestDiagnose_EscalateFalse_NeverStartsCapture is AC2's regression, spelled
// out even with the capture capability held: escalateToCapture defaulting
// to/being explicitly false must never call CaptureService.Start.
func TestDiagnose_EscalateFalse_NeverStartsCapture(t *testing.T) {
	h := newSimLabHarness(t)
	capSvc := &spyCaptureService{}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:      diagnoseTestAuth(map[string]bool{"netRead": true}, true),
		Simulator: fakeInv{g: h.graph}, Captures: capSvc,
	})

	_, res := postDiagnose(t, r, "guest-nic:pve1:300/net0", false)
	if capSvc.calls != 0 {
		t.Fatalf("capture Start calls = %d, want 0", capSvc.calls)
	}
	if s := stepByName(res, stepCapture); s.Status != diagnose.StatusSkipped {
		t.Errorf("capture status = %q, want skipped", s.Status)
	}
}

// TestDiagnose_CaptureCapabilityMissing_StepSkipped_OthersStillRun is AC3:
// a caller lacking the `capture` capability gets the capture step skipped
// (capability-not-held reason) while every other eligible step still runs
// and the ladder still returns a verdict — never a 403 for the whole call.
func TestDiagnose_CaptureCapabilityMissing_StepSkipped_OthersStillRun(t *testing.T) {
	h := newSimLabHarness(t)
	capSvc := &spyCaptureService{}
	conntrackSrc := &fakeConntrackLocalSource{}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:      diagnoseTestAuth(map[string]bool{"netRead": true}, false), // capture NOT held
		Simulator: fakeInv{g: h.graph}, ProbeClients: h.fixedProbeClients(),
		Conntrack: conntrackSrc, Captures: capSvc,
	})

	rec, res := postDiagnose(t, r, "guest-nic:pve1:300/net0", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cap := stepByName(res, stepCapture)
	if cap.Status != diagnose.StatusSkipped {
		t.Fatalf("capture status = %q, want skipped", cap.Status)
	}
	if cap.Summary != "the capture capability is not held by this session" {
		t.Errorf("capture skip reason = %q", cap.Summary)
	}
	if capSvc.calls != 0 {
		t.Errorf("capture Start calls = %d, want 0", capSvc.calls)
	}
	// Every other eligible step still ran.
	for _, name := range []string{stepConfigCheck, stepLiveProbe, stepConntrack} {
		if s := stepByName(res, name); s.Status == diagnose.StatusError {
			t.Errorf("step %s errored (%q); other steps must be unaffected by the capture gate", name, s.Summary)
		}
	}
	if res.Verdict.Summary == "" {
		t.Error("ladder must still return a verdict")
	}
}

func TestDiagnose_Unauthenticated401(t *testing.T) {
	h := newSimLabHarness(t)
	auth := fakeDiagnoseAuth{
		fakeAuthWithCaps: fakeAuthWithCaps{
			caps: map[string]bool{"netRead": true}, csrf: true,
			fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: false}},
		},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Simulator: fakeInv{g: h.graph},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnose", bytes.NewReader([]byte(`{"targetRef":"guest-nic:pve1:300/net0"}`)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body: %s", rec.Code, rec.Body.String())
	}
}

func TestDiagnose_MissingNetReadCap403(t *testing.T) {
	h := newSimLabHarness(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: diagnoseTestAuth(nil, false), Simulator: fakeInv{g: h.graph},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnose", bytes.NewReader([]byte(`{"targetRef":"guest-nic:pve1:300/net0"}`)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing netRead capability), body: %s", rec.Code, rec.Body.String())
	}
}

func TestDiagnose_MissingTargetRef400(t *testing.T) {
	h := newSimLabHarness(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: diagnoseTestAuth(map[string]bool{"netRead": true}, false), Simulator: fakeInv{g: h.graph},
	})
	rec, _ := postDiagnose(t, r, "", false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestDiagnose_InvalidTargetRef400(t *testing.T) {
	h := newSimLabHarness(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: diagnoseTestAuth(map[string]bool{"netRead": true}, false), Simulator: fakeInv{g: h.graph},
	})
	rec, _ := postDiagnose(t, r, "not-a-valid-ref", false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestDiagnoseRoute_NotMountedWithoutSimulator(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: diagnoseTestAuth(map[string]bool{"netRead": true}, false),
	})
	rec, _ := postDiagnose(t, r, "guest-nic:pve1:300/net0", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route not mounted without a Simulator graph)", rec.Code)
	}
}

func TestDiagnoseRoute_NotMountedWithoutUsernameLookup(t *testing.T) {
	h := newSimLabHarness(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Simulator: fakeInv{g: h.graph},
	})
	rec, _ := postDiagnose(t, r, "guest-nic:pve1:300/net0", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route not mounted without UsernameLookup)", rec.Code)
	}
}
