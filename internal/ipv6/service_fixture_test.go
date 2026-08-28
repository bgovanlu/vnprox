// SPDX-License-Identifier: Apache-2.0

package ipv6

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// buildFixtureGraph runs one full poll cycle against testdata/clusters/
// ipv6-dualstack.yaml (T-1404's own fixture — see that file's doc comment
// for its three scenarios) and returns the resulting real Graph plus a
// host.Reader backed by the same fixture, the same pvemock -> collect ->
// inventory.Graph pipeline internal/topology's own test helpers use.
func buildFixtureGraph(t *testing.T) (*collect.Config, func()) {
	t.Helper()
	f, err := pvemock.LoadFixture("../../testdata/clusters/ipv6-dualstack.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)

	c, err := pve.New(pve.Config{
		APIURL: ts.URL, Auth: pve.AuthTicket,
		Username: "root@pam", Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	graph := inventory.NewGraph()
	reader := host.NewFixtureReader(pvemock.NewFixtureHostReader(srv))
	cfg := &collect.Config{PVE: c, Host: reader, Graph: graph}
	coll, err := collect.New(*cfg)
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := coll.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	return cfg, ts.Close
}

// TestSegments_IPv6DualstackFixture_GoldenScenarios is T-1404 acceptance
// criterion 1: GET /ipv6/segments (here exercised at the Service layer
// that route directly wraps) against the ipv6-dualstack fixture returns
// RA/SLAAC/DHCPv6 visibility matching each of its three declared segments.
func TestSegments_IPv6DualstackFixture_GoldenScenarios(t *testing.T) {
	cfg, closeSrv := buildFixtureGraph(t)
	defer closeSrv()

	svc := NewService(Config{
		Host:      cfg.Host,
		LocalNode: func() string { return "pve1" },
		Graph:     cfg.Graph,
	})

	resp, err := svc.Segments(context.Background())
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if resp.Partial {
		t.Fatalf("expected a clean single-node response, failedNodes=%v", resp.FailedNodes)
	}

	byIface := map[string]Segment{}
	for _, s := range resp.Items {
		byIface[s.Iface] = s
	}

	// vnet20: healthy dual-stack — RA present, M=1, one prefix, correlated
	// to the vnet.
	v20, ok := byIface["vnet20"]
	if !ok {
		t.Fatalf("no segment for vnet20, got %+v", resp.Items)
	}
	if !v20.RAPresent || !v20.ManagedFlag || v20.OtherFlag {
		t.Errorf("vnet20 flags = %+v, want RAPresent=true ManagedFlag=true OtherFlag=false", v20)
	}
	if len(v20.Prefixes) != 1 || v20.Prefixes[0] != "2001:db8:70::/64" {
		t.Errorf("vnet20 prefixes = %v", v20.Prefixes)
	}
	if v20.Kind != "vnet" || v20.Vnet != "vnet20" {
		t.Errorf("vnet20 not correlated to its SDN VNet: %+v", v20)
	}
	if !v20.DHCPv6ServerPresent || !v20.DHCPv6InferredFromRA {
		t.Errorf("vnet20 DHCPv6 server presence should be inferred true from M=1: %+v", v20)
	}

	// vnet21: v6-broken — no RA observed at all on this segment (the
	// symptom itself: an interface with nothing in host.Reader.IPv6RA's
	// result simply produces no segment entry, rather than a fabricated
	// false-RA row).
	if v21, present := byIface["vnet21"]; present {
		t.Errorf("vnet21 should have no RA observation (the v6-broken symptom), got %+v", v21)
	}

	// vnet22: DHCPv6-PD-from-upstream — RA present, M=1, same visibility
	// shape as vnet20 (vnprox never distinguishes "whose DHCPv6 server" at
	// this layer, only that one is expected).
	v22, ok := byIface["vnet22"]
	if !ok {
		t.Fatalf("no segment for vnet22, got %+v", resp.Items)
	}
	if !v22.RAPresent || !v22.ManagedFlag || !v22.DHCPv6ServerPresent {
		t.Errorf("vnet22 flags = %+v, want RAPresent=true ManagedFlag=true DHCPv6ServerPresent=true", v22)
	}
}

// TestDualstackDrift_IPv6DualstackFixture is T-1404 acceptance criterion 2,
// exercised against the same real pvemock fixture (rather than a
// hand-built snapshot, internal/findings' own health_dualstack_test.go
// covers that): vnet21 (v4 SNAT'd, v6 not, no exit node) fires exactly one
// dualstack_drift finding naming both verdicts; the healthy vnet20 raises
// none.
func TestDualstackDrift_IPv6DualstackFixture(t *testing.T) {
	cfg, closeSrv := buildFixtureGraph(t)
	defer closeSrv()

	eng := findings.New(findings.Config{Graph: cfg.Graph})
	var drift []findings.Finding
	for _, f := range eng.Findings() {
		if f.Check == findings.CheckDualstackDrift {
			drift = append(drift, f)
		}
	}
	if len(drift) != 1 {
		t.Fatalf("got %d dualstack_drift findings, want 1: %+v", len(drift), drift)
	}
	f := drift[0]
	for _, want := range []string{"vnet21", "allow"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q missing %q", f.Detail, want)
		}
	}
}
