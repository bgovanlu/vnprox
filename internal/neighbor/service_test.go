package neighbor

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// fakeHost is a hand-rolled nodeNeighborReader test double.
type fakeHost struct {
	neighbors map[string][]host.Neighbor
	errs      map[string]error
}

func (f *fakeHost) Neighbors(_ context.Context, node string) ([]host.Neighbor, error) {
	if err, ok := f.errs[node]; ok {
		return nil, err
	}
	return f.neighbors[node], nil
}

// fakePeerSource is a hand-rolled PeerSource test double.
type fakePeerSource struct {
	peersErr    error
	neighbors   map[string][]host.Neighbor
	unreachable map[string]bool
	peers       []peer.Peer
}

func (f *fakePeerSource) Peers(context.Context) ([]peer.Peer, error) {
	return f.peers, f.peersErr
}

func (f *fakePeerSource) Neighbors(_ context.Context, p peer.Peer, _ string) ([]host.Neighbor, error) {
	if f.unreachable[p.Node] {
		return nil, errors.New("peer unreachable")
	}
	return f.neighbors[p.Node], nil
}

// TestService_Neighbors_LocalOnly is the single-node/no-peers case: local
// node's resolved neighbor table converts into Observations tagged
// Source: "neighbor".
func TestService_Neighbors_LocalOnly(t *testing.T) {
	h := &fakeHost{neighbors: map[string][]host.Neighbor{
		"pve1": {{IP: "10.50.0.55", MAC: "aa:bb:cc:dd:ee:01", Iface: "vmbr0", State: host.NeighborReachable}},
	}}
	svc := NewService(Config{Host: h, LocalNode: func() string { return "pve1" }})

	obs, err := svc.Neighbors(context.Background())
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("obs = %+v, want 1", obs)
	}
	if obs[0].IP != "10.50.0.55" || obs[0].MAC != "aa:bb:cc:dd:ee:01" || obs[0].Source != "neighbor" {
		t.Errorf("obs[0] = %+v", obs[0])
	}
}

// TestService_Neighbors_ClusterFanOut is T-805 acceptance criterion 5's
// fan-out half: the local node plus two peers are all fetched, one peer's
// unreachable failure doesn't blank the other's neighbors.
func TestService_Neighbors_ClusterFanOut(t *testing.T) {
	h := &fakeHost{neighbors: map[string][]host.Neighbor{
		"pve1": {{IP: "10.50.0.10", MAC: "aa:bb:cc:dd:ee:01", Iface: "vmbr0", State: host.NeighborReachable}},
	}}
	peers := &fakePeerSource{
		peers: []peer.Peer{
			{Node: "pve1", Addr: "10.0.0.1:8007"}, // same as local, must be skipped
			{Node: "pve2", Addr: "10.0.0.2:8007"},
			{Node: "pve3", Addr: "10.0.0.3:8007"},
		},
		neighbors: map[string][]host.Neighbor{
			"pve2": {{IP: "10.50.0.20", MAC: "aa:bb:cc:dd:ee:02", Iface: "vmbr0", State: host.NeighborStale}},
		},
		unreachable: map[string]bool{"pve3": true},
	}
	svc := NewService(Config{Host: h, Peers: peers, LocalNode: func() string { return "pve1" }})

	obs, err := svc.Neighbors(context.Background())
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("obs = %+v, want 2 (local pve1 + peer pve2; pve3 unreachable, pve1-as-peer deduped)", obs)
	}
	byIP := map[string]ipam.Observation{}
	for _, o := range obs {
		byIP[o.IP] = o
	}
	if _, ok := byIP["10.50.0.10"]; !ok {
		t.Error("missing local pve1's neighbor")
	}
	if _, ok := byIP["10.50.0.20"]; !ok {
		t.Error("missing peer pve2's neighbor (degraded pve3 must not blank it)")
	}
}

// TestService_Neighbors_PeerDiscoveryFailure_StillReturnsLocal verifies a
// failed Peers() call degrades to local-only rather than erroring the
// whole call.
func TestService_Neighbors_PeerDiscoveryFailure_StillReturnsLocal(t *testing.T) {
	h := &fakeHost{neighbors: map[string][]host.Neighbor{
		"pve1": {{IP: "10.50.0.10", MAC: "aa:bb:cc:dd:ee:01", State: host.NeighborReachable}},
	}}
	peers := &fakePeerSource{peersErr: errors.New("discovery failed")}
	svc := NewService(Config{Host: h, Peers: peers, LocalNode: func() string { return "pve1" }})

	obs, err := svc.Neighbors(context.Background())
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(obs) != 1 || obs[0].IP != "10.50.0.10" {
		t.Fatalf("obs = %+v, want just the local neighbor", obs)
	}
}

// TestService_Neighbors_NoLocalNodeYet_NoPeers is the daemon-just-started
// case (local node not yet discovered, no peers configured): a clean empty
// result, not an error.
func TestService_Neighbors_NoLocalNodeYet_NoPeers(t *testing.T) {
	svc := NewService(Config{Host: &fakeHost{}, LocalNode: func() string { return "" }})
	obs, err := svc.Neighbors(context.Background())
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("obs = %+v, want empty", obs)
	}
}
