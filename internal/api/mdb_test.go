// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// This file is T-3902's HTTP-level coverage for GET /mdb's cluster fan-out
// and filter wiring, mirroring conntrack_test.go's pattern exactly: a fake
// local MDBLocalSource plus a fake PeerMDBSource standing in for real peers
// (internal/peer's own client/server wire-level correctness is covered by
// that package's own tests). This file's job is proving the pieces are
// wired together correctly: fan-out merges every reachable node's entries
// and bridge config, a failing peer degrades only its own contribution,
// and the documented filters narrow the merged result set — including the
// case real users will hit most, an entirely empty MDB table everywhere.

type fakeMDBLocalSource struct {
	mdbErr      error
	linksErr    error
	mdbByNode   map[string][]byte
	linksByNode map[string][]host.LinkState
	lastArg     string
}

func (f *fakeMDBLocalSource) MDB(_ context.Context, node string) ([]byte, error) {
	f.lastArg = node
	if f.mdbErr != nil {
		return nil, f.mdbErr
	}
	return f.mdbByNode[node], nil
}

func (f *fakeMDBLocalSource) Links(_ context.Context, node string) ([]host.LinkState, error) {
	if f.linksErr != nil {
		return nil, f.linksErr
	}
	return f.linksByNode[node], nil
}

type fakePeerMDBSource struct {
	mdbPerPeer   map[string][]byte
	linksPerPeer map[string][]host.LinkState
	failNodes    map[string]bool
	peers        []peer.Peer
}

func (f *fakePeerMDBSource) Peers(context.Context) ([]peer.Peer, error) { return f.peers, nil }

func (f *fakePeerMDBSource) MDB(_ context.Context, p peer.Peer, _ string) ([]byte, error) {
	if f.failNodes[p.Node] {
		return nil, errors.New("fake: peer unreachable")
	}
	return f.mdbPerPeer[p.Node], nil
}

func (f *fakePeerMDBSource) Links(_ context.Context, p peer.Peer, _ string) ([]host.LinkState, error) {
	if f.failNodes[p.Node] {
		return nil, errors.New("fake: peer unreachable")
	}
	return f.linksPerPeer[p.Node], nil
}

func mdbTestRouter(local MDBLocalSource, peers PeerMDBSource) http.Handler {
	return NewRouter(Options{
		Version:   "test",
		DistFS:    testDistFS(),
		Logger:    testLogger(),
		Auth:      fakeAuth{authenticated: true},
		MDB:       local,
		PeerMDB:   peers,
		LocalNode: func() string { return "pve1" },
	})
}

func getMDB(t *testing.T, r http.Handler, query string) mdbListResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mdb"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body mdbListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return body
}

// pvecubeRawMDB is the exact `bridge -d -j mdb show` payload captured on
// pvecube (planning/reports/evidence/pve-9.2.4-bridge-mdb-2026-08-27.txt
// §4), used here (as in internal/host/mdb_test.go) so this HTTP-layer test
// exercises the real observed wire shape end to end.
const pvecubeRawMDB = `[{"mdb":[{"index":6,"dev":"vmbr0","port":"enp1s0","grp":"ff02::fb","state":"temp","protocol":"kernel","flags":[]}],"router":{}}]`

// TestMDBRoute_EmptyEverywhere covers T-3902's most important case per the
// task card: an entirely empty MDB table and no bridges declaring
// multicast config anywhere in the cluster (the state observed for most
// bridges on a real PVE 9.2.4 host) must render a clean, empty, non-error
// response — not partial, not failed.
func TestMDBRoute_EmptyEverywhere(t *testing.T) {
	local := &fakeMDBLocalSource{mdbByNode: map[string][]byte{"pve1": nil}}
	r := mdbTestRouter(local, nil)

	body := getMDB(t, r, "")
	if len(body.Entries) != 0 {
		t.Errorf("entries = %+v, want none", body.Entries)
	}
	if len(body.Bridges) != 0 {
		t.Errorf("bridges = %+v, want none", body.Bridges)
	}
	if body.Partial {
		t.Error("partial = true, want false")
	}
	if len(body.FailedNodes) != 0 {
		t.Errorf("failedNodes = %v, want none", body.FailedNodes)
	}
}

// TestMDBRoute_LocalOnly_ParsesRealShape proves the route round-trips the
// exact real `bridge -d -j mdb show` bytes through host.ParseMDB and
// surfaces per-bridge snooping state from Links().
func TestMDBRoute_LocalOnly_ParsesRealShape(t *testing.T) {
	local := &fakeMDBLocalSource{
		mdbByNode: map[string][]byte{"pve1": []byte(pvecubeRawMDB)},
		linksByNode: map[string][]host.LinkState{
			"pve1": {{Name: "vmbr0", Kind: "bridge", Bridge: &host.BridgeDetail{
				MulticastSnooping: true, MulticastQuerier: false, MulticastRouterMode: 1,
			}}},
		},
	}
	r := mdbTestRouter(local, nil)

	body := getMDB(t, r, "")
	if len(body.Entries) != 1 {
		t.Fatalf("entries = %+v, want 1", body.Entries)
	}
	e := body.Entries[0]
	if e.Node != "pve1" || e.Bridge != "vmbr0" || e.Group != "ff02::fb" || e.Port != "enp1s0" || e.State != "temp" || e.Protocol != "kernel" {
		t.Errorf("entry = %+v, want the real pvecube-observed fields", e)
	}
	if len(body.Bridges) != 1 {
		t.Fatalf("bridges = %+v, want 1", body.Bridges)
	}
	b := body.Bridges[0]
	if b.Node != "pve1" || b.Bridge != "vmbr0" || !b.Snooping || b.Querier || b.RouterMode != 1 {
		t.Errorf("bridge = %+v, want node=pve1 bridge=vmbr0 snooping=true querier=false routerMode=1", b)
	}
	if body.Partial {
		t.Error("partial = true, want false")
	}
	if local.lastArg != "pve1" {
		t.Errorf("local.MDB called with node = %q, want pve1 (from LocalNode)", local.lastArg)
	}
}

// TestMDBRoute_ClusterMerge is the cluster fan-out acceptance case: entries
// from every reachable node merge into one list, and one unreachable peer
// degrades only its own contribution.
func TestMDBRoute_ClusterMerge(t *testing.T) {
	local := &fakeMDBLocalSource{mdbByNode: map[string][]byte{"pve1": []byte(pvecubeRawMDB)}}
	peers := &fakePeerMDBSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}, {Node: "pve3", Addr: "10.0.0.3:8007"}},
		mdbPerPeer: map[string][]byte{
			"pve2": []byte(`[{"mdb":[{"dev":"vmbr1","port":"eno2","grp":"224.0.0.251","state":"temp","protocol":"kernel"}],"router":{}}]`),
		},
		failNodes: map[string]bool{"pve3": true},
	}
	r := mdbTestRouter(local, peers)

	body := getMDB(t, r, "")
	if len(body.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2 (local pve1 + peer pve2; pve3 failed)", body.Entries)
	}
	if !body.Partial {
		t.Error("partial = false, want true (pve3 unreachable)")
	}
	if len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve3" {
		t.Errorf("failedNodes = %v, want [pve3]", body.FailedNodes)
	}
	nodes := map[string]bool{}
	for _, e := range body.Entries {
		nodes[e.Node] = true
	}
	if !nodes["pve1"] || !nodes["pve2"] {
		t.Errorf("expected entries from both pve1 and pve2, got nodes %+v", nodes)
	}
}

// TestMDBRoute_LocalFailure_DegradesOnlyLocal proves a local read failure
// is reported the same way a peer failure is — partial/failedNodes —
// rather than 500ing the whole request.
func TestMDBRoute_LocalFailure_DegradesOnlyLocal(t *testing.T) {
	local := &fakeMDBLocalSource{mdbErr: errors.New("boom")}
	peers := &fakePeerMDBSource{
		peers:      []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}},
		mdbPerPeer: map[string][]byte{"pve2": []byte(`[{"mdb":[{"dev":"vmbr0","grp":"239.1.1.1"}],"router":{}}]`)},
	}
	r := mdbTestRouter(local, peers)

	body := getMDB(t, r, "")
	if len(body.Entries) != 1 || body.Entries[0].Node != "pve2" {
		t.Fatalf("entries = %+v, want just pve2's entry", body.Entries)
	}
	if !body.Partial || len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve1" {
		t.Errorf("partial=%v failedNodes=%v, want partial=true failedNodes=[pve1]", body.Partial, body.FailedNodes)
	}
}

// TestMDBRoute_NodeFilter proves ?node= narrows both entries and bridges to
// exactly the requested node.
func TestMDBRoute_NodeFilter(t *testing.T) {
	local := &fakeMDBLocalSource{
		mdbByNode: map[string][]byte{"pve1": []byte(pvecubeRawMDB)},
		linksByNode: map[string][]host.LinkState{
			"pve1": {{Name: "vmbr0", Kind: "bridge", Bridge: &host.BridgeDetail{MulticastSnooping: true}}},
		},
	}
	peers := &fakePeerMDBSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}},
		mdbPerPeer: map[string][]byte{
			"pve2": []byte(`[{"mdb":[{"dev":"vmbr1","grp":"224.0.0.251"}],"router":{}}]`),
		},
	}
	r := mdbTestRouter(local, peers)

	body := getMDB(t, r, "?node=pve2")
	if len(body.Entries) != 1 || body.Entries[0].Node != "pve2" {
		t.Fatalf("entries = %+v, want just pve2's entry", body.Entries)
	}
	if len(body.Bridges) != 0 {
		t.Errorf("bridges = %+v, want none (pve1's bridge filtered out)", body.Bridges)
	}
}

// TestMDBRoute_GroupFilter proves ?group= substring-matches (case
// insensitively) against the multicast group address.
func TestMDBRoute_GroupFilter(t *testing.T) {
	local := &fakeMDBLocalSource{mdbByNode: map[string][]byte{
		"pve1": []byte(`[{"mdb":[
			{"dev":"vmbr0","grp":"ff02::fb","state":"temp"},
			{"dev":"vmbr0","grp":"239.5.5.5","state":"temp"}
		],"router":{}}]`),
	}}
	r := mdbTestRouter(local, nil)

	body := getMDB(t, r, "?group=FF02")
	if len(body.Entries) != 1 || body.Entries[0].Group != "ff02::fb" {
		t.Fatalf("entries = %+v, want just the ff02::fb group", body.Entries)
	}
}

// TestMDBRoute_ParseFailure_DegradesLikeAnyOtherFailure proves that
// unparseable MDB output from a node is treated as a read failure for that
// node (partial/failedNodes), not a 500 for the whole request.
func TestMDBRoute_ParseFailure_DegradesLikeAnyOtherFailure(t *testing.T) {
	local := &fakeMDBLocalSource{mdbByNode: map[string][]byte{"pve1": []byte("not json at all")}}
	r := mdbTestRouter(local, nil)

	body := getMDB(t, r, "")
	if len(body.Entries) != 0 {
		t.Errorf("entries = %+v, want none", body.Entries)
	}
	if !body.Partial || len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve1" {
		t.Errorf("partial=%v failedNodes=%v, want partial=true failedNodes=[pve1]", body.Partial, body.FailedNodes)
	}
}
