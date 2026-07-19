package ingress

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ingress/ingressmock"
)

func TestCaddyDiscoverer_ParsesDouble(t *testing.T) {
	srv, rec := ingressmock.NewCaddyServer([]ingressmock.CaddyUpstream{
		{Address: "10.0.0.7:9000", Fails: 0},
		{Address: "10.0.0.8:9000", Fails: 3},
	})
	defer srv.Close()

	d := &CaddyDiscoverer{Client: srv.Client()}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindCaddy, Address: srv.URL})
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
	if !byAddr["10.0.0.7:9000"].Healthy {
		t.Errorf("expected 10.0.0.7:9000 healthy")
	}
	if byAddr["10.0.0.8:9000"].Healthy {
		t.Errorf("expected 10.0.0.8:9000 unhealthy (3 fails)")
	}

	reqs := rec.Requests()
	if len(reqs) == 0 {
		t.Fatalf("expected at least one recorded request")
	}
	for _, req := range reqs {
		if req.Method != "GET" {
			t.Errorf("recorded non-GET request: %+v", req)
		}
		if req.Path != caddyUpstreamsPath {
			t.Errorf("request path = %q, want %q", req.Path, caddyUpstreamsPath)
		}
	}
}

func TestCaddyDiscoverer_Unreachable(t *testing.T) {
	d := &CaddyDiscoverer{Client: unreachableClient(t)}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindCaddy, Address: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("Discover should not error on an unreachable target: %v", err)
	}
	if state.Reachable {
		t.Fatalf("expected Reachable=false")
	}
}
