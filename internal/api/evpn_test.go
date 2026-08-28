// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/evpn"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// newEvpnLabEVPNService builds a real *evpn.Service backed by a real
// host.FixtureReader over a pvemock server loaded from evpn-lab.yaml
// (T-404's extension of T-401's fixture) — the same "route through the
// real stack against a real fixture" precedent newEvpnLabSDNService set,
// scoped to the local node only (no peer fan-out — internal/evpn's own
// service_test.go already covers cluster fan-out against hand-rolled
// PeerSource doubles).
func newEvpnLabEVPNService(t *testing.T, localNode string) *evpn.Service {
	t.Helper()
	fx, err := pvemock.LoadFixture("../../testdata/clusters/evpn-lab.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)
	reader := host.NewFixtureReader(pvemock.NewFixtureHostReader(srv))
	return evpn.NewService(evpn.Config{
		Host:      reader,
		LocalNode: func() string { return localNode },
	})
}

// evpnStatusResponse mirrors evpn.Status's JSON shape for response
// assertions, pinning the wire contract independently of the Go struct
// (sdnTreeResponse's own doc comment explains why).
type evpnStatusResponse struct {
	Nodes []struct {
		Node     string `json:"node"`
		RouterID string `json:"routerId"`
		Peers    []struct {
			PeerAddr    string `json:"peerAddr"`
			PeerNode    string `json:"peerNode"`
			State       string `json:"state"`
			StateReason string `json:"stateReason"`
			PfxRcd      int    `json:"pfxRcd"`
			UptimeSecs  int64  `json:"uptimeSecs"`
		} `json:"peers"`
		VNIs []struct {
			Type string `json:"type"`
			VNI  int    `json:"vni"`
		} `json:"vnis"`
		FRRInstalled bool `json:"frrInstalled"`
	} `json:"nodes"`
	Findings []struct {
		Code string `json:"code"`
	} `json:"findings"`
	GeneratedAt int64 `json:"generatedAt"`
	Partial     bool  `json:"partial"`
}

// TestEVPNRoute_EvpnLab_PeeringMatrixAndSessionDetail is T-404 acceptance
// criterion 1: the fixture matrix renders through the mounted
// GET /sdn/evpn/status route with established/idle/active states colored
// correctly (i.e. present and distinguishable in the response) and
// session detail matching the fixture JSON.
func TestEVPNRoute_EvpnLab_PeeringMatrixAndSessionDetail(t *testing.T) {
	svc := newEvpnLabEVPNService(t, "pve1")
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, EVPN: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/evpn/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sdn/evpn/status status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got evpnStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Partial {
		t.Error("Partial = true, want false for a single reachable local node")
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1 (local-only, no peer fan-out configured)", len(got.Nodes))
	}
	ns := got.Nodes[0]
	if ns.Node != "pve1" || !ns.FRRInstalled || ns.RouterID != "10.20.0.11" {
		t.Fatalf("node = %+v, want pve1/installed/10.20.0.11", ns)
	}
	if len(ns.Peers) != 2 {
		t.Fatalf("peers = %d, want 2 (matches evpn-lab.yaml's pve1 frr.peers)", len(ns.Peers))
	}
	byAddr := map[string]struct {
		PeerAddr    string `json:"peerAddr"`
		PeerNode    string `json:"peerNode"`
		State       string `json:"state"`
		StateReason string `json:"stateReason"`
		PfxRcd      int    `json:"pfxRcd"`
		UptimeSecs  int64  `json:"uptimeSecs"`
	}{}
	for _, p := range ns.Peers {
		byAddr[p.PeerAddr] = p
	}
	established, ok := byAddr["10.20.0.12"]
	if !ok || established.State != "Established" || established.PeerNode != "pve2" || established.PfxRcd != 6 {
		t.Errorf("10.20.0.12 session detail = %+v, want Established/pve2/pfxRcd=6 (matches fixture JSON)", established)
	}
	if established.UptimeSecs != 3600+23*60+45 {
		t.Errorf("10.20.0.12 UptimeSecs = %d, want %d", established.UptimeSecs, 3600+23*60+45)
	}
	idle, ok := byAddr["10.20.0.13"]
	if !ok || idle.State != "Idle" || idle.StateReason != "Admin" {
		t.Errorf("10.20.0.13 session detail = %+v, want Idle/Admin (matches fixture JSON's \"Idle (Admin)\")", idle)
	}

	if len(ns.VNIs) != 2 {
		t.Fatalf("vnis = %d, want 2", len(ns.VNIs))
	}
	vniTypes := map[int]string{}
	for _, v := range ns.VNIs {
		vniTypes[v.VNI] = v.Type
	}
	if vniTypes[10001] != "L2" || vniTypes[10000] != "L3" {
		t.Errorf("vni types = %+v, want {10001:L2, 10000:L3}", vniTypes)
	}
}

// TestEVPNRoute_ActiveState_OnPve2 exercises a different node's fixture
// data to cover the third matrix state ("Active" — a session still
// attempting to connect, never established) alongside Established/Idle
// already covered by the pve1 test above.
func TestEVPNRoute_ActiveState_OnPve2(t *testing.T) {
	svc := newEvpnLabEVPNService(t, "pve2")
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, EVPN: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/evpn/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got evpnStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	var foundActive bool
	for _, p := range got.Nodes[0].Peers {
		if p.PeerAddr == "10.20.0.13" && p.State == "Active" {
			foundActive = true
		}
	}
	if !foundActive {
		t.Errorf("expected pve2's session to 10.20.0.13 to be Active, got %+v", got.Nodes[0].Peers)
	}
}

// TestEVPNRoute_AbsentFRR_SingleNode is T-404 acceptance criterion 2:
// single-node.yaml's only node declares no `frr:` block at all, so the
// route must report it cleanly (frrInstalled=false, no error, not
// partial) rather than as a failure.
func TestEVPNRoute_AbsentFRR_SingleNode(t *testing.T) {
	fx, err := pvemock.LoadFixture("../../testdata/clusters/single-node.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)
	reader := host.NewFixtureReader(pvemock.NewFixtureHostReader(srv))
	var localNode string
	for name := range fx.Nodes {
		localNode = name
	}
	svc := evpn.NewService(evpn.Config{Host: reader, LocalNode: func() string { return localNode }})

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, EVPN: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/evpn/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got evpnStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Partial {
		t.Error("Partial = true, want false: absent FRR is not a fan-out failure")
	}
	if len(got.Nodes) != 1 || got.Nodes[0].FRRInstalled {
		t.Fatalf("nodes = %+v, want one node reporting frrInstalled=false", got.Nodes)
	}
}

func TestEVPNRoute_Unauthenticated401(t *testing.T) {
	svc := newEvpnLabEVPNService(t, "pve1")
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, EVPN: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/evpn/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestEVPNRoute_NotMountedWhenServiceNil(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, EVPN: nil,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/evpn/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
