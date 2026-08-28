// SPDX-License-Identifier: Apache-2.0

package evpn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/sdn"
)

// fakeHost is a hand-rolled nodeFRRReader test double, matching
// docs/development.md's table-driven-test convention (internal/sdn's
// fakeReader is the precedent this mirrors).
type fakeHost struct {
	bgp    map[string][]byte
	evpn   map[string][]byte
	bgpErr map[string]error
}

func (f *fakeHost) FRRBGPSummary(_ context.Context, node string) ([]byte, error) {
	if err, ok := f.bgpErr[node]; ok {
		return nil, err
	}
	b, ok := f.bgp[node]
	if !ok {
		return nil, host.ErrFRRUnavailable
	}
	return b, nil
}

func (f *fakeHost) FRREVPNVNI(_ context.Context, node string) ([]byte, error) {
	b, ok := f.evpn[node]
	if !ok {
		return nil, host.ErrFRRUnavailable
	}
	return b, nil
}

// fakePeerSource is a hand-rolled PeerSource test double.
type fakePeerSource struct {
	peersErr    error
	bgp         map[string][]byte
	evpnVNI     map[string][]byte
	unreachable map[string]bool
	peers       []peer.Peer
}

func (f *fakePeerSource) Peers(context.Context) ([]peer.Peer, error) {
	return f.peers, f.peersErr
}

func (f *fakePeerSource) FRRBGPSummary(_ context.Context, p peer.Peer, _ string) (bool, []byte, error) {
	if f.unreachable[p.Node] {
		return false, nil, errors.New("peer unreachable")
	}
	b, ok := f.bgp[p.Node]
	if !ok {
		return false, nil, nil
	}
	return true, b, nil
}

func (f *fakePeerSource) FRREVPNVNI(_ context.Context, p peer.Peer, _ string) (bool, []byte, error) {
	if f.unreachable[p.Node] {
		return false, nil, errors.New("peer unreachable")
	}
	b, ok := f.evpnVNI[p.Node]
	if !ok {
		return true, []byte(`{}`), nil
	}
	return true, b, nil
}

func establishedBGP(peerAddr, hostname string) []byte {
	return []byte(`{"l2VpnEvpn":{"routerId":"10.0.0.1","as":65001,"peers":{"` + peerAddr + `":{"hostname":"` + hostname + `","remoteAs":65001,"pfxRcd":4,"pfxSnt":3,"peerUptime":"01:00:00","state":"Established"}}}}`)
}

func stateBGP(peerAddr, hostname, state string) []byte {
	return []byte(`{"l2VpnEvpn":{"routerId":"10.0.0.1","as":65001,"peers":{"` + peerAddr + `":{"hostname":"` + hostname + `","remoteAs":65001,"pfxRcd":0,"pfxSnt":0,"peerUptime":"never","state":"` + state + `"}}}}`)
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestStatus_MatrixRendersStatesAndSessionDetail(t *testing.T) {
	h := &fakeHost{
		bgp: map[string][]byte{
			"pve1": []byte(`{"l2VpnEvpn":{"routerId":"10.20.0.11","as":65001,"peers":{
				"10.20.0.12":{"hostname":"pve2","remoteAs":65001,"pfxRcd":6,"pfxSnt":6,"peerUptime":"01:23:45","state":"Established"},
				"10.20.0.13":{"hostname":"pve3","remoteAs":65001,"pfxRcd":0,"pfxSnt":0,"peerUptime":"never","state":"Active"}
			}}}`),
		},
		evpn: map[string][]byte{
			"pve1": []byte(`{"10001":{"vni":10001,"type":"L2","vxlanIf":"vxlan10001","numMacs":12,"numArpNd":4}}`),
		},
	}
	svc := NewService(Config{
		Host:      h,
		LocalNode: func() string { return "pve1" },
		Now:       fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	})

	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Nodes) != 1 {
		t.Fatalf("len(Nodes) = %d, want 1", len(status.Nodes))
	}
	ns := status.Nodes[0]
	if ns.Node != "pve1" || !ns.FRRInstalled {
		t.Fatalf("node = %+v, want pve1/installed", ns)
	}
	if len(ns.Peers) != 2 {
		t.Fatalf("len(Peers) = %d, want 2", len(ns.Peers))
	}
	byAddr := map[string]Peer{}
	for _, p := range ns.Peers {
		byAddr[p.PeerAddr] = p
	}
	established, ok := byAddr["10.20.0.12"]
	if !ok || established.State != "Established" || established.PeerNode != "pve2" || established.PfxRcd != 6 {
		t.Errorf("10.20.0.12 = %+v, want Established/pve2/pfxRcd=6", established)
	}
	if established.UptimeSecs != 3600+23*60+45 {
		t.Errorf("UptimeSecs = %d, want %d", established.UptimeSecs, 3600+23*60+45)
	}
	active, ok := byAddr["10.20.0.13"]
	if !ok || active.State != "Active" {
		t.Errorf("10.20.0.13 = %+v, want Active", active)
	}
	if len(ns.VNIs) != 1 || ns.VNIs[0].VNI != 10001 || ns.VNIs[0].Type != "L2" {
		t.Errorf("VNIs = %+v, want one L2 VNI 10001", ns.VNIs)
	}
	if status.Partial {
		t.Error("Partial = true, want false")
	}
}

// TestStatus_AbsentFRR_ReportsCleanNoEVPN is T-404 AC2.
func TestStatus_AbsentFRR_ReportsCleanNoEVPN(t *testing.T) {
	h := &fakeHost{} // no bgp/evpn entries at all -> ErrFRRUnavailable
	svc := NewService(Config{
		Host:      h,
		LocalNode: func() string { return "pve1" },
		Now:       fixedClock(time.Now()),
	})

	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Nodes) != 1 {
		t.Fatalf("len(Nodes) = %d, want 1", len(status.Nodes))
	}
	ns := status.Nodes[0]
	if ns.FRRInstalled {
		t.Error("FRRInstalled = true, want false")
	}
	if ns.Error != "" {
		t.Errorf("Error = %q, want empty (absent FRR is not a failure)", ns.Error)
	}
	if status.Partial {
		t.Error("Partial = true, want false: absent FRR must not count as a fan-out failure")
	}
	if len(ns.Peers) != 0 {
		t.Errorf("len(Peers) = %d, want 0", len(ns.Peers))
	}
	// Peers/VNIs must be non-nil even with no FRR: a nil slice marshals to
	// JSON `null`, which the EVPN view iterates directly (buildEvpnMatrix's
	// `for..of node.peers`, VniList's `node.vnis.map`) and blanks the whole
	// page. They must serialize as `[]`.
	if ns.Peers == nil {
		t.Error("Peers is nil, want an empty (non-nil) slice so it marshals as [] not null")
	}
	if ns.VNIs == nil {
		t.Error("VNIs is nil, want an empty (non-nil) slice so it marshals as [] not null")
	}
	// Prove it end to end at the JSON layer — the actual wire contract the
	// frontend consumes.
	blob, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if bytes.Contains(blob, []byte(`"peers":null`)) || bytes.Contains(blob, []byte(`"vnis":null`)) {
		t.Errorf("status JSON contains a null peers/vnis array: %s", blob)
	}
}

// TestStatus_PeerUnreachable_ReportsPartial verifies a genuinely broken
// peer read (as opposed to a clean "no FRR") is surfaced as a fetch
// failure, distinct from AC2's absent-FRR case.
func TestStatus_PeerUnreachable_ReportsPartial(t *testing.T) {
	h := &fakeHost{bgp: map[string][]byte{"pve1": establishedBGP("10.20.0.12", "pve2")}, evpn: map[string][]byte{"pve1": []byte(`{}`)}}
	peers := &fakePeerSource{
		peers:       []peer.Peer{{Node: "pve2", Addr: "10.20.0.12:8007"}},
		unreachable: map[string]bool{"pve2": true},
	}
	svc := NewService(Config{
		Host:      h,
		Peers:     peers,
		LocalNode: func() string { return "pve1" },
		Now:       fixedClock(time.Now()),
	})

	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Partial {
		t.Error("Partial = false, want true")
	}
	found := false
	for _, n := range status.FailedNodes {
		if n == "pve2" {
			found = true
		}
	}
	if !found {
		t.Errorf("FailedNodes = %v, want to include pve2", status.FailedNodes)
	}
}

// TestStatus_ClusterFanOut verifies local + peer nodes are both included.
func TestStatus_ClusterFanOut(t *testing.T) {
	h := &fakeHost{bgp: map[string][]byte{"pve1": establishedBGP("10.20.0.12", "pve2")}, evpn: map[string][]byte{}}
	peers := &fakePeerSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.20.0.12:8007"}, {Node: "pve3", Addr: "10.20.0.13:8007"}},
		bgp:   map[string][]byte{"pve2": establishedBGP("10.20.0.11", "pve1")},
		// pve3 deliberately absent from bgp map -> reported unavailable.
	}
	svc := NewService(Config{
		Host:      h,
		Peers:     peers,
		LocalNode: func() string { return "pve1" },
		Now:       fixedClock(time.Now()),
	})

	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Nodes) != 3 {
		t.Fatalf("len(Nodes) = %d, want 3 (pve1, pve2, pve3)", len(status.Nodes))
	}
	byNode := map[string]NodeStatus{}
	for _, n := range status.Nodes {
		byNode[n.Node] = n
	}
	if !byNode["pve1"].FRRInstalled || !byNode["pve2"].FRRInstalled {
		t.Errorf("pve1/pve2 should both report FRR installed: %+v / %+v", byNode["pve1"], byNode["pve2"])
	}
	if byNode["pve3"].FRRInstalled {
		t.Error("pve3 should report FRR not installed")
	}
	if status.Partial {
		t.Error("Partial = true, want false (pve3 absent-FRR is not a failure)")
	}
}

// TestStatus_PeerDiscoveryFailed_ReportsPartial verifies a Peers() error
// (as opposed to an individual peer's read failing) is also surfaced.
func TestStatus_PeerDiscoveryFailed_ReportsPartial(t *testing.T) {
	h := &fakeHost{bgp: map[string][]byte{"pve1": []byte(`{}`)}}
	peers := &fakePeerSource{peersErr: errors.New("cluster status unreachable")}
	svc := NewService(Config{
		Host:      h,
		Peers:     peers,
		LocalNode: func() string { return "pve1" },
		Now:       fixedClock(time.Now()),
	})
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Partial {
		t.Error("Partial = false, want true")
	}
}

// TestStatus_FlappingSession_RaisesFinding is T-404 AC3, exercised at the
// Service level: repeated Status() calls with a scripted oscillating peer
// state must eventually surface a Finding; a stable peer observed the
// same number of times must not.
func TestStatus_FlappingSession_RaisesFinding(t *testing.T) {
	h := &fakeHost{evpn: map[string][]byte{"pve1": []byte(`{}`)}}
	svc := NewService(Config{
		Host:       h,
		LocalNode:  func() string { return "pve1" },
		FlapWindow: 10 * time.Minute,
		FlapThresh: 3,
	})

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	states := []string{"Established", "Idle", "Established", "Idle", "Established"}
	var last Status
	for i, state := range states {
		h.bgp = map[string][]byte{"pve1": stateBGP("10.20.0.12", "pve2", state)}
		svc.cfg.Now = fixedClock(base.Add(time.Duration(i) * time.Minute))
		status, err := svc.Status(context.Background())
		if err != nil {
			t.Fatalf("Status(#%d): %v", i, err)
		}
		last = status
	}
	if len(last.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1 for the flapping session; findings=%+v", len(last.Findings), last.Findings)
	}
	f := last.Findings[0]
	if f.Code != "evpn_bgp_flapping" || f.Node != "pve1" || f.PeerAddr != "10.20.0.12" {
		t.Errorf("finding = %+v, unexpected", f)
	}
}

func TestStatus_StableSession_NoFinding(t *testing.T) {
	h := &fakeHost{evpn: map[string][]byte{"pve1": []byte(`{}`)}}
	svc := NewService(Config{
		Host:       h,
		LocalNode:  func() string { return "pve1" },
		FlapWindow: 10 * time.Minute,
		FlapThresh: 3,
	})

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var last Status
	for i := 0; i < 5; i++ {
		h.bgp = map[string][]byte{"pve1": establishedBGP("10.20.0.12", "pve2")}
		svc.cfg.Now = fixedClock(base.Add(time.Duration(i) * time.Minute))
		status, err := svc.Status(context.Background())
		if err != nil {
			t.Fatalf("Status(#%d): %v", i, err)
		}
		last = status
	}
	if len(last.Findings) != 0 {
		t.Errorf("len(Findings) = %d, want 0 for a stable session; findings=%+v", len(last.Findings), last.Findings)
	}
}

// fakeSDN is a hand-rolled SDNZoneSource test double.
type fakeSDN struct {
	err  error
	tree sdn.Tree
}

func (f *fakeSDN) Tree(context.Context) (sdn.Tree, error) { return f.tree, f.err }

func TestStatus_ExitNodeHealth(t *testing.T) {
	h := &fakeHost{
		bgp:  map[string][]byte{"pve3": establishedBGP("10.20.0.11", "pve1")},
		evpn: map[string][]byte{"pve3": []byte(`{}`)},
	}
	sdnSvc := &fakeSDN{tree: sdn.Tree{Zones: []sdn.Zone{
		{ID: "evpnz", Type: "evpn", ExitNodes: []string{"pve3"}},
		{ID: "vlanz", Type: "vlan"}, // not evpn, ignored
	}}}
	svc := NewService(Config{
		Host:      h,
		LocalNode: func() string { return "pve3" },
		SDN:       sdnSvc,
		Now:       fixedClock(time.Now()),
	})

	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.ExitNodes) != 1 {
		t.Fatalf("len(ExitNodes) = %d, want 1", len(status.ExitNodes))
	}
	en := status.ExitNodes[0]
	if en.Zone != "evpnz" || en.Node != "pve3" || !en.Healthy {
		t.Errorf("exit node = %+v, want healthy evpnz/pve3", en)
	}
}

func TestStatus_ExitNodeHealth_Unhealthy(t *testing.T) {
	h := &fakeHost{
		bgp:  map[string][]byte{"pve3": stateBGP("10.20.0.11", "pve1", "Active")},
		evpn: map[string][]byte{"pve3": []byte(`{}`)},
	}
	sdnSvc := &fakeSDN{tree: sdn.Tree{Zones: []sdn.Zone{{ID: "evpnz", Type: "evpn", ExitNodes: []string{"pve3"}}}}}
	svc := NewService(Config{
		Host:      h,
		LocalNode: func() string { return "pve3" },
		SDN:       sdnSvc,
		Now:       fixedClock(time.Now()),
	})
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.ExitNodes) != 1 || status.ExitNodes[0].Healthy {
		t.Fatalf("ExitNodes = %+v, want one unhealthy entry", status.ExitNodes)
	}
}

func TestStatus_NoSDN_NoExitNodes(t *testing.T) {
	h := &fakeHost{}
	svc := NewService(Config{Host: h, LocalNode: func() string { return "pve1" }, Now: fixedClock(time.Now())})
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.ExitNodes) != 0 {
		t.Errorf("ExitNodes = %+v, want empty when SDN is nil", status.ExitNodes)
	}
}

func TestStatus_NoLocalNodeYet_ReturnsEmpty(t *testing.T) {
	svc := NewService(Config{Host: &fakeHost{}, LocalNode: func() string { return "" }, Now: fixedClock(time.Now())})
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Nodes) != 0 {
		t.Errorf("Nodes = %+v, want empty before local node is known", status.Nodes)
	}
}

// TestStatus_MergePeers_PrefersEVPNAddressFamily verifies mergePeers
// prefers an l2VpnEvpn observation over other AFIs for the same address.
func TestStatus_MergePeers_PrefersEVPNAddressFamily(t *testing.T) {
	h := &fakeHost{
		bgp: map[string][]byte{"pve1": []byte(`{
			"ipv4Unicast":{"routerId":"10.0.0.1","as":65001,"peers":{"10.20.0.12":{"hostname":"pve2","pfxRcd":100,"state":"Established"}}},
			"l2VpnEvpn":{"routerId":"10.0.0.1","as":65001,"peers":{"10.20.0.12":{"hostname":"pve2","pfxRcd":6,"state":"Established"}}}
		}`)},
	}
	svc := NewService(Config{Host: h, LocalNode: func() string { return "pve1" }, Now: fixedClock(time.Now())})
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Nodes[0].Peers) != 1 {
		t.Fatalf("len(Peers) = %d, want 1 (deduped across AFIs)", len(status.Nodes[0].Peers))
	}
	if status.Nodes[0].Peers[0].AddressFamily != "l2VpnEvpn" || status.Nodes[0].Peers[0].PfxRcd != 6 {
		t.Errorf("Peers[0] = %+v, want l2VpnEvpn/pfxRcd=6", status.Nodes[0].Peers[0])
	}
}
