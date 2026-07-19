package ingress

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ingress/ingressmock"
)

func TestTraefikDiscoverer_ParsesDouble(t *testing.T) {
	srv, rec := ingressmock.NewTraefikServer([]ingressmock.TraefikServer{
		{Name: "web-svc@docker", Enabled: true, URLs: []string{"http://10.0.0.8:5000", "http://10.0.0.9:5000"}},
		{Name: "stale-svc@docker", Enabled: false, URLs: []string{"http://10.0.0.10:5000"}},
	})
	defer srv.Close()

	d := &TraefikDiscoverer{Client: srv.Client()}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindTraefik, Address: srv.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !state.Reachable {
		t.Fatalf("expected Reachable, got Error=%q", state.Error)
	}
	if len(state.Backends) != 3 {
		t.Fatalf("expected 3 backends, got %d (%+v)", len(state.Backends), state.Backends)
	}
	byAddr := map[string]Backend{}
	for _, b := range state.Backends {
		byAddr[b.Address] = b
	}
	if !byAddr["10.0.0.8:5000"].Healthy || byAddr["10.0.0.8:5000"].Route != "web-svc@docker" {
		t.Errorf("backend 10.0.0.8:5000 = %+v, want healthy under web-svc@docker", byAddr["10.0.0.8:5000"])
	}
	if byAddr["10.0.0.10:5000"].Healthy {
		t.Errorf("expected 10.0.0.10:5000 unhealthy (disabled service)")
	}

	for _, req := range rec.Requests() {
		if req.Method != "GET" {
			t.Errorf("recorded non-GET request: %+v", req)
		}
		if req.Path != traefikServicesPath {
			t.Errorf("request path = %q, want %q", req.Path, traefikServicesPath)
		}
	}
}

func TestTraefikDiscoverer_Unreachable(t *testing.T) {
	d := &TraefikDiscoverer{Client: unreachableClient(t)}
	state, err := d.Discover(context.Background(), Target{ID: "t1", Kind: KindTraefik, Address: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("Discover should not error on an unreachable target: %v", err)
	}
	if state.Reachable {
		t.Fatalf("expected Reachable=false")
	}
}
