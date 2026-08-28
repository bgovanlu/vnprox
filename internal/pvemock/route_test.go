// SPDX-License-Identifier: Apache-2.0

// External test package (pvemock_test, not pvemock): internal/route
// itself imports internal/host, which imports internal/pvemock (for
// host.FixtureReader's adapter) — a route_test.go inside package pvemock
// importing internal/route would therefore be a real import cycle
// (pvemock -> route -> host -> pvemock), even though it only exists in a
// test build. As an external test package, this file depends on both
// pvemock and route as an ordinary third party would, which is exactly
// what it's verifying: that FixtureHostReader's route-explorer output is
// real, parseable route.FIBRoute/PolicyRule/RIBRoute data, not merely
// something this package's own (nonexistent) decoder would accept.
package pvemock_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/route"
)

func routeFixturePath(name string) string {
	return filepath.Join("..", "..", "testdata", "clusters", name)
}

// *pvemock.FixtureHostReader satisfies route.Fetcher directly, asserted
// at compile time — the same "small interface, real type satisfies it"
// guarantee internal/route/service.go asserts for *host.Real.
var _ route.Fetcher = (*pvemock.FixtureHostReader)(nil)

// TestFixtureHostReader_RouteTable_ThreeNodeVlan verifies the
// fixture-synthesized FIB round-trips through internal/route's own real
// parser (ParseFIBRoutes) — the exact JSON shape this package emits must
// be exactly what the parser this task validated against pvecube expects,
// not a shape only this package's own decoder would accept.
func TestFixtureHostReader_RouteTable_ThreeNodeVlan(t *testing.T) {
	f, err := pvemock.LoadFixture(routeFixturePath("three-node-vlan.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(f)
	r := pvemock.NewFixtureHostReader(srv)
	ctx := context.Background()

	raw, err := r.RouteTableV4(ctx, "pve1")
	if err != nil {
		t.Fatalf("RouteTableV4(pve1): %v", err)
	}
	fib, err := route.ParseFIBRoutes(raw, route.AFIv4)
	if err != nil {
		t.Fatalf("route.ParseFIBRoutes: %v (raw: %s)", err, raw)
	}

	var haveDefault, haveConnected1020, haveLocalOwn bool
	for _, rt := range fib {
		switch {
		case rt.Dst == "0.0.0.0/0" && rt.Gateway == "10.10.0.1" && rt.Dev == "vmbr0":
			haveDefault = true
		case rt.Dst == "10.10.20.0/24" && rt.Table == "main" && rt.Dev == "vmbr0.20":
			haveConnected1020 = true
		case rt.Dst == "10.10.0.11/32" && rt.Table == "local" && rt.Type == "local":
			haveLocalOwn = true
		}
	}
	if !haveDefault {
		t.Errorf("no default route via 10.10.0.1 dev vmbr0 in %+v", fib)
	}
	if !haveConnected1020 {
		t.Errorf("no connected 10.10.20.0/24 route in %+v", fib)
	}
	if !haveLocalOwn {
		t.Errorf("no local-table host route for pve1's own vmbr0 address in %+v", fib)
	}

	v6Raw, err := r.RouteTableV6(ctx, "pve1")
	if err != nil {
		t.Fatalf("RouteTableV6(pve1): %v", err)
	}
	v6, err := route.ParseFIBRoutes(v6Raw, route.AFIv6)
	if err != nil {
		t.Fatalf("route.ParseFIBRoutes(v6): %v (raw: %s)", err, v6Raw)
	}
	if len(v6) == 0 {
		t.Error("no v6 routes synthesized at all (want at least the per-interface fe80::/64 set)")
	}
}

// TestFixtureHostReader_RouteRules verifies the stock rule set round-trips
// through route.ParsePolicyRules.
func TestFixtureHostReader_RouteRules(t *testing.T) {
	f, err := pvemock.LoadFixture(routeFixturePath("three-node-vlan.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	r := pvemock.NewFixtureHostReader(pvemock.NewServer(f))
	ctx := context.Background()

	raw, err := r.RouteRulesV4(ctx, "pve1")
	if err != nil {
		t.Fatalf("RouteRulesV4: %v", err)
	}
	rules, err := route.ParsePolicyRules(raw, route.AFIv4)
	if err != nil {
		t.Fatalf("route.ParsePolicyRules: %v", err)
	}
	if len(rules) != 3 {
		t.Errorf("got %d v4 rules, want 3", len(rules))
	}

	v6Raw, err := r.RouteRulesV6(ctx, "pve1")
	if err != nil {
		t.Fatalf("RouteRulesV6: %v", err)
	}
	v6Rules, err := route.ParsePolicyRules(v6Raw, route.AFIv6)
	if err != nil {
		t.Fatalf("route.ParsePolicyRules(v6): %v", err)
	}
	if len(v6Rules) != 2 {
		t.Errorf("got %d v6 rules, want 2", len(v6Rules))
	}
}

// TestFixtureHostReader_FRRRIB_EvpnLab verifies a node with a declared
// `frr:` block produces a RIB that round-trips through route.ParseFRRRIB,
// and that a node with none declared reports ErrFRRUnavailable — the same
// convention FRRBGPSummary/FRREVPNVNI already established.
func TestFixtureHostReader_FRRRIB_EvpnLab(t *testing.T) {
	f, err := pvemock.LoadFixture(routeFixturePath("evpn-lab.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	r := pvemock.NewFixtureHostReader(pvemock.NewServer(f))
	ctx := context.Background()

	raw, err := r.FRRRIBV4(ctx, "pve1")
	if err != nil {
		t.Fatalf("FRRRIBV4(pve1): %v", err)
	}
	rib, err := route.ParseFRRRIB(raw, route.AFIv4)
	if err != nil {
		t.Fatalf("route.ParseFRRRIB: %v (raw: %s)", err, raw)
	}
	if len(rib) == 0 {
		t.Error("no RIB routes synthesized for a node with a declared frr: block")
	}
	for _, rt := range rib {
		if rt.VRF != "default" {
			t.Errorf("route %+v: VRF = %q, want default", rt, rt.VRF)
		}
	}
}

func TestFixtureHostReader_FRRRIB_Unavailable(t *testing.T) {
	f, err := pvemock.LoadFixture(routeFixturePath("three-node-vlan.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	r := pvemock.NewFixtureHostReader(pvemock.NewServer(f))
	if _, err := r.FRRRIBV4(context.Background(), "pve1"); !errors.Is(err, pvemock.ErrFRRUnavailable) {
		t.Errorf("FRRRIBV4(node with no frr: block) error = %v, want ErrFRRUnavailable", err)
	}
	if _, err := r.FRRRIBV6(context.Background(), "pve1"); !errors.Is(err, pvemock.ErrFRRUnavailable) {
		t.Errorf("FRRRIBV6(node with no frr: block) error = %v, want ErrFRRUnavailable", err)
	}
}

func TestFixtureHostReader_RouteTable_UnknownNode(t *testing.T) {
	f, err := pvemock.LoadFixture(routeFixturePath("three-node-vlan.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	r := pvemock.NewFixtureHostReader(pvemock.NewServer(f))
	ctx := context.Background()
	if _, err := r.RouteTableV4(ctx, "nope"); !errors.Is(err, pvemock.ErrNotFound) {
		t.Errorf("RouteTableV4(unknown node) error = %v, want ErrNotFound", err)
	}
	if _, err := r.RouteRulesV4(ctx, "nope"); !errors.Is(err, pvemock.ErrNotFound) {
		t.Errorf("RouteRulesV4(unknown node) error = %v, want ErrNotFound", err)
	}
}
