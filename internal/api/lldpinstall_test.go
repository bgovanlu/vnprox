package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeLocalLLDPInstaller records whether it was called and returns a
// canned error.
type fakeLocalLLDPInstaller struct {
	err    error
	called bool
}

func (f *fakeLocalLLDPInstaller) InstallLLDPD(_ context.Context) error {
	f.called = true
	return f.err
}

// fakePeerLLDPInstaller is a canned peer fan-out: peers is the discovered
// peer list, installErrs maps a peer node name to the error InstallLLDPD
// should return for it (nil/absent = success).
type fakePeerLLDPInstaller struct {
	peersErr    error
	installErrs map[string]error
	installed   []string
	peers       []peer.Peer
}

func (f *fakePeerLLDPInstaller) Peers(_ context.Context) ([]peer.Peer, error) {
	return f.peers, f.peersErr
}

func (f *fakePeerLLDPInstaller) InstallLLDPD(_ context.Context, p peer.Peer, confirm bool) error {
	if !confirm {
		return errors.New("confirm must be true")
	}
	f.installed = append(f.installed, p.Node)
	return f.installErrs[p.Node]
}

// fakeAuditor records every appended entry.
type fakeAuditor struct {
	entries []store.AuditEntry
}

func (f *fakeAuditor) Append(_ context.Context, e store.AuditEntry) (int64, error) {
	f.entries = append(f.entries, e)
	return int64(len(f.entries)), nil
}

func newLLDPInstallTestRouter(local LocalLLDPInstaller, peers PeerLLDPInstaller, audit lldpInstallAuditor, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{},
		LLDPInstaller: local, LLDPPeerInstaller: peers, LLDPAudit: audit,
		LocalNode: func() string { return "pve1" },
	})
}

func TestLLDPInstallRoutes_NotMountedWithoutInstaller(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lldp/install", bytes.NewBufferString(`{"confirm":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestLLDPInstallRoutes_RequiresConfirm(t *testing.T) {
	local := &fakeLocalLLDPInstaller{}
	r := newLLDPInstallTestRouter(local, nil, nil, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lldp/install", bytes.NewBufferString(`{"confirm":false}`))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	if local.called {
		t.Error("local installer must not run without confirm:true")
	}
}

func TestLLDPInstallRoutes_RequiresNetWrite(t *testing.T) {
	local := &fakeLocalLLDPInstaller{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetWrite: false},
	}
	r := newLLDPInstallTestRouter(local, nil, nil, auth)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lldp/install", bytes.NewBufferString(`{"confirm":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestLLDPInstallRoutes_RequiresCSRF(t *testing.T) {
	local := &fakeLocalLLDPInstaller{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetWrite: true},
		csrf:             true,
	}
	r := newLLDPInstallTestRouter(local, nil, nil, auth)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lldp/install", bytes.NewBufferString(`{"confirm":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing CSRF), body: %s", rec.Code, rec.Body.String())
	}
}

func TestLLDPInstallRoutes_LocalOnlySuccess(t *testing.T) {
	local := &fakeLocalLLDPInstaller{}
	audit := &fakeAuditor{}
	r := newLLDPInstallTestRouter(local, nil, audit, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lldp/install", bytes.NewBufferString(`{"confirm":true}`))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !local.called {
		t.Error("expected local installer to be called")
	}

	var resp lldpInstallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Node != "pve1" || !resp.Results[0].OK {
		t.Errorf("Results = %+v, want a single ok result for pve1", resp.Results)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "lldp.install" || audit.entries[0].Username != "alice" {
		t.Errorf("audit entries = %+v, want one lldp.install entry for alice", audit.entries)
	}
}

func TestLLDPInstallRoutes_FansOutToPeersAndReportsPartialFailure(t *testing.T) {
	local := &fakeLocalLLDPInstaller{}
	peers := &fakePeerLLDPInstaller{
		peers:       []peer.Peer{{Node: "pve2"}, {Node: "pve3"}},
		installErrs: map[string]error{"pve3": errors.New("apt-get: network unreachable")},
	}
	audit := &fakeAuditor{}
	r := newLLDPInstallTestRouter(local, peers, audit, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lldp/install", bytes.NewBufferString(`{"confirm":true}`))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var resp lldpInstallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("Results = %+v, want 3 entries (local + 2 peers)", resp.Results)
	}
	byNode := map[string]lldpInstallNodeResult{}
	for _, r := range resp.Results {
		byNode[r.Node] = r
	}
	if !byNode["pve1"].OK || !byNode["pve2"].OK {
		t.Errorf("expected pve1/pve2 to succeed, got %+v", resp.Results)
	}
	if byNode["pve3"].OK || byNode["pve3"].Error == "" {
		t.Errorf("expected pve3 to report its install error, got %+v", byNode["pve3"])
	}
	if len(audit.entries) != 3 {
		t.Errorf("expected 3 audit entries (one per node), got %d", len(audit.entries))
	}
}
