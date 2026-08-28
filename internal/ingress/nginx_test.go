// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ingress/ingressmock"
)

func TestNginxDiscoverer_PlusAPI_ParsesDouble(t *testing.T) {
	srv, rec := ingressmock.NewNginxServer(ingressmock.NginxPlusAPI, "backend_pool", []ingressmock.NginxPeer{
		{Server: "10.0.0.5:8080", Up: true},
		{Server: "10.0.0.6:8080", Up: false},
	})
	defer srv.Close()

	d := &NginxDiscoverer{Client: srv.Client()}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindNginx, Address: srv.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !state.Reachable {
		t.Fatalf("expected Reachable, got Error=%q", state.Error)
	}
	if len(state.Backends) != 2 {
		t.Fatalf("expected 2 backends, got %d (%+v)", len(state.Backends), state.Backends)
	}
	byAddr := map[string]Backend{}
	for _, b := range state.Backends {
		byAddr[b.Address] = b
	}
	if !byAddr["10.0.0.5:8080"].Healthy {
		t.Errorf("expected 10.0.0.5:8080 healthy")
	}
	if byAddr["10.0.0.6:8080"].Healthy {
		t.Errorf("expected 10.0.0.6:8080 unhealthy")
	}
	for _, req := range rec.Requests() {
		if req.Method != "GET" {
			t.Errorf("recorded non-GET request: %+v", req)
		}
	}
}

func TestNginxDiscoverer_StubStatus_ReportsAliveNoBackends(t *testing.T) {
	srv, _ := ingressmock.NewNginxServer(ingressmock.NginxStubStatus, "", nil)
	defer srv.Close()

	d := &NginxDiscoverer{Client: srv.Client()}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindNginx, Address: srv.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !state.Reachable {
		t.Fatalf("expected Reachable, got Error=%q", state.Error)
	}
	if len(state.Backends) != 0 {
		t.Fatalf("stub_status should never report backends, got %+v", state.Backends)
	}
}

func TestNginxDiscoverer_Unreachable(t *testing.T) {
	d := &NginxDiscoverer{Client: unreachableClient(t)}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindNginx, Address: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("Discover should not error on an unreachable target: %v", err)
	}
	if state.Reachable {
		t.Fatalf("expected Reachable=false")
	}
}
