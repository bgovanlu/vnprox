package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakeLLDPService is a minimal LLDPService stand-in for router tests.
type fakeLLDPService struct {
	neighbors []*inventory.LldpNeighbor
	findings  []topology.VlanFinding
	ports     []topology.PortRow
}

func (f fakeLLDPService) LLDPNeighbors() []*inventory.LldpNeighbor { return f.neighbors }
func (f fakeLLDPService) VlanFindings() []topology.VlanFinding     { return f.findings }
func (f fakeLLDPService) Ports() []topology.PortRow                { return f.ports }

func TestLLDPRoutes_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{}, LLDP: fakeLLDPService{},
	})
	for _, path := range []string{"/api/v1/lldp", "/api/v1/lldp/vlan-check", "/api/v1/ports"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s (unauthenticated) status = %d, want 401", path, rec.Code)
		}
	}
}

func TestLLDPRoute_Authenticated(t *testing.T) {
	neighbor := &inventory.LldpNeighbor{
		Ref:  inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/sw1/p1"},
		Node: "pve1", LocalIface: "eno1", ChassisName: "sw-core-01", ChassisID: "sw1", PortID: "Gi1",
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
		LLDP: fakeLLDPService{neighbors: []*inventory.LldpNeighbor{neighbor}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lldp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/lldp status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			ChassisName string `json:"chassisName"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ChassisName != "sw-core-01" {
		t.Errorf("items = %+v, want one neighbor named sw-core-01", body.Items)
	}
}

func TestVlanCheckRoute_Authenticated(t *testing.T) {
	finding := topology.VlanFinding{BridgeRef: "bridge:pve1:vmbr1", Code: topology.VlanCheckOK, Severity: "info"}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
		LLDP: fakeLLDPService{findings: []topology.VlanFinding{finding}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lldp/vlan-check", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []topology.VlanFinding `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Code != topology.VlanCheckOK {
		t.Errorf("items = %+v", body.Items)
	}
}

func TestPortsRoute_JSONAndCSV(t *testing.T) {
	rows := []topology.PortRow{{Node: "pve1", NIC: "eno1", Switch: "sw1", Port: "Gi1", PVID: 10}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
		LLDP: fakeLLDPService{ports: rows},
	})

	t.Run("json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ports", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Items []topology.PortRow `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if len(body.Items) != 1 || body.Items[0].NIC != "eno1" {
			t.Errorf("items = %+v", body.Items)
		}
	})

	t.Run("csv", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ports?format=csv", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Errorf("Content-Type = %q, want text/csv prefix", ct)
		}
		if !strings.Contains(rec.Body.String(), "pve1,eno1,sw1,Gi1") {
			t.Errorf("CSV body missing expected row: %s", rec.Body.String())
		}
	})
}
