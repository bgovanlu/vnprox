// SPDX-License-Identifier: Apache-2.0

package route

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// fakeFetcher is a hand-rolled Fetcher double, keyed by node — the same
// shape internal/neighbor's/internal/evpn's own service tests use for
// their local-reader fake.
type fakeFetcher struct {
	// frrErr leads: an error interface is two pointer words, a slice is one
	// pointer plus two scalars, so densest-first minimises the bytes govet's
	// fieldalignment counts up to the final pointer.
	frrErr           error
	v4Table, v6Table []byte
	v4Rules, v6Rules []byte
	ribV4, ribV6     []byte
	failV4Table      bool
}

func (f *fakeFetcher) RouteTableV4(context.Context, string) ([]byte, error) {
	if f.failV4Table {
		return nil, errors.New("boom")
	}
	return f.v4Table, nil
}
func (f *fakeFetcher) RouteTableV6(context.Context, string) ([]byte, error) { return f.v6Table, nil }
func (f *fakeFetcher) RouteRulesV4(context.Context, string) ([]byte, error) { return f.v4Rules, nil }
func (f *fakeFetcher) RouteRulesV6(context.Context, string) ([]byte, error) { return f.v6Rules, nil }
func (f *fakeFetcher) FRRRIBV4(context.Context, string) ([]byte, error) {
	if f.frrErr != nil {
		return nil, f.frrErr
	}
	return f.ribV4, nil
}
func (f *fakeFetcher) FRRRIBV6(context.Context, string) ([]byte, error) {
	if f.frrErr != nil {
		return nil, f.frrErr
	}
	return f.ribV6, nil
}

// fakePeerSource is a hand-rolled PeerSource double routing every call to
// a single node-keyed fakeFetcher map, mirroring the local Fetcher's
// signatures with an extra peer.Peer argument.
type fakePeerSource struct {
	peerErr error
	byNode  map[string]*fakeFetcher
	peers   []peer.Peer
}

func (p *fakePeerSource) Peers(context.Context) ([]peer.Peer, error) {
	if p.peerErr != nil {
		return nil, p.peerErr
	}
	return p.peers, nil
}
func (p *fakePeerSource) RouteTableV4(ctx context.Context, pr peer.Peer, node string) ([]byte, error) {
	return p.byNode[node].RouteTableV4(ctx, node)
}
func (p *fakePeerSource) RouteTableV6(ctx context.Context, pr peer.Peer, node string) ([]byte, error) {
	return p.byNode[node].RouteTableV6(ctx, node)
}
func (p *fakePeerSource) RouteRulesV4(ctx context.Context, pr peer.Peer, node string) ([]byte, error) {
	return p.byNode[node].RouteRulesV4(ctx, node)
}
func (p *fakePeerSource) RouteRulesV6(ctx context.Context, pr peer.Peer, node string) ([]byte, error) {
	return p.byNode[node].RouteRulesV6(ctx, node)
}
func (p *fakePeerSource) FRRRIBV4(ctx context.Context, pr peer.Peer, node string) (bool, []byte, error) {
	f := p.byNode[node]
	raw, err := f.FRRRIBV4(ctx, node)
	if err != nil {
		if errors.Is(err, host.ErrFRRUnavailable) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, raw, nil
}
func (p *fakePeerSource) FRRRIBV6(ctx context.Context, pr peer.Peer, node string) (bool, []byte, error) {
	f := p.byNode[node]
	raw, err := f.FRRRIBV6(ctx, node)
	if err != nil {
		if errors.Is(err, host.ErrFRRUnavailable) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, raw, nil
}

func localFetcher() *fakeFetcher {
	return &fakeFetcher{
		v4Table: []byte(pvecubeRouteTableV4),
		v6Table: []byte(pvecubeRouteTableV6),
		v4Rules: []byte(pvecubeRulesV4),
		v6Rules: []byte(pvecubeRulesV6),
		frrErr:  host.ErrFRRUnavailable,
	}
}

func TestService_Snapshot_local_frrUnavailable(t *testing.T) {
	svc := NewService(Config{
		Host:      localFetcher(),
		LocalNode: func() string { return "pvecube" },
	})
	snap, err := svc.Snapshot(context.Background(), "pvecube")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Node != "pvecube" {
		t.Errorf("Node = %q", snap.Node)
	}
	if !snap.FRRUnavailable || snap.RIB != nil {
		t.Errorf("FRRUnavailable = %v, RIB = %v; want FRRUnavailable true, RIB nil", snap.FRRUnavailable, snap.RIB)
	}
	if len(snap.FIB) != 6+5 {
		t.Errorf("len(FIB) = %d, want 11 (6 v4 + 5 v6 from the evidence fixture)", len(snap.FIB))
	}
	if len(snap.Rules) != 3+2 {
		t.Errorf("len(Rules) = %d, want 5 (3 v4 + 2 v6)", len(snap.Rules))
	}
}

func TestService_Snapshot_frrAvailable(t *testing.T) {
	f := localFetcher()
	f.frrErr = nil
	f.ribV4 = []byte(pvecubeRIBv4)
	f.ribV6 = []byte(`{}`)
	svc := NewService(Config{Host: f, LocalNode: func() string { return "pvecube" }})
	snap, err := svc.Snapshot(context.Background(), "pvecube")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.FRRUnavailable {
		t.Error("FRRUnavailable = true, want false")
	}
	if len(snap.RIB) != 2 {
		t.Errorf("len(RIB) = %d, want 2", len(snap.RIB))
	}
}

func TestService_Snapshot_fetchFailure(t *testing.T) {
	f := localFetcher()
	f.failV4Table = true
	svc := NewService(Config{Host: f, LocalNode: func() string { return "pvecube" }})
	if _, err := svc.Snapshot(context.Background(), "pvecube"); err == nil {
		t.Error("Snapshot with a failing FIB read succeeded, want error")
	}
}

func TestService_Snapshot_peerFanOut(t *testing.T) {
	peerFetcher := localFetcher()
	svc := NewService(Config{
		Host:      localFetcher(),
		LocalNode: func() string { return "pvecube" },
		Peers: &fakePeerSource{
			peers:  []peer.Peer{{Node: "pve001"}},
			byNode: map[string]*fakeFetcher{"pve001": peerFetcher},
		},
	})
	snap, err := svc.Snapshot(context.Background(), "pve001")
	if err != nil {
		t.Fatalf("Snapshot(peer node): %v", err)
	}
	if snap.Node != "pve001" {
		t.Errorf("Node = %q", snap.Node)
	}
	if len(snap.FIB) != 11 {
		t.Errorf("len(FIB) = %d, want 11", len(snap.FIB))
	}
}

func TestService_Snapshot_unknownNode(t *testing.T) {
	svc := NewService(Config{
		Host:      localFetcher(),
		LocalNode: func() string { return "pvecube" },
		Peers:     &fakePeerSource{},
	})
	_, err := svc.Snapshot(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("Snapshot(unknown node) error = %v, want ErrNodeNotFound", err)
	}
}

func TestService_Nodes(t *testing.T) {
	svc := NewService(Config{
		Host:      localFetcher(),
		LocalNode: func() string { return "pvecube" },
		Peers:     &fakePeerSource{peers: []peer.Peer{{Node: "pve001"}}},
	})
	nodes := svc.Nodes(context.Background())
	if len(nodes) != 2 || nodes[0] != "pve001" || nodes[1] != "pvecube" {
		t.Errorf("Nodes() = %v, want [pve001 pvecube] (sorted)", nodes)
	}
}

func TestService_Lookup(t *testing.T) {
	svc := NewService(Config{Host: localFetcher(), LocalNode: func() string { return "pvecube" }})
	res, err := svc.Lookup(context.Background(), "pvecube", "8.8.8.8", "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !res.Reachable || res.MatchedRoute.Dev != "vmbr0" {
		t.Errorf("Lookup(8.8.8.8) = %+v", res)
	}
}
