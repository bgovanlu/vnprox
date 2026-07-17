package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/neighbor"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
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

// TestProbeDivergenceToFinding is T-806's finding-shape golden test: a
// persisted SimDivergenceFinding row maps to source probe, check
// sim_divergence, never fixable, and a DocsLink that is the simulator
// deep link (not a docs page — a documented deviation from every other
// producer's DocsLink convention) whose query params round-trip to the
// exact src/dst/proto/port tuple.
func TestProbeDivergenceToFinding(t *testing.T) {
	row := store.SimDivergenceFinding{
		ID:               "probe:sim_divergence|guest-nic:pve1:300/net0|guest-nic:guest-nic:pve1:301/net0|tcp|22",
		SrcRef:           "guest-nic:pve1:300/net0",
		DstKind:          "guest-nic",
		DstRef:           "guest-nic:pve1:301/net0",
		Proto:            "tcp",
		Port:             22,
		SimulatedVerdict: "allow",
		ObservedOutcome:  "unreachable",
		Detail:           "Simulated verdict: allow. Observed: unreachable.",
		CreatedAt:        100, UpdatedAt: 100,
	}

	f := probeDivergenceToFinding(row)
	if f.Source != findings.SourceProbe {
		t.Errorf("source = %q, want %q", f.Source, findings.SourceProbe)
	}
	if f.Check != "sim_divergence" {
		t.Errorf("check = %q, want sim_divergence", f.Check)
	}
	if f.ID != row.ID {
		t.Errorf("id = %q, want the stored row's own id %q (reused verbatim)", f.ID, row.ID)
	}
	if f.Fixable {
		t.Error("a sim_divergence finding must never be fixable (honesty contract: never a silent correction)")
	}
	if len(f.Refs) != 1 || f.Refs[0] != row.SrcRef {
		t.Errorf("refs = %v, want [%s]", f.Refs, row.SrcRef)
	}
	// Nodes must always be a non-nil slice (Finding.Nodes has no
	// `omitempty` — a nil value here serializes as JSON `null`, which
	// crashed the frontend's findings-stream node filter; see this
	// function's own doc comment) and, for a guest-nic src, names that
	// guest's own node.
	if f.Nodes == nil {
		t.Error("nodes is nil, want a non-nil slice (serializes to JSON null otherwise)")
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("nodes = %v, want [pve1] (parsed from the src ref)", f.Nodes)
	}

	// The exact regression this task's own e2e run hit: GET /findings must
	// serialize `nodes` as `[...]`, never `null` (Finding.Nodes has no
	// `omitempty`) — the frontend's node filter iterates it unconditionally.
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(raw), `"nodes":null`) {
		t.Errorf("marshaled finding has nodes:null (crashes web/src/findings/filters.ts): %s", raw)
	}

	link, err := url.Parse(f.DocsLink)
	if err != nil {
		t.Fatalf("DocsLink %q did not parse as a URL: %v", f.DocsLink, err)
	}
	if link.Path != "/tools" {
		t.Errorf("DocsLink path = %q, want /tools", link.Path)
	}
	q := link.Query()
	if q.Get("srcKind") != "guest-nic" || q.Get("srcRef") != row.SrcRef {
		t.Errorf("DocsLink src params = %v, want srcKind=guest-nic&srcRef=%s", q, row.SrcRef)
	}
	if q.Get("dstKind") != "guest-nic" || q.Get("dstRef") != row.DstRef {
		t.Errorf("DocsLink dst params = %v, want dstKind=guest-nic&dstRef=%s", q, row.DstRef)
	}
	if q.Get("proto") != "tcp" || q.Get("port") != "22" {
		t.Errorf("DocsLink proto/port = %v, want proto=tcp&port=22", q)
	}
}

// TestSimDivergenceDeepLink_IPDst covers the ip-kind dst branch (dstIp
// instead of dstRef) — the other half of simDivergenceDeepLink's switch.
func TestSimDivergenceDeepLink_IPDst(t *testing.T) {
	link := simDivergenceDeepLink(store.SimDivergenceFinding{
		SrcRef: "guest-nic:pve1:300/net0", DstKind: "ip", DstIP: "10.20.0.5", Proto: "icmp",
	})
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()
	if q.Get("dstKind") != "ip" || q.Get("dstIp") != "10.20.0.5" {
		t.Errorf("dst params = %v, want dstKind=ip&dstIp=10.20.0.5", q)
	}
	if q.Get("dstRef") != "" {
		t.Errorf("dstRef = %q, want empty for an ip-kind dst", q.Get("dstRef"))
	}
	if _, ok := q["port"]; ok {
		t.Errorf("port present (%v) for a port=0 tuple, want omitted", q["port"])
	}
}

// TestProbeFindingsAdapter_NilRepoIsSafe mirrors
// TestIpamFindingsAdapter_NilServiceIsSafe's nil-safety convention.
func TestProbeFindingsAdapter_NilRepoIsSafe(t *testing.T) {
	a := probeFindingsAdapter{repo: nil, logger: testLogger()}
	if got := a.Findings(); got != nil {
		t.Errorf("a nil repo must contribute no findings, got %v", got)
	}
}

// TestProbeFindingsAdapter_ReadsPersistedRows proves the adapter's read
// side round-trips a real store-backed repo, end to end (not just the
// pure mapping function above).
func TestProbeFindingsAdapter_ReadsPersistedRows(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/vnprox.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewSimDivergenceRepo(db)
	if err := repo.Upsert(context.Background(), store.SimDivergenceFinding{
		ID: "probe:sim_divergence|x", SrcRef: "guest-nic:pve1:300/net0", DstKind: "external",
		Proto: "icmp", SimulatedVerdict: "deny", ObservedOutcome: "reachable",
		CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	a := probeFindingsAdapter{repo: repo, logger: testLogger()}
	fs := a.Findings()
	if len(fs) != 1 || fs[0].ID != "probe:sim_divergence|x" || fs[0].Source != findings.SourceProbe {
		t.Fatalf("Findings() = %+v, want exactly one probe:sim_divergence|x finding", fs)
	}
}
