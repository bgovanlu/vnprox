// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// This file is T-1305's HTTP-level coverage for GET /conntrack's cluster
// fan-out and filter wiring, mirroring flows_http_test.go's pattern
// exactly: a fake local ConntrackLocalSource plus a fake
// PeerConntrackSource standing in for real peers (internal/peer's own
// client/server wire-level correctness is covered by that package's own
// tests). This file's job is proving the pieces are wired together
// correctly: fan-out merges every reachable node's table, a failing peer
// degrades only its own contribution, and every documented filter narrows
// the merged result set (ANDed when combined).

type fakeConntrackLocalSource struct {
	err     error
	byNode  map[string][]host.ConntrackEntry
	lastArg string
}

func (f *fakeConntrackLocalSource) Conntrack(_ context.Context, node string) ([]host.ConntrackEntry, error) {
	f.lastArg = node
	if f.err != nil {
		return nil, f.err
	}
	return f.byNode[node], nil
}

type fakePeerConntrackSource struct {
	perPeer          map[string][]host.ConntrackEntry
	failNodes        map[string]bool
	unavailableNodes map[string]bool
	peers            []peer.Peer
}

func (f *fakePeerConntrackSource) Peers(context.Context) ([]peer.Peer, error) { return f.peers, nil }

func (f *fakePeerConntrackSource) Conntrack(_ context.Context, p peer.Peer, _ string) ([]host.ConntrackEntry, error) {
	if f.unavailableNodes[p.Node] {
		return nil, fmt.Errorf("fake: peer conntrack unavailable: %w", host.ErrConntrackUnavailable)
	}
	if f.failNodes[p.Node] {
		return nil, errors.New("fake: peer unreachable")
	}
	return f.perPeer[p.Node], nil
}

type fakeConntrackGuestResolver struct {
	byGuest map[string][]string
}

func (f *fakeConntrackGuestResolver) GuestIPs(_ context.Context, guestRef string) ([]string, error) {
	return f.byGuest[guestRef], nil
}

func conntrackTestRouter(local ConntrackLocalSource, peers PeerConntrackSource, guests ConntrackGuestResolver) http.Handler {
	return NewRouter(Options{
		Version:         "test",
		DistFS:          testDistFS(),
		Logger:          testLogger(),
		Auth:            fakeAuth{authenticated: true},
		Conntrack:       local,
		PeerConntrack:   peers,
		ConntrackGuests: guests,
		LocalNode:       func() string { return "pve1" },
	})
}

func getConntrack(t *testing.T, r http.Handler, query string) conntrackListResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conntrack"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body conntrackListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return body
}

func TestConntrackRoute_LocalOnly_UnchangedWhenNoPeerSource(t *testing.T) {
	local := &fakeConntrackLocalSource{byNode: map[string][]host.ConntrackEntry{
		"pve1": {{Proto: 6, SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 1000, DstPort: 443, State: "ESTABLISHED"}},
	}}
	r := conntrackTestRouter(local, nil, nil)

	body := getConntrack(t, r, "")
	if len(body.Items) != 1 || body.Items[0].SrcIP != "10.0.0.1" || body.Items[0].Node != "pve1" {
		t.Fatalf("items = %+v", body.Items)
	}
	if body.Partial {
		t.Error("partial = true, want false (no peer source configured)")
	}
	if local.lastArg != "pve1" {
		t.Errorf("local.Conntrack called with node = %q, want pve1 (from LocalNode)", local.lastArg)
	}
}

// TestConntrackRoute_ClusterMerge is T-1305 acceptance criterion 3: entries
// from every reachable node merge into one list, and one unreachable peer
// degrades only its own contribution (partial/failedNodes set, the local
// node's and the healthy peer's entries still present).
func TestConntrackRoute_ClusterMerge(t *testing.T) {
	local := &fakeConntrackLocalSource{byNode: map[string][]host.ConntrackEntry{
		"pve1": {{Proto: 6, SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 1000, DstPort: 443, State: "ESTABLISHED"}},
	}}
	peers := &fakePeerConntrackSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}, {Node: "pve3", Addr: "10.0.0.3:8007"}},
		perPeer: map[string][]host.ConntrackEntry{
			"pve2": {{Proto: 17, SrcIP: "10.0.1.1", DstIP: "10.0.1.2", SrcPort: 5000, DstPort: 53, State: "ASSURED"}},
		},
		failNodes: map[string]bool{"pve3": true},
	}
	r := conntrackTestRouter(local, peers, nil)

	body := getConntrack(t, r, "")
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want 2 (local pve1 + peer pve2; pve3 failed)", body.Items)
	}
	if !body.Partial {
		t.Error("partial = false, want true (pve3 unreachable)")
	}
	if len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve3" {
		t.Errorf("failedNodes = %v, want [pve3]", body.FailedNodes)
	}
	nodes := map[string]bool{}
	for _, it := range body.Items {
		nodes[it.Node] = true
	}
	if !nodes["pve1"] || !nodes["pve2"] {
		t.Errorf("expected entries from both pve1 and pve2, got nodes %+v", nodes)
	}
}

// TestConntrackRoute_LocalFailure_DegradesOnlyLocal proves a local read
// failure (e.g. Real.Conntrack erroring) is reported the same way a peer
// failure is — partial/failedNodes — rather than 500ing the whole request,
// so a healthy peer's entries still surface.
func TestConntrackRoute_LocalFailure_DegradesOnlyLocal(t *testing.T) {
	local := &fakeConntrackLocalSource{err: errors.New("boom")}
	peers := &fakePeerConntrackSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}},
		perPeer: map[string][]host.ConntrackEntry{
			"pve2": {{Proto: 6, SrcIP: "10.0.1.1", DstIP: "10.0.1.2", SrcPort: 1, DstPort: 2, State: "ESTABLISHED"}},
		},
	}
	r := conntrackTestRouter(local, peers, nil)

	body := getConntrack(t, r, "")
	if len(body.Items) != 1 || body.Items[0].Node != "pve2" {
		t.Fatalf("items = %+v, want just pve2's entry", body.Items)
	}
	if !body.Partial || len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve1" {
		t.Errorf("partial=%v failedNodes=%v, want partial=true failedNodes=[pve1]", body.Partial, body.FailedNodes)
	}
	if len(body.UnavailableNodes) != 0 {
		t.Errorf("unavailableNodes = %v, want empty (this is an ordinary read failure, not host.ErrConntrackUnavailable)", body.UnavailableNodes)
	}
}

// TestConntrackRoute_UnavailableInterface_SurfacesDistinctlyFromFailure is
// T-3711's API-layer requirement: a node whose conntrack read fails
// because the interface itself cannot be provided (host.ErrConntrackUnavailable
// — no CAP_NET_ADMIN, or no netlink conntrack support at all) must be
// named in unavailableNodes, never failedNodes — "this node cannot
// provide conntrack" and "the read failed" are different statements. This
// covers both the local node and a peer (whose error crosses the peer
// wire as the conntrack_unavailable code, mapped back by
// peer.Client.Conntrack — see internal/peer/client_test.go's own coverage
// of that translation).
func TestConntrackRoute_UnavailableInterface_SurfacesDistinctlyFromFailure(t *testing.T) {
	local := &fakeConntrackLocalSource{err: fmt.Errorf("wrapped: %w", host.ErrConntrackUnavailable)}
	peers := &fakePeerConntrackSource{
		peers:            []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}, {Node: "pve3", Addr: "10.0.0.3:8007"}},
		failNodes:        map[string]bool{"pve3": true},
		unavailableNodes: map[string]bool{"pve2": true},
	}
	r := conntrackTestRouter(local, peers, nil)

	body := getConntrack(t, r, "")
	if !body.Partial {
		t.Fatal("partial = false, want true")
	}
	if len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve3" {
		t.Errorf("failedNodes = %v, want [pve3] (an ordinary peer read failure)", body.FailedNodes)
	}
	wantUnavailable := map[string]bool{"pve1": true, "pve2": true}
	if len(body.UnavailableNodes) != len(wantUnavailable) {
		t.Fatalf("unavailableNodes = %v, want %v", body.UnavailableNodes, wantUnavailable)
	}
	for _, n := range body.UnavailableNodes {
		if !wantUnavailable[n] {
			t.Errorf("unexpected node %q in unavailableNodes", n)
		}
	}
	// A node must never appear in both lists.
	for _, n := range body.FailedNodes {
		if wantUnavailable[n] {
			t.Errorf("node %q appears in both failedNodes and unavailableNodes", n)
		}
	}
}

// TestConntrackRoute_FilterMatrix is T-1305 acceptance criterion 4:
// guest=/srcIp=/dstIp=/port=/state= each independently narrow the merged
// result set, ANDed when combined.
func TestConntrackRoute_FilterMatrix(t *testing.T) {
	local := &fakeConntrackLocalSource{byNode: map[string][]host.ConntrackEntry{
		"pve1": {
			{Proto: 6, SrcIP: "10.0.0.5", DstIP: "10.0.0.20", SrcPort: 54321, DstPort: 443, State: "ESTABLISHED"},
			{Proto: 17, SrcIP: "10.0.0.6", DstIP: "10.0.0.30", SrcPort: 51000, DstPort: 53, State: "ASSURED"},
			{
				Proto: 6, SrcIP: "10.0.0.5", DstIP: "8.8.8.8", SrcPort: 44444, DstPort: 443, State: "ESTABLISHED",
				NatSrc: &host.NatAddr{IP: "203.0.113.10", Port: 44444},
			},
		},
	}}
	guests := &fakeConntrackGuestResolver{byGuest: map[string][]string{
		"guest:pve1:104": {"10.0.0.5"},
	}}
	r := conntrackTestRouter(local, nil, guests)

	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"no filter", "", 3},
		{"guest= narrows to that guest's known IPs", "?guest=guest:pve1:104", 2},
		{"guest= unknown ref matches nothing", "?guest=guest:pve1:999", 0},
		{"srcIp= narrows exactly", "?srcIp=10.0.0.6", 1},
		{"dstIp= narrows exactly", "?dstIp=8.8.8.8", 1},
		{"port= matches either src or dst port", "?port=443", 2},
		{"port= matches nothing when absent from any entry", "?port=9999", 0},
		{"state= is case-insensitive", "?state=established", 2},
		{"combined guest= AND port= is ANDed", "?guest=guest:pve1:104&port=44444", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := getConntrack(t, r, tt.query)
			if len(body.Items) != tt.want {
				t.Errorf("items = %d, want %d (query %q): %+v", len(body.Items), tt.want, tt.query, body.Items)
			}
		})
	}

	// NAT columns round-trip.
	body := getConntrack(t, r, "?dstIp=8.8.8.8")
	if len(body.Items) != 1 || body.Items[0].NatSrc == nil || body.Items[0].NatSrc.IP != "203.0.113.10" {
		t.Fatalf("NAT entry = %+v, want NatSrc.IP=203.0.113.10", body.Items)
	}
}

func TestConntrackRoute_InvalidPort_Returns400(t *testing.T) {
	local := &fakeConntrackLocalSource{byNode: map[string][]host.ConntrackEntry{}}
	r := conntrackTestRouter(local, nil, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conntrack?port=notanumber", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestConntrackRoute_NoMutationMethodAllowed is T-1305's explicit non-goal
// regression (acceptance criterion 6): conntrack is read-only this arc —
// no flush/delete of any entry, ever — so every write method against
// /conntrack must fail to route (chi has no handler registered for
// anything but GET here; a 405/404 proves that, as opposed to a write
// somehow reaching a handler that would perform one).
func TestConntrackRoute_NoMutationMethodAllowed(t *testing.T) {
	local := &fakeConntrackLocalSource{byNode: map[string][]host.ConntrackEntry{
		"pve1": {{Proto: 6, SrcIP: "10.0.0.1", DstIP: "10.0.0.2", State: "ESTABLISHED"}},
	}}
	r := conntrackTestRouter(local, nil, nil)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/conntrack", nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s /conntrack returned 200, want a routing failure (no mutation route exists)", method)
		}
	}
}

// TestConntrackRoute_NotMountedWithoutLocalSource mirrors every other
// optional-route-family convention: no Conntrack seam configured means the
// route simply isn't there (404 from chi, not a panic).
func TestConntrackRoute_NotMountedWithoutLocalSource(t *testing.T) {
	r := conntrackTestRouter(nil, nil, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conntrack", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
