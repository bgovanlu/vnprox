package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// newIpamLabServiceWithInventory is newIpamLabService (ipam_test.go) plus a
// real guest-nic-bearing inventory graph, so DHCP()'s guestRef-by-MAC
// correlation has something to resolve against.
func newIpamLabServiceWithInventory(t *testing.T) *ipam.Service {
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
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{}, []inventory.Entity{
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "300"}, VMID: 300, Name: "web1", Type: "qemu", Node: "pve1", Status: "running"},
		&inventory.GuestNic{
			Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "300/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "300"}, Key: "net0",
			TargetName: "vmbr0", Mac: "AA:BB:CC:DD:EE:01", Vid: 10,
		},
	})
	return ipam.NewService(ipam.Config{PVE: c, Inventory: fakeInv{g: g}})
}

// TestDHCPRoute_ReservationsAndZoneFilter is T-406 acceptance criterion 2
// at the mounted-route level: reservations render with a resolved guestRef
// where a known guest matches, and ?zone= scoping works.
func TestDHCPRoute_ReservationsAndZoneFilter(t *testing.T) {
	svc := newIpamLabServiceWithInventory(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, DHCP: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/dhcp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sdn/dhcp status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Reservations []struct {
			IP       string `json:"ip"`
			Hostname string `json:"hostname"`
			MAC      string `json:"mac"`
			GuestRef string `json:"guestRef"`
		} `json:"reservations"`
		Leases      []json.RawMessage `json:"leases"`
		GeneratedAt int64             `json:"generatedAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Reservations) == 0 {
		t.Fatal("no reservations in GET /sdn/dhcp response")
	}
	var web1 *struct {
		IP       string `json:"ip"`
		Hostname string `json:"hostname"`
		MAC      string `json:"mac"`
		GuestRef string `json:"guestRef"`
	}
	for i := range got.Reservations {
		if got.Reservations[i].IP == "10.50.0.10" {
			web1 = &got.Reservations[i]
		}
	}
	if web1 == nil {
		t.Fatal("10.50.0.10 (web1) reservation missing")
	}
	if web1.GuestRef == "" {
		t.Errorf("web1 reservation has no guestRef, want correlation by mac to the fixture's guest-nic")
	}

	// zone filter: an unknown zone yields no reservations.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/dhcp?zone=no-such-zone", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /sdn/dhcp?zone=no-such-zone status = %d", rec2.Code)
	}
	var empty struct {
		Reservations []json.RawMessage `json:"reservations"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(empty.Reservations) != 0 {
		t.Errorf("zone=no-such-zone reservations = %v, want none", empty.Reservations)
	}
}

// TestDHCPRoute_Unauthenticated401 mirrors every other mounted route's
// auth-gating test.
func TestDHCPRoute_Unauthenticated401(t *testing.T) {
	svc := newIpamLabServiceWithInventory(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, DHCP: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/dhcp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestDHCPRoute_NilService404 mirrors every other mounted route's
// nil-degraded-mode test: no DHCP service wired -> route isn't mounted at
// all, so it 404s.
func TestDHCPRoute_NilService404(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sdn/dhcp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
