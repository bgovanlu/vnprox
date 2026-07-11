package ipam_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// newIpamLabWriteClient builds a fresh pvemock server + *pve.Client against
// ipam-lab.yaml, like newIpamTestService does, but returns the client
// itself (not just an *ipam.Service) so a test can also issue the raw
// IPAM write calls (CreateIPAMAllocation/DeleteIPAMAllocation) the change
// engine's ipam.alloc.create/delete ops drive in production.
func newIpamLabWriteClient(t *testing.T) *pve.Client {
	t.Helper()
	f, err := pvemock.LoadFixture(ipamLabFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return client
}

// TestService_DHCP_Reservations is T-406 acceptance criterion 2 (part 1):
// every MAC-bound PVE-IPAM allocation on a DHCP-enabled subnet
// (ipam-lab.yaml's 10.50.0.0/24, with a dhcp_range_start/end this task
// added) appears as a reservation, correlated to its known guest by MAC
// where one exists — the gateway (no MAC) is excluded.
func TestService_DHCP_Reservations(t *testing.T) {
	svc := newIpamTestService(t)
	ctx := context.Background()

	view, err := svc.DHCP(ctx, "")
	if err != nil {
		t.Fatalf("DHCP: %v", err)
	}

	byIP := map[string]ipam.Reservation{}
	for _, r := range view.Reservations {
		byIP[r.IP] = r
	}

	web1, ok := byIP["10.50.0.10"]
	if !ok {
		t.Fatal("10.50.0.10 (web1) missing from DHCP reservations")
	}
	if web1.Hostname != "web1" || web1.MAC != "AA:BB:CC:DD:EE:01" {
		t.Errorf("web1 reservation = %+v, want hostname=web1 mac=AA:BB:CC:DD:EE:01", web1)
	}
	if web1.GuestRef == "" {
		t.Errorf("web1 reservation has no guestRef, want it correlated by MAC to guest-nic:pve1:300/net0's owning guest")
	}

	web2, ok := byIP["10.50.0.20"]
	if !ok || web2.Hostname != "web2" {
		t.Errorf("10.50.0.20 (web2) reservation = %+v, want hostname web2", web2)
	}

	ghost, ok := byIP["10.50.0.11"]
	if !ok {
		t.Fatal("10.50.0.11 (ghost/allocated_dark) missing from DHCP reservations")
	}
	if ghost.GuestRef != "" {
		t.Errorf("ghost reservation guestRef = %q, want empty (no real guest matches its mac)", ghost.GuestRef)
	}

	if _, ok := byIP["10.50.0.1"]; ok {
		t.Error("gateway address must not appear as a DHCP reservation")
	}

	for _, r := range view.Reservations {
		if r.CIDR != "10.50.0.0/24" || r.Zone != "labz" || r.Vnet != "vnet10" {
			t.Errorf("reservation %+v missing/wrong cidr/zone/vnet context", r)
		}
	}
}

// TestService_DHCP_ZoneFilter checks the ?zone= scoping.
func TestService_DHCP_ZoneFilter(t *testing.T) {
	svc := newIpamTestService(t)
	ctx := context.Background()

	view, err := svc.DHCP(ctx, "labz")
	if err != nil {
		t.Fatalf("DHCP: %v", err)
	}
	if len(view.Reservations) == 0 {
		t.Fatal("zone=labz should still include labz's reservations")
	}

	empty, err := svc.DHCP(ctx, "no-such-zone")
	if err != nil {
		t.Fatalf("DHCP: %v", err)
	}
	if len(empty.Reservations) != 0 {
		t.Errorf("zone=no-such-zone reservations = %+v, want none", empty.Reservations)
	}
}

// fakeLeaseSource is a hand-rolled LeaseSource test double.
type fakeLeaseSource struct {
	err error
	obs []ipam.Observation
}

func (f fakeLeaseSource) Leases(context.Context) ([]ipam.Observation, error) {
	return f.obs, f.err
}

// TestService_DHCP_LeasesCorrelateToGuestByMAC is T-406 acceptance
// criterion 3's correlation half: a lease observation whose MAC matches a
// fixture guest NIC's MAC resolves that lease's guestRef.
func TestService_DHCP_LeasesCorrelateToGuestByMAC(t *testing.T) {
	client := newIpamLabWriteClient(t)

	leases := fakeLeaseSource{obs: []ipam.Observation{
		// Lower-case MAC, matching web1's fixture MAC
		// (AA:BB:CC:DD:EE:01) case-insensitively -- correlation must
		// normalize case.
		{IP: "10.50.0.150", MAC: "aa:bb:cc:dd:ee:01", Hostname: "dhcp-web1", Source: "dhcp-lease"},
		// No known guest owns this MAC.
		{IP: "10.50.0.160", MAC: "de:ad:be:ef:00:00", Hostname: "unknown-host", Source: "dhcp-lease"},
		// Outside every DHCP-enabled subnet -- dropped.
		{IP: "192.168.99.50", MAC: "11:22:33:44:55:66", Source: "dhcp-lease"},
	}}

	svc := ipam.NewService(ipam.Config{PVE: client, Inventory: ipamLabInventory(), Leases: leases})
	view, err := svc.DHCP(context.Background(), "")
	if err != nil {
		t.Fatalf("DHCP: %v", err)
	}
	if len(view.Leases) != 2 {
		t.Fatalf("leases = %+v, want 2 (one outside any dhcp subnet dropped)", view.Leases)
	}

	byIP := map[string]ipam.Lease{}
	for _, l := range view.Leases {
		byIP[l.IP] = l
	}
	known, ok := byIP["10.50.0.150"]
	if !ok || known.GuestRef == "" {
		t.Errorf("lease at 10.50.0.150 = %+v, want a resolved guestRef (mac matches web1)", known)
	}
	unknown, ok := byIP["10.50.0.160"]
	if !ok || unknown.GuestRef != "" {
		t.Errorf("lease at 10.50.0.160 = %+v, want empty guestRef (no known guest owns this mac)", unknown)
	}
}

// TestService_DHCP_ReservationIsOneRecordNotTwo is T-406 acceptance
// criterion 2's core claim: a reservation created from a guest's MAC is
// genuinely a single stored PVE-IPAM record, visible as both an allocated
// IPAM grid cell and a DHCP reservation -- not two independently-kept
// copies. Proven by mutating the underlying record exactly once (a single
// ipam.alloc.create, then a single ipam.alloc.delete against the same
// vnet+ip) and asserting both views appear/disappear together: if DHCP()
// or Allocations() were backed by separate storage, one could show stale
// data after the other's mutation.
func TestService_DHCP_ReservationIsOneRecordNotTwo(t *testing.T) {
	client := newIpamLabWriteClient(t)
	svc := ipam.NewService(ipam.Config{PVE: client, Inventory: ipamLabInventory()})
	ctx := context.Background()

	const ip, mac, hostname = "10.50.0.222", "de:ad:be:ef:12:34", "new-reservation"

	// Before: absent from both views.
	beforeGrid, err := svc.Allocations(ctx, "10.50.0.0/24", ipam.GridOptions{})
	if err != nil {
		t.Fatalf("Allocations (before): %v", err)
	}
	if s := cellState(beforeGrid, ip); s == ipam.CellAllocated || s == ipam.CellReserved {
		t.Fatalf("%s already allocated/reserved before the test creates it", ip)
	}
	beforeView, err := svc.DHCP(ctx, "")
	if err != nil {
		t.Fatalf("DHCP (before): %v", err)
	}
	if reservationFor(beforeView, ip) != nil {
		t.Fatalf("%s already a reservation before the test creates it", ip)
	}

	// The single mutation: one ipam.alloc.create-equivalent PVE write (the
	// same write internal/change's executor issues for that op — see
	// apply_ipam_test.go's TestApply_IpamAlloc_ReserveThenRelease for the
	// change-engine-level round trip of this exact call).
	if createErr := client.CreateIPAMAllocation(ctx, "vnet10", pve.IPAMAllocation{
		IP: ip, MAC: mac, Hostname: hostname, Zone: "labz", Subnet: "10.50.0.0/24",
	}); createErr != nil {
		t.Fatalf("CreateIPAMAllocation: %v", createErr)
	}

	// After the single create: both views reflect it, with the same
	// hostname/mac -- because both are reading the one record PVE now has.
	afterGrid, err := svc.Allocations(ctx, "10.50.0.0/24", ipam.GridOptions{})
	if err != nil {
		t.Fatalf("Allocations (after create): %v", err)
	}
	cell := findCell(afterGrid, ip)
	// No VMID on this allocation -> merge.go's resolve() classifies it as
	// a manual reservation (CellReserved), not CellAllocated (which is
	// reserved for a VMID-tied record) -- see mergeEntry.resolve's "No
	// VMID: a manual reservation, not tied to any guest" case. That is
	// exactly the DHCP-reservation shape this test is proving stays in
	// sync with the grid, so CellReserved is the correct expectation here.
	if cell == nil || cell.State != ipam.CellReserved || cell.MAC != mac || cell.Hostname != hostname {
		t.Fatalf("grid cell after create = %+v, want reserved with mac=%s hostname=%s", cell, mac, hostname)
	}
	afterView, err := svc.DHCP(ctx, "")
	if err != nil {
		t.Fatalf("DHCP (after create): %v", err)
	}
	res := reservationFor(afterView, ip)
	if res == nil || res.MAC != mac || res.Hostname != hostname {
		t.Fatalf("DHCP reservation after create = %+v, want mac=%s hostname=%s", res, mac, hostname)
	}

	// The single reverse mutation: one ipam.alloc.delete-equivalent write.
	if deleteErr := client.DeleteIPAMAllocation(ctx, "vnet10", ip, "10.50.0.0/24"); deleteErr != nil {
		t.Fatalf("DeleteIPAMAllocation: %v", deleteErr)
	}

	// After the single delete: gone from *both* views. If DHCP() held its
	// own separate copy of reservations, this delete (which only touches
	// PVE's IPAM entries, the same store Allocations reads) would leave a
	// stale reservation behind.
	finalGrid, err := svc.Allocations(ctx, "10.50.0.0/24", ipam.GridOptions{})
	if err != nil {
		t.Fatalf("Allocations (after delete): %v", err)
	}
	if s := cellState(finalGrid, ip); s == ipam.CellAllocated || s == ipam.CellReserved {
		t.Errorf("%s still allocated/reserved in the grid after delete", ip)
	}
	finalView, err := svc.DHCP(ctx, "")
	if err != nil {
		t.Fatalf("DHCP (after delete): %v", err)
	}
	if reservationFor(finalView, ip) != nil {
		t.Errorf("%s still a DHCP reservation after delete -- proves a second, out-of-sync record existed", ip)
	}
}

func findCell(grid ipam.AllocationGrid, ip string) *ipam.Cell {
	for i := range grid.Cells {
		if grid.Cells[i].IP == ip {
			return &grid.Cells[i]
		}
	}
	return nil
}

func cellState(grid ipam.AllocationGrid, ip string) ipam.CellState {
	c := findCell(grid, ip)
	if c == nil {
		return ""
	}
	return c.State
}

func reservationFor(view ipam.DHCPView, ip string) *ipam.Reservation {
	for i := range view.Reservations {
		if view.Reservations[i].IP == ip {
			return &view.Reservations[i]
		}
	}
	return nil
}
