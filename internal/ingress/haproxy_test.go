package ingress

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ingress/ingressmock"
)

func TestHAProxyDiscoverer_ParsesDouble(t *testing.T) {
	srv, rec := ingressmock.NewHAProxyServer([]ingressmock.HAProxyBackend{
		{Pool: "web_pool", Name: "web1", Addr: "10.0.0.5:8080", Up: true},
		{Pool: "web_pool", Name: "web2", Addr: "10.0.0.6:8080", Up: false},
	})
	defer srv.Close()

	d := &HAProxyDiscoverer{Client: srv.Client()}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindHAProxy, Address: srv.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !state.Reachable {
		t.Fatalf("expected Reachable, got Error=%q", state.Error)
	}
	if len(state.Backends) != 2 {
		t.Fatalf("expected 2 backends, got %d (%+v)", len(state.Backends), state.Backends)
	}
	if state.Backends[0].Address != "10.0.0.5:8080" || !state.Backends[0].Healthy {
		t.Errorf("backend[0] = %+v, want healthy 10.0.0.5:8080", state.Backends[0])
	}
	if state.Backends[1].Address != "10.0.0.6:8080" || state.Backends[1].Healthy {
		t.Errorf("backend[1] = %+v, want unhealthy 10.0.0.6:8080", state.Backends[1])
	}

	for _, req := range rec.Requests() {
		if req.Method != "GET" {
			t.Errorf("recorded non-GET request: %+v", req)
		}
	}
}

func TestHAProxyDiscoverer_FallsBackToSvnameWithNoAddrColumn(t *testing.T) {
	// A version of HAProxy predating the `addr` column: served CSV omits
	// it entirely, so ParseHAProxyCSV must fall back to svname.
	backends, err := ParseHAProxyCSV([]byte("# pxname,svname,status\nweb_pool,FRONTEND,OPEN\nweb_pool,BACKEND,UP\nweb_pool,web1,UP\n"))
	if err != nil {
		t.Fatalf("ParseHAProxyCSV: %v", err)
	}
	if len(backends) != 1 || backends[0].Address != "web1" {
		t.Fatalf("backends = %+v, want one backend addressed by svname", backends)
	}
}

func TestHAProxyDiscoverer_Unreachable(t *testing.T) {
	d := &HAProxyDiscoverer{Client: unreachableClient(t)}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindHAProxy, Address: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("Discover should not error on an unreachable target: %v", err)
	}
	if state.Reachable {
		t.Fatalf("expected Reachable=false")
	}
	if state.Error == "" {
		t.Fatalf("expected a non-empty Error")
	}
}
