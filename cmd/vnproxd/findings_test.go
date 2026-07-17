package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/neighbor"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

func TestIpamConflictToFinding(t *testing.T) {
	f := ipamConflictToFinding(ipam.SubnetConflict{
		CIDR: "10.50.0.0/24",
		Conflict: ipam.Conflict{
			Type:       "duplicate_ip",
			Severity:   findings.SeverityError,
			Message:    "two guests claim 10.50.0.5",
			Suggestion: "release one of them",
			IPs:        []string{"10.50.0.5"},
		},
	})

	if f.Source != findings.SourceIPAM {
		t.Errorf("source = %q, want %q", f.Source, findings.SourceIPAM)
	}
	if f.Check != "duplicate_ip" {
		t.Errorf("check = %q", f.Check)
	}
	if f.Severity != findings.SeverityError {
		t.Errorf("severity = %q", f.Severity)
	}
	// Stable, content-derived id: source, type, subnet, sorted addresses.
	if want := "ipam:duplicate_ip|10.50.0.0/24|10.50.0.5"; f.ID != want {
		t.Errorf("id = %q, want %q", f.ID, want)
	}
	if f.Fixable {
		t.Error("IPAM conflicts carry no computed fix op — Fixable must be false")
	}
	if f.DocsLink == "" {
		t.Error("a non-fixable finding must carry a docs link")
	}
	if !strings.Contains(f.Detail, "release one of them") {
		t.Errorf("detail should fold in the suggestion, got %q", f.Detail)
	}
}

func TestIpamFindingsAdapter_NilServiceIsSafe(t *testing.T) {
	a := ipamFindingsAdapter{ipam: nil, logger: testLogger()}
	if got := a.Findings(); got != nil {
		t.Errorf("a nil ipam service must contribute no findings, got %v", got)
	}
}

// emptyInventory satisfies ipam.InventorySource with an empty graph — the
// neighbor-sourced observed_unallocated conflict this test exercises
// doesn't consult inventory data at all (unlike allocated_dark/
// duplicate_ip), so no guest/bridge fixture data is needed here.
type emptyInventory struct{}

func (emptyInventory) Snapshot() inventory.Snapshot { return inventory.NewGraph().Snapshot() }

// TestIpamFindingsAdapter_NeighborSourcedConflict_UsesExistingIDConvention
// is T-805 acceptance criterion 6: a neighbor-sourced observed_unallocated
// finding (the new data source this task adds) flows through
// ipamFindingsAdapter/ipamConflictToFinding completely unchanged, producing
// the same `ipam:<type>|<cidr>|<sorted-ips>` id shape as every other
// source — the merge/conflict/findings pipeline this task feeds into is
// untouched by this task, so a conflict's Finding never records which
// enrichment source (guest-agent, dhcp-lease, neighbor) produced its
// underlying Observation.
func TestIpamFindingsAdapter_NeighborSourcedConflict_UsesExistingIDConvention(t *testing.T) {
	f, err := pvemock.LoadFixture("../../testdata/clusters/ipam-lab.yaml")
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
	neighborSvc := neighbor.NewService(neighbor.Config{
		Host:      host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		LocalNode: func() string { return "pve1" },
	})
	svc := ipam.NewService(ipam.Config{PVE: client, Inventory: emptyInventory{}, Neighbors: neighborSvc})

	a := ipamFindingsAdapter{ipam: svc, logger: testLogger()}
	fs := a.Findings()

	var got *findings.Finding
	for i := range fs {
		if fs[i].Check == "observed_unallocated" && strings.Contains(fs[i].ID, "10.50.0.55") {
			got = &fs[i]
		}
	}
	if got == nil {
		t.Fatalf("no observed_unallocated finding for 10.50.0.55 among %+v", fs)
	}
	if want := "ipam:observed_unallocated|10.50.0.0/24|10.50.0.55"; got.ID != want {
		t.Errorf("id = %q, want %q (existing convention unchanged)", got.ID, want)
	}
	if got.Source != findings.SourceIPAM {
		t.Errorf("source = %q, want %q", got.Source, findings.SourceIPAM)
	}
	if got.Fixable {
		t.Error("IPAM conflicts carry no computed fix op")
	}
}
