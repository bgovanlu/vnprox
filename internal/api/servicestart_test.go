// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
)

type fakeServiceStarter struct {
	err     error
	started []string
}

func (f *fakeServiceStarter) StartService(_ context.Context, unit string) error {
	f.started = append(f.started, unit)
	return f.err
}

type fakePeerServiceStarter struct {
	peersErr error
	err      error
	peers    []peer.Peer
	started  []string
}

func (f *fakePeerServiceStarter) Peers(_ context.Context) ([]peer.Peer, error) {
	return f.peers, f.peersErr
}

func (f *fakePeerServiceStarter) StartService(_ context.Context, p peer.Peer, unit string, confirm bool) error {
	if !confirm {
		return errors.New("confirm must be true")
	}
	f.started = append(f.started, p.Node+"/"+unit)
	return f.err
}

func newServiceStartRouter(local ServiceStarter, peers PeerServiceStarter, audit collectorRefreshAuditor, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{},
		ServiceStarter: local, PeerServiceStarter: peers, CollectorAudit: audit,
		LocalNode: func() string { return "pve1" },
	})
}

func postStart(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/services/start", bytes.NewBufferString(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The allow-list is the whole security story of this route. It is checked
// here, again on the receiving peer, and a third time in the function that
// builds the argv — this asserts the first of the three.
func TestServiceStart_RefusesAUnitOutsideTheAllowList(t *testing.T) {
	for _, unit := range []string{"sshd", "pve-cluster", "", "dnsmasq; rm -rf /", "DNSMASQ", "frr.service"} {
		t.Run(unit, func(t *testing.T) {
			local := &fakeServiceStarter{}
			body, err := json.Marshal(serviceStartRequest{Node: "pve1", Unit: unit, Confirm: true})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			rec := postStart(t, newServiceStartRouter(local, nil, nil, fullCapsAuth("alice")), string(body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for unit %q", rec.Code, unit)
			}
			if len(local.started) != 0 {
				t.Errorf("started %v — a refused unit must never reach the host writer", local.started)
			}
		})
	}
}

// A refused attempt is the single most interesting thing this route can
// log. Dropping it would make the one event worth reviewing the one that
// leaves no trace.
func TestServiceStart_AuditsARefusal(t *testing.T) {
	audit := &fakeAuditor{}
	rec := postStart(t, newServiceStartRouter(&fakeServiceStarter{}, nil, audit, fullCapsAuth("alice")),
		`{"node":"pve1","unit":"sshd","confirm":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(audit.entries))
	}
	if audit.entries[0].Result != "refused" {
		t.Errorf("result = %q, want refused", audit.entries[0].Result)
	}
	if audit.entries[0].Target.String != "pve1/sshd" {
		t.Errorf("target = %q, want pve1/sshd", audit.entries[0].Target.String)
	}
}

func TestServiceStart_RequiresConfirm(t *testing.T) {
	local := &fakeServiceStarter{}
	rec := postStart(t, newServiceStartRouter(local, nil, nil, fullCapsAuth("alice")),
		`{"node":"pve1","unit":"dnsmasq","confirm":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if len(local.started) != 0 {
		t.Error("started without confirm")
	}
}

func TestServiceStart_RequiresNetWrite(t *testing.T) {
	local := &fakeServiceStarter{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetWrite: false},
	}
	rec := postStart(t, newServiceStartRouter(local, nil, nil, auth), `{"node":"pve1","unit":"dnsmasq","confirm":true}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if len(local.started) != 0 {
		t.Error("a session without netWrite started a service")
	}
}

func TestServiceStart_StartsLocallyForTheLocalNode(t *testing.T) {
	local := &fakeServiceStarter{}
	audit := &fakeAuditor{}
	rec := postStart(t, newServiceStartRouter(local, nil, audit, fullCapsAuth("alice")),
		`{"node":"pve1","unit":"dnsmasq","confirm":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if len(local.started) != 1 || local.started[0] != "dnsmasq" {
		t.Errorf("started = %v, want [dnsmasq]", local.started)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "service.start" || audit.entries[0].Result != "ok" {
		t.Errorf("audit = %+v, want one ok service.start row", audit.entries)
	}
}

func TestServiceStart_RoutesToThePeerForARemoteNode(t *testing.T) {
	local := &fakeServiceStarter{}
	peers := &fakePeerServiceStarter{peers: []peer.Peer{{Node: "pve2"}}}
	rec := postStart(t, newServiceStartRouter(local, peers, nil, fullCapsAuth("alice")),
		`{"node":"pve2","unit":"frr","confirm":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if len(peers.started) != 1 || peers.started[0] != "pve2/frr" {
		t.Errorf("peer started = %v, want [pve2/frr]", peers.started)
	}
	if len(local.started) != 0 {
		t.Error("a remote node's service was started locally")
	}
}

// Reporting success for a node this daemon has never heard of would be the
// worst possible outcome of pressing "start": the operator believes the
// outage is fixed and it is not.
func TestServiceStart_UnknownNodeIsAnError(t *testing.T) {
	peers := &fakePeerServiceStarter{peers: []peer.Peer{{Node: "pve2"}}}
	rec := postStart(t, newServiceStartRouter(&fakeServiceStarter{}, peers, nil, fullCapsAuth("alice")),
		`{"node":"pve9","unit":"frr","confirm":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

// systemd's own message reaches the operator: "Unit frr.service is masked"
// is actionable, "could not start service" is not.
func TestServiceStart_SurfacesTheSystemdErrorVerbatim(t *testing.T) {
	// Verbatim systemd output, trailing period and capitals included — the
	// whole point of the test is that this reaches the operator unedited.
	//nolint:revive // error-strings: this is a captured message, not one this codebase authors
	local := &fakeServiceStarter{err: errors.New("systemctl start frr: exit status 1: Failed to start frr.service: Unit frr.service is masked.")}
	audit := &fakeAuditor{}
	rec := postStart(t, newServiceStartRouter(local, nil, audit, fullCapsAuth("alice")),
		`{"node":"pve1","unit":"frr","confirm":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("is masked")) {
		t.Errorf("body = %s, want the systemd message", rec.Body.String())
	}
	if len(audit.entries) != 1 || audit.entries[0].Result != "error" {
		t.Errorf("audit = %+v, want one error row", audit.entries)
	}
}

// The route's allow-list and the host package's must not drift: the route
// checks host.IsWatchedService directly, and this pins that the exported
// list it is documented against agrees with it.
func TestServiceStart_AllowListMatchesTheHostPackage(t *testing.T) {
	for _, unit := range host.WatchedServices {
		if !host.IsWatchedService(unit) {
			t.Errorf("WatchedServices contains %q but IsWatchedService rejects it", unit)
		}
	}
	if host.IsWatchedService("sshd") {
		t.Error("IsWatchedService accepted sshd")
	}
}
