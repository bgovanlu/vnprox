package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
)

// buildSimGraph builds a graph with a bridge, two firewall-enabled guests on
// it, and a cluster ruleset — enough to exercise POST /simulate/path.
func buildSimGraph(t *testing.T) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	bridge := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
		Name: "vmbr0", Virt: inventory.BridgeLinux, VlanAware: true, VlanAwareSet: true, Gateway: "10.0.0.1",
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{bridge})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: "pve1"}, []inventory.Entity{bridge})

	guests := []inventory.Entity{
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}, VMID: 100, Name: "a", Node: "pve1", Type: "qemu"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}, Key: "net0", TargetName: "vmbr0", Firewall: true},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "101"}, VMID: 101, Name: "b", Node: "pve1", Type: "qemu"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "101/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "101"}, Key: "net0", TargetName: "vmbr0", Firewall: true},
	}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}}, guests)

	fwEnts := []inventory.Entity{
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster,
			Enabled: true, DefaultIn: "DROP", DefaultOut: "ACCEPT",
			Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22"}}},
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/100"}, Scope: inventory.FwScopeGuest, Enabled: true},
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/101"}, Scope: inventory.FwScopeGuest, Enabled: true},
	}
	g.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Kinds: []inventory.Kind{inventory.KindFwRuleset}}, fwEnts)
	return g
}

func postSimulate(t *testing.T, graph SimulatorGraph, auth AuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Simulator: graph,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/simulate/path", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func netReadAuth() fakeAuthWithCaps { return firewallTestAuth(map[string]bool{"netRead": true}) }

func TestSimulate_Unauthenticated401(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), fakeAuth{authenticated: false},
		`{"src":{"kind":"external"},"dst":{"kind":"external"}}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestSimulate_GuestToGuestAllow(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:100/net0"},"dst":{"kind":"guest-nic","ref":"guest-nic:pve1:101/net0"},"proto":"tcp","port":22}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got simulateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Verdict != "allow" {
		t.Errorf("verdict = %q, want allow (body: %s)", got.Verdict, rec.Body.String())
	}
	if len(got.Caveats) == 0 {
		t.Error("result must always carry caveats (AC3)")
	}
	if len(got.Hops) == 0 {
		t.Error("expected a hop list")
	}
}

func TestSimulate_GuestToGuestDeny(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:100/net0"},"dst":{"kind":"guest-nic","ref":"guest-nic:pve1:101/net0"},"proto":"tcp","port":80}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got simulateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Verdict != "deny" {
		t.Fatalf("verdict = %q, want deny", got.Verdict)
	}
	if got.BlockingRule == nil || got.BlockingRule.EnforcementPoint != "dest-guest-in" {
		t.Errorf("blockingRule = %+v, want a dest-guest-in block", got.BlockingRule)
	}
}

func TestSimulate_ExternalEndpoint(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:100/net0"},"dst":{"kind":"external"},"proto":"tcp","port":443}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got simulateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Verdict != "allow" {
		t.Errorf("guest->external verdict = %q, want allow (via gateway); body: %s", got.Verdict, rec.Body.String())
	}
}

func TestSimulate_MalformedBody400(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), netReadAuth(), `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSimulate_BadEndpointKind400(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), netReadAuth(),
		`{"src":{"kind":"bogus"},"dst":{"kind":"external"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestSimulate_BadGuestRef400(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"not-a-ref"},"dst":{"kind":"external"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// buildDualstackSimGraph builds a graph with two firewall-enabled guests on
// one bridge; the destination guest's ruleset carries an IPv6-only DROP
// rule ("dest: ::/0" matches every v6 address and, by netip.Prefix.
// Contains' own cross-family semantics, no v4 address at all), default
// policy ACCEPT otherwise — so a v4 flow to it always allows and a v6 flow
// always denies, for T-1404 acceptance criterion 3.
func buildDualstackSimGraph(t *testing.T) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	bridge := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
		Name: "vmbr0", Virt: inventory.BridgeLinux, VlanAware: true, VlanAwareSet: true,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{bridge})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: "pve1"}, []inventory.Entity{bridge})

	guests := []inventory.Entity{
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}, VMID: 200, Name: "src", Node: "pve1", Type: "qemu"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "200/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}, Key: "net0", TargetName: "vmbr0",
			Firewall: true, Mac: "AA:AA:AA:AA:AA:01"},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "201"}, VMID: 201, Name: "dst", Node: "pve1", Type: "qemu"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "201/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "201"}, Key: "net0", TargetName: "vmbr0",
			Firewall: true, Mac: "AA:AA:AA:AA:AA:02"},
	}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}}, guests)

	fwEnts := []inventory.Entity{
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/200"},
			Scope: inventory.FwScopeGuest, Enabled: true, DefaultOut: "ACCEPT"},
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/201"},
			Scope: inventory.FwScopeGuest, Enabled: true, DefaultIn: "ACCEPT",
			Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "DROP", Dest: "::/0"}}},
	}
	g.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Kinds: []inventory.Kind{inventory.KindFwRuleset}}, fwEnts)
	return g
}

func postSimulateWithIPAM(t *testing.T, graph SimulatorGraph, ipamSvc GuestInteriorIPAMSource, auth AuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Simulator: graph, GuestInteriorIPAM: ipamSvc,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/simulate/path", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestSimulate_FamilyV4VsV6_DistinctVerdict is T-1404 acceptance criterion
// 3: the identical request, differing only in "family", resolves the
// destination guest-nic's known v4 vs v6 address (via the IPAM allocation
// seam) and produces distinct, correct verdicts against the fixture's
// IPv6-only firewall rule.
func TestSimulate_FamilyV4VsV6_DistinctVerdict(t *testing.T) {
	graph := buildDualstackSimGraph(t)
	ipamSvc := fakeGuestInteriorIPAM{byCIDR: map[string][]ipam.Allocation{
		"10.0.0.0/24": {
			{IP: "10.0.0.20", MAC: "aa:aa:aa:aa:aa:02", VMID: 201},
		},
		"2001:db8:60::/64": {
			{IP: "2001:db8:60::20", MAC: "aa:aa:aa:aa:aa:02", VMID: 201},
		},
	}}

	body := `{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:200/net0"},"dst":{"kind":"guest-nic","ref":"guest-nic:pve1:201/net0"},"family":"%s"}`

	recV4 := postSimulateWithIPAM(t, graph, ipamSvc, netReadAuth(), fmt.Sprintf(body, "v4"))
	if recV4.Code != http.StatusOK {
		t.Fatalf("v4: status = %d, body: %s", recV4.Code, recV4.Body.String())
	}
	var v4 simulateResponse
	if err := json.Unmarshal(recV4.Body.Bytes(), &v4); err != nil {
		t.Fatalf("v4 decode: %v", err)
	}
	if v4.Verdict != "allow" {
		t.Errorf("v4 verdict = %q, want allow (body: %s)", v4.Verdict, recV4.Body.String())
	}

	recV6 := postSimulateWithIPAM(t, graph, ipamSvc, netReadAuth(), fmt.Sprintf(body, "v6"))
	if recV6.Code != http.StatusOK {
		t.Fatalf("v6: status = %d, body: %s", recV6.Code, recV6.Body.String())
	}
	var v6 simulateResponse
	if err := json.Unmarshal(recV6.Body.Bytes(), &v6); err != nil {
		t.Fatalf("v6 decode: %v", err)
	}
	if v6.Verdict != "deny" {
		t.Errorf("v6 verdict = %q, want deny (body: %s)", v6.Verdict, recV6.Body.String())
	}
	if v4.Verdict == v6.Verdict {
		t.Errorf("family v4/v6 produced the same verdict %q, want distinct verdicts", v4.Verdict)
	}
}

// TestSimulate_InvalidFamily400 is a regression guard on validateFamily.
func TestSimulate_InvalidFamily400(t *testing.T) {
	rec := postSimulate(t, buildSimGraph(t), netReadAuth(),
		`{"src":{"kind":"external"},"dst":{"kind":"external"},"family":"v5"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSimulate_RouteNotMountedWithoutGraph(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: netReadAuth(), Simulator: nil,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/simulate/path", bytes.NewBufferString(`{"src":{"kind":"external"},"dst":{"kind":"external"}}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}
