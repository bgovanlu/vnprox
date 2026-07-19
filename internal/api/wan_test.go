package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/wan"
)

// fakeWanService is an in-memory WanService test double.
type fakeWanService struct {
	statusErr   error
	replaceErr  error
	targets     map[string][]wan.Target
	gotReplaced []wan.Target
	status      wan.Status
}

func newFakeWanService() *fakeWanService {
	return &fakeWanService{targets: map[string][]wan.Target{}}
}

func (f *fakeWanService) Status(context.Context, int64) (wan.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeWanService) ListTargets(_ context.Context, node string) ([]wan.Target, error) {
	return f.targets[node], nil
}

func (f *fakeWanService) ReplaceTargets(_ context.Context, node string, targets []wan.Target, _ int64) error {
	if f.replaceErr != nil {
		return f.replaceErr
	}
	f.targets[node] = targets
	f.gotReplaced = targets
	return nil
}

// fakeFindingsForWan is a minimal FindingsService test double for the
// verdict-correlation tests below.
type fakeFindingsForWan struct {
	items []findings.Finding
}

func (f fakeFindingsForWan) Findings() []findings.Finding { return f.items }
func (f fakeFindingsForWan) FixOps(string) ([]change.Op, string, bool) {
	return nil, "", false
}

func newWanTestRouter(svc WanService, findingsSvc FindingsService, audit wanAuditor, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{},
		Wan: svc, Findings: findingsSvc, WanAudit: audit,
		LocalNode: func() string { return "pve1" },
	})
}

func TestWanRoutes_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
		LocalNode: func() string { return "pve1" },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wan/status", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET /wan/status with no WanService wired = %d, want 404 (route not mounted)", rr.Code)
	}
}

func TestWanRoutes_PUTNotMountedWithoutUsernameLookup(t *testing.T) {
	svc := newFakeWanService()
	audit := &fakeAuditor{}
	// fakeAuth (bare, no UsernameLookup) — the PUT route must not mount,
	// but the GET routes still should (mirrors mountProtectedRoutes' own
	// documented degrade for the same reason).
	r := newWanTestRouter(svc, nil, audit, fakeAuth{authenticated: true})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/wan/status", nil)
	getRR := httptest.NewRecorder()
	r.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /wan/status = %d, want 200 even without UsernameLookup", getRR.Code)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/wan/targets", bytes.NewReader([]byte(`{"targets":[]}`)))
	putRR := httptest.NewRecorder()
	r.ServeHTTP(putRR, putReq)
	// GET /wan/targets is mounted (it doesn't need UsernameLookup), so chi
	// reports "method not allowed" for PUT on that same path rather than
	// "not found" — the route itself simply never registers a PUT handler.
	if putRR.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /wan/targets with no UsernameLookup = %d, want 405 (no PUT handler registered)", putRR.Code)
	}
}

func TestWanTargets_GetPutRoundTrip(t *testing.T) {
	svc := newFakeWanService()
	audit := &fakeAuditor{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{username: "alice", fakeAuth: fakeAuth{authenticated: true}},
		caps:             map[string]bool{"netRead": true, "netWrite": true},
	}
	r := newWanTestRouter(svc, nil, audit, auth)

	body := `{"targets":[{"uplink":"vmbr0","host":"1.1.1.1"},{"uplink":"vmbr0","host":"8.8.8.8"}]}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/wan/targets", bytes.NewReader([]byte(body)))
	putReq.Header.Set("Content-Type", "application/json")
	putRR := httptest.NewRecorder()
	r.ServeHTTP(putRR, putReq)
	if putRR.Code != http.StatusOK {
		t.Fatalf("PUT /wan/targets = %d, body=%s", putRR.Code, putRR.Body.String())
	}
	if len(svc.gotReplaced) != 2 {
		t.Fatalf("ReplaceTargets got %d targets, want 2: %+v", len(svc.gotReplaced), svc.gotReplaced)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "wan.targets_update" {
		t.Fatalf("audit entries = %+v, want one wan.targets_update row", audit.entries)
	}
	if audit.entries[0].Username != "alice" {
		t.Errorf("audit Username = %q, want alice", audit.entries[0].Username)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/wan/targets", nil)
	getRR := httptest.NewRecorder()
	r.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /wan/targets = %d, body=%s", getRR.Code, getRR.Body.String())
	}
	var got wanTargetsResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Node != "pve1" || len(got.Targets) != 2 {
		t.Fatalf("got %+v, want node=pve1 with 2 targets", got)
	}
}

func TestWanTargets_Put_RequiresNetWrite(t *testing.T) {
	svc := newFakeWanService()
	audit := &fakeAuditor{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{username: "bob", fakeAuth: fakeAuth{authenticated: true}},
		caps:             map[string]bool{"netRead": true}, // no netWrite
	}
	r := newWanTestRouter(svc, nil, audit, auth)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/wan/targets", bytes.NewReader([]byte(`{"targets":[]}`)))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("PUT /wan/targets without netWrite = %d, want 403", rr.Code)
	}
}

func TestWanTargets_Put_RequiresCSRF(t *testing.T) {
	svc := newFakeWanService()
	audit := &fakeAuditor{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{username: "carol", fakeAuth: fakeAuth{authenticated: true}},
		caps:             map[string]bool{"netRead": true, "netWrite": true},
		csrf:             true,
	}
	r := newWanTestRouter(svc, nil, audit, auth)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/wan/targets", bytes.NewReader([]byte(`{"targets":[]}`)))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("PUT /wan/targets without CSRF header = %d, want 403", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/wan/targets", bytes.NewReader([]byte(`{"targets":[]}`)))
	req2.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("PUT /wan/targets with valid CSRF header = %d, want 200, body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestWanTargets_Put_ValidationRejectsEmptyFields(t *testing.T) {
	svc := newFakeWanService()
	audit := &fakeAuditor{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{username: "dave", fakeAuth: fakeAuth{authenticated: true}},
		caps:             map[string]bool{"netRead": true, "netWrite": true},
	}
	r := newWanTestRouter(svc, nil, audit, auth)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/wan/targets", bytes.NewReader([]byte(`{"targets":[{"uplink":"","host":"1.1.1.1"}]}`)))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT /wan/targets with empty uplink = %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
}

func TestWanStatus_VerdictHealthy(t *testing.T) {
	svc := &fakeWanService{status: wan.Status{
		Uplinks: []wan.UplinkStatus{{Node: "pve1", Uplink: "vmbr0", Status: wan.UplinkHealthy}},
	}}
	r := newWanTestRouter(svc, nil, &fakeAuditor{}, fakeAuth{authenticated: true})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wan/status", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /wan/status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got wanStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Verdict != "healthy" {
		t.Errorf("Verdict = %q, want healthy", got.Verdict)
	}
	if len(got.Uplinks) != 1 {
		t.Fatalf("got %d uplinks, want 1", len(got.Uplinks))
	}
}

func TestWanStatus_VerdictNoTargets(t *testing.T) {
	svc := &fakeWanService{status: wan.Status{}}
	r := newWanTestRouter(svc, nil, &fakeAuditor{}, fakeAuth{authenticated: true})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wan/status", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	var got wanStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Verdict != "no_targets" {
		t.Errorf("Verdict = %q, want no_targets", got.Verdict)
	}
}

// TestWanStatus_VerdictLikelyISP_WhenClusterOtherwiseClean is docs/api.md's
// "dashboard tile that says it's the ISP, not the cluster": a degraded WAN
// uplink plus an otherwise-quiet findings stream (no non-wan finding at
// warning+ severity) yields the "likely_isp" verdict.
func TestWanStatus_VerdictLikelyISP_WhenClusterOtherwiseClean(t *testing.T) {
	svc := &fakeWanService{status: wan.Status{
		Uplinks: []wan.UplinkStatus{{Node: "pve1", Uplink: "vmbr0", Status: wan.UplinkUnreachable}},
	}}
	findingsSvc := fakeFindingsForWan{items: []findings.Finding{
		{ID: "wan:wan_degraded|x", Source: findings.SourceWan, Severity: findings.SeverityWarning},
		{ID: "health:x|y", Source: findings.SourceHealth, Severity: findings.SeverityInfo}, // below warning, doesn't count
	}}
	r := newWanTestRouter(svc, findingsSvc, &fakeAuditor{}, fakeAuth{authenticated: true})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wan/status", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	var got wanStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Verdict != "likely_isp" {
		t.Errorf("Verdict = %q, want likely_isp: %+v", got.Verdict, got)
	}
}

// TestWanStatus_VerdictDegraded_WhenOtherFindingsAlsoActive: same degraded
// WAN uplink, but a real non-wan warning-level finding is also active — the
// verdict must not falsely blame the ISP alone.
func TestWanStatus_VerdictDegraded_WhenOtherFindingsAlsoActive(t *testing.T) {
	svc := &fakeWanService{status: wan.Status{
		Uplinks: []wan.UplinkStatus{{Node: "pve1", Uplink: "vmbr0", Status: wan.UplinkDegraded}},
	}}
	findingsSvc := fakeFindingsForWan{items: []findings.Finding{
		{ID: "health:bond_slave_down|x", Source: findings.SourceHealth, Severity: findings.SeverityWarning},
	}}
	r := newWanTestRouter(svc, findingsSvc, &fakeAuditor{}, fakeAuth{authenticated: true})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wan/status", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	var got wanStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Verdict != "wan_degraded" {
		t.Errorf("Verdict = %q, want wan_degraded (other cluster issues active too)", got.Verdict)
	}
}
