package change_test

// T-305 acceptance criterion 3: "A generated fixing changeset, applied
// (pvemock), clears the finding on the next drift cycle (closed loop
// test)." This exercises the real chain: messy-brownfield's cross-node
// vmbr0 MTU drift (MESS 4) -> internal/drift computes a fixing changeset's
// ops -> the real change engine (validate -> apply -> confirm, against
// pvemock) applies it -> the "next drift cycle" (re-ingesting the fixed
// node's resulting interfaces file, exactly what a real poll would do)
// no longer reports the finding.
//
// Note on why the "next drift cycle" step re-parses the fake NodeAgent's
// committed interfaces file rather than re-polling pvemock's own PVE
// network API: this package's fakeNodeAgent (apply_helpers_test.go) keeps
// its own in-memory "committed file" state, separate from pvemock's own
// simulated ns.network (only the *reload task* mechanics round-trip
// through the real pvemock server — see that file's doc comment). On a
// real node the two are the same physical file, so re-parsing the agent's
// committed content is the faithful equivalent of "the next poll sees the
// applied change".

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

const fixtureMessyBrownfield = "../../testdata/clusters/messy-brownfield.yaml"

func TestDrift_FixingChangeset_ClosedLoop(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureMessyBrownfield)
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

	// Seed the graph drift.Service reads from every node's pve-network view
	// (the same declared-config source the messy-brownfield MTU drift is
	// expressed through — see internal/drift's messybrownfield_test.go).
	graph := inventory.NewGraph()
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		ifaces, ifErr := client.ListNodeNetwork(context.Background(), node)
		if ifErr != nil {
			t.Fatalf("ListNodeNetwork(%s): %v", node, ifErr)
		}
		graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, inventory.FromPVENetwork(node, ifaces))
	}

	driftSvc := drift.New(drift.Config{Graph: graph})
	finding, ok := findFixableMTUFinding(driftSvc.Findings())
	if !ok {
		t.Fatal("expected a fixable mtu_consistency finding for vmbr0's cross-node drift")
	}
	ops, title, ok := driftSvc.FixOps(finding.ID)
	if !ok || len(ops) == 0 {
		t.Fatalf("FixOps(%s) = ok=%v ops=%v, want a non-empty op list", finding.ID, ok, ops)
	}

	// Build a real apply-capable change.Service against the same pvemock
	// server, wired to the same graph so referential validation sees the
	// target bridge (mirrors apply_helpers_test.go's newHarness, plus
	// Inventory — that harness omits it since its own tests don't need
	// referential checks).
	db := openTestDB(t)
	agent := newFakeNodeAgent(pvemock.NewFixtureHostReader(srv), client)
	protectedPath := filepath.Join(t.TempDir(), "protected.json")
	svc := newService(t, change.Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db), WS: &fakeBroadcaster{},
		Nodes: agent, Snapshots: store.NewSnapshotRepo(db), Blobs: store.NewBlobRepo(db),
		Refresher: &fakeRefresher{}, TimerFunc: (&fakeTimers{}).New, ProtectedPath: protectedPath,
		Inventory: inventorySource{graph},
	})

	ctx := context.Background()
	cs, err := svc.Create(ctx, "root@pam", title, ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	validated, err := svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != change.StatusValidated {
		t.Fatalf("status after validate = %s, want validated (findings: %+v)", validated.Status, validated.Findings)
	}

	applied, err := svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}
	if _, confirmErr := svc.Confirm(ctx, cs.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("Confirm: %v", confirmErr)
	}

	// "Next drift cycle": re-ingest the fixed node's resulting interfaces
	// file (see this test's top-of-file doc comment) and re-run the
	// checks — the finding must be gone.
	targetNode := ops[0].Target.Node
	reparsed, err := host.ParseInterfaces([]byte(agent.committedFile(targetNode)))
	if err != nil {
		t.Fatalf("ParseInterfaces(%s): %v", targetNode, err)
	}
	graph.ApplyPoll(inventory.SourceHostInterfaces, inventory.Scope{Node: targetNode, Kinds: []inventory.Kind{inventory.KindBridge}},
		inventory.FromInterfaces(targetNode, reparsed))

	after := driftSvc.Findings()
	for _, f := range after {
		if f.ID == finding.ID {
			t.Fatalf("finding %s still present after applying its fix: %+v", finding.ID, f)
		}
	}
	if _, stillThere := findFixableMTUFinding(after); stillThere {
		t.Fatalf("a fixable mtu_consistency finding for vmbr0 is still present after the fix: %+v", after)
	}
}

func findFixableMTUFinding(findings []drift.Finding) (drift.Finding, bool) {
	for _, f := range findings {
		if f.Check == drift.CheckMTUConsistency && f.Fixable {
			return f, true
		}
	}
	return drift.Finding{}, false
}

// inventorySource adapts a *inventory.Graph to change.InventorySource
// (Snapshot() inventory.Snapshot) — apply_helpers_test.go's newHarness
// doesn't need this seam since its tests don't exercise referential
// validation against a populated graph.
type inventorySource struct{ g *inventory.Graph }

func (s inventorySource) Snapshot() inventory.Snapshot { return s.g.Snapshot() }
