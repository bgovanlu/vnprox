package ipam_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/neighbor"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// newIpamTestServiceWithNeighbors builds an *ipam.Service exactly like
// newIpamTestService, but with Config.Neighbors wired to a real
// internal/neighbor.Service backed by the same fixture's
// host.Reader-shaped data (ipam-lab.yaml's NodeSpec.Neighbors, T-805) —
// the full local-collection pipeline (host.FixtureReader.Neighbors ->
// internal/neighbor.Service -> ipam.NeighborSource), not a hand-rolled
// test double, since T-805 acceptance criteria 3/4 are about the
// end-to-end merge, not just the interface point.
func newIpamTestServiceWithNeighbors(t *testing.T) *ipam.Service {
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

	hostReader := host.NewFixtureReader(pvemock.NewFixtureHostReader(srv))
	neighborSvc := neighbor.NewService(neighbor.Config{
		Host:      hostReader,
		LocalNode: func() string { return "pve1" },
	})

	return ipam.NewService(ipam.Config{PVE: client, Inventory: ipamLabInventory(), Neighbors: neighborSvc})
}

// TestService_Neighbors_ObservedUnallocated is T-805 acceptance criterion
// 3: a neighbor-observed IP with no PVE-IPAM allocation renders
// confidence "observed", sources ["neighbor"], and an
// observed_unallocated conflict.
func TestService_Neighbors_ObservedUnallocated(t *testing.T) {
	svc := newIpamTestServiceWithNeighbors(t)
	ctx := context.Background()

	list, err := svc.Allocations(ctx, "10.50.0.0/24")
	if err != nil {
		t.Fatalf("Allocations: %v", err)
	}

	cell := findCell(list, "10.50.0.55")
	if cell == nil {
		t.Fatal("10.50.0.55 missing from address list entries")
	}
	if cell.State != ipam.CellObserved || cell.Confidence != ipam.ConfidenceObserved {
		t.Errorf("10.50.0.55 = state=%s confidence=%s, want observed/observed", cell.State, cell.Confidence)
	}
	if len(cell.Sources) != 1 || cell.Sources[0] != "neighbor" {
		t.Errorf("10.50.0.55 sources = %v, want [neighbor]", cell.Sources)
	}

	found := false
	for _, c := range list.Conflicts {
		if c.Type == "observed_unallocated" && len(c.IPs) == 1 && c.IPs[0] == "10.50.0.55" {
			found = true
		}
	}
	if !found {
		t.Errorf("no observed_unallocated conflict for 10.50.0.55 among %+v", list.Conflicts)
	}

	// The FAILED neighbor entry (10.50.0.57) must never reach the merged
	// address list at all -- the fixture-to-Reader filtering pipeline
	// excludes it upstream of ipam's own merge.
	if findCell(list, "10.50.0.57") != nil {
		t.Error("10.50.0.57 (FAILED neighbor state) must not appear in the address list")
	}
}

// TestService_Neighbors_CorroboratesAllocation is T-805 acceptance
// criterion 4: an already-allocated address with a corroborating neighbor
// observation (same MAC) renders confidence "both".
func TestService_Neighbors_CorroboratesAllocation(t *testing.T) {
	svc := newIpamTestServiceWithNeighbors(t)
	ctx := context.Background()

	list, err := svc.Allocations(ctx, "10.50.0.0/24")
	if err != nil {
		t.Fatalf("Allocations: %v", err)
	}

	cell := findCell(list, "10.50.0.20")
	if cell == nil {
		t.Fatal("10.50.0.20 missing from address list entries")
	}
	if cell.Confidence != ipam.ConfidenceBoth {
		t.Errorf("10.50.0.20 confidence = %s, want both (allocation + neighbor sighting agree)", cell.Confidence)
	}
	found := false
	for _, s := range cell.Sources {
		if s == "neighbor" {
			found = true
		}
	}
	if !found {
		t.Errorf("10.50.0.20 sources = %v, want to include neighbor", cell.Sources)
	}
}

// fakeNeighborSource is a hand-rolled NeighborSource test double.
type fakeNeighborSource struct {
	err error
	obs []ipam.Observation
}

func (f fakeNeighborSource) Neighbors(context.Context) ([]ipam.Observation, error) {
	return f.obs, f.err
}

// TestService_Neighbors_NilSourceContributesNothing proves the documented
// nil-safety contract on ipam.Config.Neighbors (NeighborSource's own doc
// comment: "A nil NeighborSource contributes no observations"): omitting
// Config.Neighbors entirely (as every other ipam.Service test in this
// package already does) must not error or panic.
func TestService_Neighbors_NilSourceContributesNothing(t *testing.T) {
	svc := newIpamTestService(t)
	list, err := svc.Allocations(context.Background(), "10.50.0.0/24")
	if err != nil {
		t.Fatalf("Allocations: %v", err)
	}
	if findCell(list, "10.50.0.55") != nil {
		t.Error("10.50.0.55 must not appear without a wired NeighborSource")
	}
}

// TestService_Neighbors_FailingSourceDegradesGracefully proves
// enrichmentObservations' existing "err == nil" gate already covers a
// NeighborSource error exactly like it does for LeaseSource -- a failing
// neighbor collector must not fail the whole Allocations call.
func TestService_Neighbors_FailingSourceDegradesGracefully(t *testing.T) {
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

	svc := ipam.NewService(ipam.Config{
		PVE: client, Inventory: ipamLabInventory(),
		Neighbors: fakeNeighborSource{err: context.DeadlineExceeded},
	})
	if _, err := svc.Allocations(context.Background(), "10.50.0.0/24"); err != nil {
		t.Fatalf("Allocations: %v, want a failing NeighborSource to degrade quietly", err)
	}
}
