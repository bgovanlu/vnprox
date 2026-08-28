// SPDX-License-Identifier: Apache-2.0

package ipv6

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

type fakeHostReader struct {
	byNode map[string][]host.IPv6RAObservation
}

func (f *fakeHostReader) IPv6RA(_ context.Context, node string) ([]host.IPv6RAObservation, error) {
	return f.byNode[node], nil
}

type fakeGraph struct{ entities []inventory.Entity }

func (g fakeGraph) Snapshot() inventory.Snapshot {
	gr := inventory.NewGraph()
	if len(g.entities) > 0 {
		gr.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, g.entities)
	}
	return gr.Snapshot()
}

func TestSegmentsSingleNodeDualStackVnet(t *testing.T) {
	vnet := &inventory.SdnVnet{
		Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "vnet0"},
		ID:  "vnet0", Zone: "zone1", Tag: 30,
	}
	host1 := &fakeHostReader{byNode: map[string][]host.IPv6RAObservation{
		"pve1": {
			{
				Iface: "vnet0", RAPresent: true, ManagedFlag: true, OtherFlag: false,
				Prefixes: []string{"2001:db8:30::/64"}, RouterLifetimeSec: 1800,
				DHCPv6ServerPresent: true, DHCPv6InferredFromRA: true,
			},
		},
	}}
	svc := NewService(Config{
		Host:      host1,
		LocalNode: func() string { return "pve1" },
		Graph:     fakeGraph{entities: []inventory.Entity{vnet}},
		Now:       func() time.Time { return time.Unix(1000, 0) },
	})

	resp, err := svc.Segments(context.Background())
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if resp.Partial {
		t.Fatalf("expected non-partial response, failedNodes=%v", resp.FailedNodes)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 segment, got %d: %+v", len(resp.Items), resp.Items)
	}
	seg := resp.Items[0]
	if seg.Kind != "vnet" || seg.Vnet != "vnet0" || seg.Vid != 30 || seg.Zone != "zone1" {
		t.Errorf("segment not correlated to vnet: %+v", seg)
	}
	if !seg.RAPresent || !seg.ManagedFlag || !seg.DHCPv6ServerPresent || !seg.DHCPv6InferredFromRA {
		t.Errorf("segment flags not preserved: %+v", seg)
	}
	if len(seg.Prefixes) != 1 || seg.Prefixes[0] != "2001:db8:30::/64" {
		t.Errorf("prefixes not preserved: %+v", seg.Prefixes)
	}
}

func TestSegmentsPeerFailurePartial(t *testing.T) {
	local := &fakeHostReader{byNode: map[string][]host.IPv6RAObservation{
		"pve1": {{Iface: "vmbr0", RAPresent: false}},
	}}
	svc := NewService(Config{
		Host:      local,
		LocalNode: func() string { return "pve1" },
		Peers:     failingPeerSource{},
		Graph:     fakeGraph{},
	})
	resp, err := svc.Segments(context.Background())
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if !resp.Partial {
		t.Fatal("expected partial response when peer discovery fails")
	}
}

var errPeerDiscovery = errors.New("peer discovery unavailable")

type failingPeerSource struct{}

func (failingPeerSource) Peers(context.Context) ([]peer.Peer, error) {
	return nil, errPeerDiscovery
}

func (failingPeerSource) IPv6RA(context.Context, peer.Peer, string) ([]host.IPv6RAObservation, error) {
	return nil, errPeerDiscovery
}
