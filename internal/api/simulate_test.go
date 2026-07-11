package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
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
