// SPDX-License-Identifier: Apache-2.0

package flow_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// This file is T-1002's flow-lab end-to-end resolver test: load the real
// testdata/clusters/flow-lab.yaml pvemock fixture, poll it into a live
// *inventory.Graph exactly the way cmd/vnproxd's own collectors do, refresh
// a flow.GraphResolver from that graph, and confirm the golden .bin
// fixtures' embedded IPs resolve against the real bridges/subnets
// flow-lab.yaml declares — never guessed, per GraphResolver's own
// contract. Mirrors internal/collect's own test-helper pattern (ticket auth
// against pvemock, short poll intervals, wait for the graph to populate).

func TestFlowLab_GraphResolver_ResolvesGoldenFixtureIPs(t *testing.T) {
	f, err := pvemock.LoadFixture("../../testdata/clusters/flow-lab.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	pveClient, err := pve.New(pve.Config{
		APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	graph := inventory.NewGraph()
	collector, err := collect.New(collect.Config{
		PVE:          pveClient,
		Host:         host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph:        graph,
		PVEInterval:  20 * time.Millisecond,
		HostInterval: 20 * time.Millisecond,
		LLDPInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = collector.RunPVELoop(ctx) }()
	go func() { _ = collector.RunHostLoop(ctx) }()

	// Wait until both nodes' bridges AND the SDN subnet are visible in the
	// graph (proof the fixture actually loaded and both poll loops — host
	// and PVE, which carries SDN — completed at least once, not an empty/
	// partial snapshot).
	deadline := time.Now().Add(5 * time.Second)
	for {
		all := graph.Snapshot().All()
		var bridgeCount int
		var sawSubnet bool
		for _, e := range all {
			switch v := e.(type) {
			case *inventory.Bridge:
				if len(v.Addresses) > 0 {
					bridgeCount++
				}
			case *inventory.SdnSubnet:
				sawSubnet = true
			}
		}
		if bridgeCount >= 2 && sawSubnet {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for both nodes' addressed bridges and the SDN subnet to appear in the graph (bridges=%d, subnet=%v)", bridgeCount, sawSubnet)
		}
		time.Sleep(10 * time.Millisecond)
	}

	resolver := flow.NewGraphResolver()
	resolver.RefreshFromGraph(graph)

	tests := []struct {
		fixture string
		ip      string
		wantRef string
	}{
		// netflow5_basic.bin: 10.0.0.5 -> 10.0.0.10, both within pve1's
		// vmbr0 (10.0.0.0/24).
		{"netflow5_basic.bin", "10.0.0.5", "bridge:pve1:vmbr0"},
		{"netflow5_basic.bin", "10.0.0.10", "bridge:pve1:vmbr0"},
		// sflow5_basic.bin: 10.1.1.5 -> 10.1.1.50, both within pve2's
		// vmbr0 (10.1.1.0/24).
		{"sflow5_basic.bin", "10.1.1.5", "bridge:pve2:vmbr0"},
		{"sflow5_basic.bin", "10.1.1.50", "bridge:pve2:vmbr0"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+"/"+tt.ip, func(t *testing.T) {
			ref, ok := resolver.Resolve(tt.ip)
			if !ok {
				t.Fatalf("Resolve(%q) did not resolve; want %q", tt.ip, tt.wantRef)
			}
			if ref != tt.wantRef {
				t.Errorf("Resolve(%q) = %q, want %q", tt.ip, ref, tt.wantRef)
			}
		})
	}

	// An IP the fixture never declares (8.8.8.8, netflow5_basic.bin's
	// external destination) must never resolve — proving the "never
	// guessed" contract end-to-end, not just at the unit level.
	if ref, ok := resolver.Resolve("8.8.8.8"); ok {
		t.Errorf("Resolve(8.8.8.8) = %q, want unresolved (no fixture subnet covers it)", ref)
	}

	// The SDN subnet (10.100.0.0/24) resolves to its owning vnet's Ref —
	// GraphResolver's documented cluster-scoped-subnet path. The vnet Ref's
	// ID is the documented "zone/vnet" composite (docs/data-model.md §1's
	// "sdn-vnet::zone1/vnet1" example), not the bare vnet name.
	const wantVnetRef = "sdn-vnet::vlanz/vnet100"
	if ref, ok := resolver.Resolve("10.100.0.42"); !ok || ref != wantVnetRef {
		t.Errorf("Resolve(10.100.0.42) = (%q, %v), want (%q, true)", ref, ok, wantVnetRef)
	}
}
