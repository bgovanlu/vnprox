// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// This file is T-3904's HTTP-level coverage for GET /firewall/compiled,
// mirroring mdb_test.go's pattern: a fake local NftRulesetLocalSource plus
// a fake PeerNftRulesetSource standing in for real peers (internal/peer's
// own client/server wire correctness is covered by that package's own
// tests).

type fakeNftRulesetLocalSource struct {
	err     error
	byNode  map[string][]byte
	lastArg string
}

func (f *fakeNftRulesetLocalSource) NftRuleset(_ context.Context, node string) ([]byte, error) {
	f.lastArg = node
	if f.err != nil {
		return nil, f.err
	}
	return f.byNode[node], nil
}

type fakePeerNftRulesetSource struct {
	perPeer map[string][]byte
	peers   []peer.Peer
}

func (f *fakePeerNftRulesetSource) Peers(context.Context) ([]peer.Peer, error) { return f.peers, nil }

func (f *fakePeerNftRulesetSource) NftRuleset(_ context.Context, p peer.Peer, _ string) ([]byte, error) {
	return f.perPeer[p.Node], nil
}

func nftTestRouter(local NftRulesetLocalSource, peers PeerNftRulesetSource, graph FirewallGraph) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:           fakeAuth{authenticated: true},
		NftRuleset:     local,
		PeerNftRuleset: peers,
		Firewall:       graph,
		LocalNode:      func() string { return "pve1" },
	})
}

func getNftRuleset(t *testing.T, r http.Handler, query string) (int, nftRulesetResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/firewall/compiled"+query, nil))
	var body nftRulesetResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding response: %v body=%s", err, rec.Body.String())
		}
	}
	return rec.Code, body
}

// pvecubeEmptyNftRuleset is the exact `nft -j list ruleset` payload
// captured on pvecube (planning/reports/evidence/
// pve-9.2.4-nftables-firewall-engine-2026-08-28.txt §2) — a disabled
// firewall's real, observed shape: metainfo only, no tables.
const pvecubeEmptyNftRuleset = `{"nftables": [{"metainfo": {"version": "1.1.3", "release_name": "Commodore Bullmoose #4", "json_schema_version": 1}}]}`

func TestNftRulesetRoute_RealEmptyShape(t *testing.T) {
	local := &fakeNftRulesetLocalSource{byNode: map[string][]byte{"pve1": []byte(pvecubeEmptyNftRuleset)}}
	r := nftTestRouter(local, nil, nil)

	code, body := getNftRuleset(t, r, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !body.Empty {
		t.Errorf("Empty = false, want true for a metainfo-only document")
	}
	if len(body.Tables) != 0 || len(body.Rules) != 0 {
		t.Errorf("expected no tables/rules, got %+v", body)
	}
	if local.lastArg != "pve1" {
		t.Errorf("local reader called with node %q, want pve1 (from LocalNode)", local.lastArg)
	}
}

const fixtureNftRuleset = `{
  "nftables": [
    {"metainfo": {"version": "1.1.3", "release_name": "x", "json_schema_version": 1}},
    {"table": {"family": "inet", "name": "proxmox-firewall", "handle": 1}},
    {"chain": {"family": "inet", "table": "proxmox-firewall", "name": "input", "handle": 1, "type": "filter", "hook": "input", "prio": "filter", "policy": "drop"}},
    {"chain": {"family": "inet", "table": "proxmox-firewall", "name": "block-smurfs", "handle": 2}},
    {"rule": {"family": "inet", "table": "proxmox-firewall", "chain": "input", "handle": 10,
      "expr": [
        {"match": {"left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 22, "op": "=="}},
        {"match": {"left": {"meta": {"key": "l4proto"}}, "right": "tcp", "op": "=="}},
        {"accept": null}
      ]}},
    {"rule": {"family": "inet", "table": "proxmox-firewall", "chain": "block-smurfs", "handle": 11,
      "expr": [{"drop": null}]}}
  ]
}`

func TestNftRulesetRoute_ParsesAndAttributesAgainstClusterRule(t *testing.T) {
	local := &fakeNftRulesetLocalSource{byNode: map[string][]byte{"pve1": []byte(fixtureNftRuleset)}}
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster,
		Enabled: true,
		Rules:   []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22"}},
	}
	graph := buildTestGraph(t, cluster)
	r := nftTestRouter(local, nil, graph)

	code, body := getNftRuleset(t, r, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body.Empty {
		t.Fatalf("Empty = true, want false: %+v", body)
	}
	if len(body.Tables) != 1 || !body.Tables[0].PVEAuthored {
		t.Fatalf("expected one PVE-authored table, got %+v", body.Tables)
	}

	var mgmtRule, smurfRule *nftRuleResponse
	for i := range body.Rules {
		switch body.Rules[i].Handle {
		case 10:
			mgmtRule = &body.Rules[i]
		case 11:
			smurfRule = &body.Rules[i]
		}
	}
	if mgmtRule == nil || smurfRule == nil {
		t.Fatalf("expected both rules, got %+v", body.Rules)
	}

	if !mgmtRule.Attribution.Determined {
		t.Errorf("mgmtRule.Attribution.Determined = false, want true: %+v", mgmtRule.Attribution)
	}
	if mgmtRule.Attribution.Scope != "cluster" || mgmtRule.Attribution.Pos != 0 {
		t.Errorf("mgmtRule.Attribution = %+v, want scope=cluster pos=0", mgmtRule.Attribution)
	}

	// The block-smurfs chain is a PVE built-in protection chain — must be
	// explicitly labeled, never guess-matched against the cluster rule
	// (which has an entirely different verdict/proto anyway).
	if smurfRule.Attribution.Determined {
		t.Errorf("smurfRule.Attribution.Determined = true, want false (PVE built-in chain): %+v", smurfRule.Attribution)
	}
	if smurfRule.Attribution.Reason == "" {
		t.Error("smurfRule.Attribution.Reason must explain why no attribution was made")
	}
}

// TestNftRulesetRoute_RoutesToPeerNode proves ?node=<a peer node> is
// answered from that peer, not from the local reader — GET /firewall/
// compiled serves exactly one node per request (see fetchNftRuleset's doc
// comment), unlike GET /mdb's cluster-wide merge.
func TestNftRulesetRoute_RoutesToPeerNode(t *testing.T) {
	local := &fakeNftRulesetLocalSource{byNode: map[string][]byte{"pve1": []byte(pvecubeEmptyNftRuleset)}}
	peers := &fakePeerNftRulesetSource{
		peers:   []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}},
		perPeer: map[string][]byte{"pve2": []byte(fixtureNftRuleset)},
	}
	r := nftTestRouter(local, peers, nil)

	code, body := getNftRuleset(t, r, "?node=pve2")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body.Node != "pve2" {
		t.Errorf("Node = %q, want pve2", body.Node)
	}
	if body.Empty {
		t.Errorf("Empty = true, want false — pve2's fixture ruleset is non-empty")
	}
	if local.lastArg == "pve2" {
		t.Errorf("local reader was called for pve2 — this request should have routed to the peer instead")
	}
}

func TestNftRulesetRoute_LocalFailure(t *testing.T) {
	local := &fakeNftRulesetLocalSource{err: errTestNftReadFailure}
	r := nftTestRouter(local, nil, nil)
	code, _ := getNftRuleset(t, r, "")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
}

var errTestNftReadFailure = &testError{"simulated read failure"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// TestNftRulesetRoute_Unauthenticated401 matches every other netRead route
// in this package's own convention.
func TestNftRulesetRoute_Unauthenticated401(t *testing.T) {
	local := &fakeNftRulesetLocalSource{byNode: map[string][]byte{"pve1": []byte(pvecubeEmptyNftRuleset)}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, NftRuleset: local, LocalNode: func() string { return "pve1" },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/compiled", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestNftRulesetRoute_GetOnly is T-3904's acceptance criterion 3: the
// route accepts GET only — no edit affordance of any kind exists at the
// HTTP layer, which is the permanent PVE-firewall-engine boundary's
// hardest guarantee (an editor can't be added to the UI without also
// wiring a write route here first, and this test pins that this file
// never does).
func TestNftRulesetRoute_GetOnly(t *testing.T) {
	local := &fakeNftRulesetLocalSource{byNode: map[string][]byte{"pve1": []byte(pvecubeEmptyNftRuleset)}}
	r := nftTestRouter(local, nil, nil)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/firewall/compiled", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK || rec.Code == http.StatusNoContent || rec.Code == http.StatusCreated {
			t.Errorf("%s /firewall/compiled unexpectedly succeeded (status %d) — this route must be GET-only", method, rec.Code)
		}
	}
}
