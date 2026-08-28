// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

type fakeInv struct{ g *inventory.Graph }

func (f fakeInv) Snapshot() inventory.Snapshot { return f.g.Snapshot() }

// newIpamLabService builds a real *ipam.Service backed by a real *pve.Client
// talking to a pvemock server loaded from ipam-lab.yaml, plus a minimal
// hand-built inventory graph (just enough for the route-level tests below —
// internal/ipam's own package tests already cover the full merge/conflict
// golden-map assertions against this fixture).
func newIpamLabService(t *testing.T) *ipam.Service {
	t.Helper()
	fx, err := pvemock.LoadFixture("../../testdata/clusters/ipam-lab.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	g := inventory.NewGraph()
	return ipam.NewService(ipam.Config{PVE: c, Inventory: fakeInv{g: g}})
}

func TestIPAMRoute_Subnets_OK(t *testing.T) {
	svc := newIpamLabService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPAM: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipam/subnets", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ipam/subnets status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []struct {
			CIDR string `json:"cidr"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	found := false
	for _, it := range got.Items {
		if it.CIDR == "10.50.0.0/24" {
			found = true
		}
	}
	if !found {
		t.Fatalf("10.50.0.0/24 missing from response: %+v", got.Items)
	}
}

// TestIPAMRoute_Allocations_CIDRWithSlash proves the trailing-wildcard route
// correctly recovers a CIDR (which itself contains '/') from the URL path,
// both percent-encoded and not (mirrors topology.go's handleInventoryDetail
// coverage for the same Ref-encoding issue).
func TestIPAMRoute_Allocations_CIDRWithSlash(t *testing.T) {
	svc := newIpamLabService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPAM: svc,
	})

	paths := []string{
		"/api/v1/ipam/subnets/10.50.0.0/24/allocations",
		"/api/v1/ipam/subnets/" + url.PathEscape("10.50.0.0/24") + "/allocations",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
		var got struct {
			CIDR       string `json:"cidr"`
			Entries    []any  `json:"entries"`
			FreeRanges []any  `json:"freeRanges"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding response for %s: %v", path, err)
		}
		if got.CIDR != "10.50.0.0/24" {
			t.Errorf("path %s: cidr = %q, want 10.50.0.0/24", path, got.CIDR)
		}
		// The brownfield lab fixture has occupied addresses (entries) and
		// free space between them (ranges) — both must survive the round trip.
		if len(got.Entries) == 0 {
			t.Errorf("path %s: got no occupied entries, want the fixture's allocations", path)
		}
		if len(got.FreeRanges) == 0 {
			t.Errorf("path %s: got no free ranges, want the /24's unallocated gaps", path)
		}
	}
}

func TestIPAMRoute_Allocations_CSVExport(t *testing.T) {
	svc := newIpamLabService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPAM: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipam/subnets/10.50.0.0/24/allocations?format=csv", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", rec.Header().Get("Content-Disposition"))
	}
	if !strings.HasPrefix(rec.Body.String(), "ip,state,confidence") {
		n := 40
		if rec.Body.Len() < n {
			n = rec.Body.Len()
		}
		t.Errorf("csv body does not start with the expected header: %q", rec.Body.String()[:n])
	}
}

func TestIPAMRoute_Allocations_UnknownSubnet404(t *testing.T) {
	svc := newIpamLabService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPAM: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipam/subnets/203.0.113.0/24/allocations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestIPAMRoute_Unauthenticated401(t *testing.T) {
	svc := newIpamLabService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, IPAM: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipam/subnets", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestIPAMRoute_Allocations_WizardCreatedGatewayIsVisible is T-701
// acceptance criterion 4's full-stack check: a simple-zone wizard's drafted
// ops (zone+vnet+subnet-with-gateway), realized directly against pvemock
// exactly as the change engine's PVEStageOp would (internal/pve.Client's
// own Create* calls — the same wire calls cmd/vnproxd/changeagent.go's
// SDNStageOp makes), leave a `gateway: true` IPAM allocation readable
// through the real /ipam/subnets/{cidr}/allocations route — proving
// pvemock's new gateway-registration fidelity (ipam.go's
// registerSubnetGateway) is actually wired all the way through
// internal/ipam's merge, not just visible at the raw PVE API layer
// (already covered by internal/pvemock's own sdn_test.go).
func TestIPAMRoute_Allocations_WizardCreatedGatewayIsVisible(t *testing.T) {
	fx, err := pvemock.LoadFixture("../../testdata/clusters/single-node.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()
	if err := c.CreateSDNZone(ctx, pve.SDNZone{ID: "homelab", Type: "simple", Nodes: []string{"pve1"}}); err != nil {
		t.Fatalf("CreateSDNZone: %v", err)
	}
	if err := c.CreateSDNVnet(ctx, pve.SDNVnet{ID: "vnet1", Zone: "homelab"}); err != nil {
		t.Fatalf("CreateSDNVnet: %v", err)
	}
	if err := c.CreateSDNSubnet(ctx, "vnet1", pve.SDNSubnet{ID: pve.SDNSubnetID("10.50.0.0/24"), Vnet: "vnet1", CIDR: "10.50.0.0/24", Gateway: "10.50.0.1"}); err != nil {
		t.Fatalf("CreateSDNSubnet: %v", err)
	}

	g := inventory.NewGraph()
	svc := ipam.NewService(ipam.Config{PVE: c, Inventory: fakeInv{g: g}})
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPAM: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipam/subnets/10.50.0.0/24/allocations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET allocations status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Entries []struct {
			IP    string `json:"ip"`
			State string `json:"state"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	found := false
	for _, c := range got.Entries {
		if c.IP == "10.50.0.1" {
			found = true
			if c.State != "gateway" {
				t.Errorf("10.50.0.1 state = %q, want %q", c.State, "gateway")
			}
		}
	}
	if !found {
		t.Fatalf("10.50.0.1 missing from address list entries: %+v", got.Entries)
	}
}

func TestIPAMRoute_NotMountedWhenServiceNil(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPAM: nil,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipam/subnets", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
