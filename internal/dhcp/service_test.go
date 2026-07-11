package dhcp

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// fakeHost is a hand-rolled nodeLeaseReader test double.
type fakeHost struct {
	leases map[string][]byte
	errs   map[string]error
}

func (f *fakeHost) DHCPLeases(_ context.Context, node string) ([]byte, error) {
	if err, ok := f.errs[node]; ok {
		return nil, err
	}
	return f.leases[node], nil
}

// fakePeerSource is a hand-rolled PeerSource test double.
type fakePeerSource struct {
	peersErr    error
	leases      map[string][]byte
	unreachable map[string]bool
	peers       []peer.Peer
}

func (f *fakePeerSource) Peers(context.Context) ([]peer.Peer, error) {
	return f.peers, f.peersErr
}

func (f *fakePeerSource) DHCPLeases(_ context.Context, p peer.Peer, _ string) ([]byte, error) {
	if f.unreachable[p.Node] {
		return nil, errors.New("peer unreachable")
	}
	return f.leases[p.Node], nil
}

// TestService_Leases_LocalOnly is the single-node/no-peers case: local
// node's raw lease content parses into Observations tagged
// Source: "dhcp-lease".
func TestService_Leases_LocalOnly(t *testing.T) {
	h := &fakeHost{leases: map[string][]byte{
		"pve1": []byte("1735689600 aa:bb:cc:dd:ee:01 10.50.0.150 web1 *\ngarbage-line\n"),
	}}
	svc := NewService(Config{Host: h, LocalNode: func() string { return "pve1" }})

	obs, err := svc.Leases(context.Background())
	if err != nil {
		t.Fatalf("Leases: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("obs = %+v, want 1 (the malformed line skipped)", obs)
	}
	if obs[0].IP != "10.50.0.150" || obs[0].MAC != "aa:bb:cc:dd:ee:01" || obs[0].Hostname != "web1" || obs[0].Source != "dhcp-lease" {
		t.Errorf("obs[0] = %+v", obs[0])
	}
}

// TestService_Leases_ClusterFanOut is T-406's "leases reader per node via
// peer API": the local node plus two peers are all fetched, one peer's
// unreachable failure doesn't blank the other's leases.
func TestService_Leases_ClusterFanOut(t *testing.T) {
	h := &fakeHost{leases: map[string][]byte{
		"pve1": []byte("1735689600 aa:bb:cc:dd:ee:01 10.50.0.10 web1 *\n"),
	}}
	peers := &fakePeerSource{
		peers: []peer.Peer{
			{Node: "pve1", Addr: "10.0.0.1:8007"}, // same as local, must be skipped
			{Node: "pve2", Addr: "10.0.0.2:8007"},
			{Node: "pve3", Addr: "10.0.0.3:8007"},
		},
		leases: map[string][]byte{
			"pve2": []byte("1735689700 aa:bb:cc:dd:ee:02 10.50.0.20 web2 *\n"),
		},
		unreachable: map[string]bool{"pve3": true},
	}
	svc := NewService(Config{Host: h, Peers: peers, LocalNode: func() string { return "pve1" }})

	obs, err := svc.Leases(context.Background())
	if err != nil {
		t.Fatalf("Leases: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("obs = %+v, want 2 (local pve1 + peer pve2; pve3 unreachable, pve1-as-peer deduped)", obs)
	}
	byIP := map[string]ipam.Observation{}
	for _, o := range obs {
		byIP[o.IP] = o
	}
	if _, ok := byIP["10.50.0.10"]; !ok {
		t.Error("missing local pve1's lease")
	}
	if _, ok := byIP["10.50.0.20"]; !ok {
		t.Error("missing peer pve2's lease")
	}
}

// TestService_Leases_PeerDiscoveryFailure_StillReturnsLocal verifies a
// failed Peers() call degrades to local-only rather than erroring the
// whole call.
func TestService_Leases_PeerDiscoveryFailure_StillReturnsLocal(t *testing.T) {
	h := &fakeHost{leases: map[string][]byte{
		"pve1": []byte("1735689600 aa:bb:cc:dd:ee:01 10.50.0.10 web1 *\n"),
	}}
	peers := &fakePeerSource{peersErr: errors.New("discovery failed")}
	svc := NewService(Config{Host: h, Peers: peers, LocalNode: func() string { return "pve1" }})

	obs, err := svc.Leases(context.Background())
	if err != nil {
		t.Fatalf("Leases: %v", err)
	}
	if len(obs) != 1 || obs[0].IP != "10.50.0.10" {
		t.Fatalf("obs = %+v, want just the local lease", obs)
	}
}

// TestService_Leases_NoLocalNodeYet_NoPeers is the daemon-just-started
// case (local node not yet discovered, no peers configured): a clean
// empty result, not an error.
func TestService_Leases_NoLocalNodeYet_NoPeers(t *testing.T) {
	svc := NewService(Config{Host: &fakeHost{}, LocalNode: func() string { return "" }})
	obs, err := svc.Leases(context.Background())
	if err != nil {
		t.Fatalf("Leases: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("obs = %+v, want empty", obs)
	}
}
