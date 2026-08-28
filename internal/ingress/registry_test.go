// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"context"
	"testing"
)

type fakeDiscoverer struct {
	err   error
	state ProxyState
	calls int
}

func (f *fakeDiscoverer) Discover(_ context.Context, target Target) (ProxyState, error) {
	f.calls++
	return f.state, f.err
}

func TestRegistry_DispatchesByKind(t *testing.T) {
	haproxy := &fakeDiscoverer{state: ProxyState{Reachable: true, Kind: KindHAProxy}}
	nginx := &fakeDiscoverer{state: ProxyState{Reachable: true, Kind: KindNginx}}
	reg := Registry{KindHAProxy: haproxy, KindNginx: nginx}

	state, err := reg.Discover(context.Background(), Target{Kind: KindHAProxy})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !state.Reachable || haproxy.calls != 1 || nginx.calls != 0 {
		t.Fatalf("expected only the haproxy discoverer called, got haproxy.calls=%d nginx.calls=%d", haproxy.calls, nginx.calls)
	}
}

func TestRegistry_UnregisteredKindReportsUnreachableNotError(t *testing.T) {
	reg := Registry{KindHAProxy: &fakeDiscoverer{}}
	state, err := reg.Discover(context.Background(), Target{ID: "t9", Kind: "unknown-vendor"})
	if err != nil {
		t.Fatalf("Discover should not error for an unregistered kind: %v", err)
	}
	if state.Reachable {
		t.Fatalf("expected Reachable=false for an unregistered kind")
	}
	if state.Error == "" {
		t.Fatalf("expected a descriptive Error")
	}
}

// TestRegistry_IsPluggable proves the seam T-1702's future plugin SDK
// relies on: registering a brand-new Kind against Registry (as a plugin
// would) requires no change to this package or to any type it exports —
// the caller only ever needs to hold the one-method IngressDiscoverer
// interface.
func TestRegistry_IsPluggable(t *testing.T) {
	plugin := &fakeDiscoverer{state: ProxyState{Reachable: true, Kind: "envoy"}}
	reg := NewDefaultRegistry(nil)
	reg["envoy"] = plugin

	var d IngressDiscoverer = reg
	state, err := d.Discover(context.Background(), Target{Kind: "envoy"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !state.Reachable || plugin.calls != 1 {
		t.Fatalf("expected the plugin discoverer to be invoked, got %+v calls=%d", state, plugin.calls)
	}
	// The four shipped vendors are still present and untouched.
	for _, k := range ValidKinds {
		if _, ok := reg[k]; !ok {
			t.Errorf("expected default registry to retain kind %q after plugin registration", k)
		}
	}
}
