package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/sdn"
)

// newEvpnLabSDNService builds a real *sdn.Service backed by a real
// *pve.Client talking to a pvemock server loaded from evpn-lab.yaml — T-401
// acceptance criterion 1's "evpn-lab fixture ... golden /sdn JSON test"
// exercised through the actual mounted route, not a hand-built fixture
// struct.
func newEvpnLabSDNService(t *testing.T) *sdn.Service {
	t.Helper()
	fx, err := pvemock.LoadFixture("../../testdata/clusters/evpn-lab.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := pve.New(pve.Config{
		APIURL:   ts.URL,
		Auth:     pve.AuthTicket,
		Username: "root@pam",
		Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return sdn.NewService(c)
}

// sdnTreeResponse mirrors sdn.Tree's JSON shape for response assertions —
// pinning the wire contract (docs/api.md's GET /sdn) independently of the
// Go struct, the same pattern diff_route_test.go's diffResponse uses.
type sdnTreeResponse struct {
	Zones []struct {
		Diff *struct {
			State         string   `json:"state"`
			ChangedFields []string `json:"changedFields"`
		} `json:"pendingDiff"`
		ID         string   `json:"id"`
		Type       string   `json:"type"`
		Bridge     string   `json:"bridge"`
		Pending    string   `json:"pending"`
		Nodes      []string `json:"nodes"`
		Peers      []string `json:"peers"`
		NodeStatus []struct {
			Node   string `json:"node"`
			Status string `json:"status"`
		} `json:"nodeStatus"`
		Vnets []struct {
			ID      string `json:"id"`
			Zone    string `json:"zone"`
			Pending string `json:"pending"`
			Diff    *struct {
				State string `json:"state"`
			} `json:"pendingDiff"`
			Subnets []struct {
				ID   string `json:"id"`
				CIDR string `json:"cidr"`
			} `json:"subnets"`
		} `json:"vnets"`
		MTU int `json:"mtu"`
	} `json:"zones"`
	GeneratedAt int64 `json:"generatedAt"`
}

// TestSDNRoute_EvpnLab_GoldenTree is T-401 acceptance criterion 1: the tree
// renders all five zone types (simple/vlan/qinq/vxlan/evpn) with correct
// per-node status, through the mounted GET /sdn route.
func TestSDNRoute_EvpnLab_GoldenTree(t *testing.T) {
	svc := newEvpnLabSDNService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, SDN: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sdn status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got sdnTreeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if len(got.Zones) != 5 {
		t.Fatalf("zones = %d, want 5 (simple, vlan, qinq, vxlan, evpn); got %+v", len(got.Zones), got.Zones)
	}

	byID := map[string]int{}
	for i, z := range got.Zones {
		byID[z.ID] = i
	}
	wantTypes := map[string]string{
		"simplez": "simple", "vlanz": "vlan", "qinqz": "qinq", "vxlanz": "vxlan", "evpnz": "evpn",
	}
	for id, wantType := range wantTypes {
		i, ok := byID[id]
		if !ok {
			t.Fatalf("missing zone %q in %+v", id, got.Zones)
		}
		if got.Zones[i].Type != wantType {
			t.Errorf("zone %s type = %q, want %q", id, got.Zones[i].Type, wantType)
		}
	}

	// --- AC4: simplez's missing vmbr1 on pve3 reports node status=error,
	// while pve1/pve2 (which do have vmbr1) report ok. ---------------------
	simplez := got.Zones[byID["simplez"]]
	statusByNode := map[string]string{}
	for _, ns := range simplez.NodeStatus {
		statusByNode[ns.Node] = ns.Status
	}
	if statusByNode["pve1"] != "ok" || statusByNode["pve2"] != "ok" {
		t.Errorf("simplez pve1/pve2 status = %+v, want both ok", statusByNode)
	}
	if statusByNode["pve3"] != "error" {
		t.Errorf("simplez pve3 status = %q, want error", statusByNode["pve3"])
	}

	// --- AC2: vlanz is pending=changed with a real running/staged mtu
	// delta; qinqz's vnet is pending=new; evpnz is in sync (no Diff at
	// all). ------------------------------------------------------------
	vlanz := got.Zones[byID["vlanz"]]
	if vlanz.Pending != "changed" {
		t.Fatalf("vlanz.Pending = %q, want changed", vlanz.Pending)
	}
	if vlanz.Diff == nil || vlanz.Diff.State != "changed" {
		t.Fatalf("vlanz.Diff = %+v, want state=changed", vlanz.Diff)
	}
	if len(vlanz.Diff.ChangedFields) != 1 || vlanz.Diff.ChangedFields[0] != "mtu" {
		t.Errorf("vlanz.Diff.ChangedFields = %v, want exactly [mtu]", vlanz.Diff.ChangedFields)
	}

	evpnz := got.Zones[byID["evpnz"]]
	if evpnz.Pending != "" || evpnz.Diff != nil {
		t.Errorf("evpnz should be in sync (no pendingDiff), got pending=%q diff=%+v", evpnz.Pending, evpnz.Diff)
	}

	qinqz := got.Zones[byID["qinqz"]]
	if len(qinqz.Vnets) != 1 || qinqz.Vnets[0].Pending != "new" {
		t.Fatalf("qinqz.Vnets = %+v, want one vnet pending=new", qinqz.Vnets)
	}
	if qinqz.Vnets[0].Diff == nil || qinqz.Vnets[0].Diff.State != "new" {
		t.Errorf("qinqz vnet Diff = %+v, want state=new", qinqz.Vnets[0].Diff)
	}

	// --- vxlanz/evpnz both carry the fixture's 3-node peer list, feeding
	// the VTEP mesh (internal/inventory/link.go); subnets nest correctly
	// under their vnet. ---------------------------------------------------
	vxlanz := got.Zones[byID["vxlanz"]]
	if len(vxlanz.Peers) != 3 {
		t.Errorf("vxlanz.Peers = %v, want 3 peer addresses", vxlanz.Peers)
	}
	if len(vxlanz.Vnets) != 1 || len(vxlanz.Vnets[0].Subnets) != 1 || vxlanz.Vnets[0].Subnets[0].CIDR != "10.80.0.0/24" {
		t.Fatalf("vxlanz vnet/subnet nesting = %+v", vxlanz.Vnets)
	}
}

// TestSDNRoute_Unauthenticated401 mirrors T-106's topology route test: no
// session -> 401, proving the route is actually gated.
func TestSDNRoute_Unauthenticated401(t *testing.T) {
	svc := newEvpnLabSDNService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, SDN: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /sdn (unauthenticated) status = %d, want 401", rec.Code)
	}
}

// TestSDNRoute_NotMountedWhenServiceNil documents the degraded-mode
// behavior (collectors' PVE client failed to build, see
// cmd/vnproxd/collect.go): the route isn't mounted at all, so it 404s
// rather than 500ing.
func TestSDNRoute_NotMountedWhenServiceNil(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, SDN: nil,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /sdn (nil service) status = %d, want 404", rec.Code)
	}
}
