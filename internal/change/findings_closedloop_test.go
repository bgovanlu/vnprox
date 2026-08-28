// SPDX-License-Identifier: Apache-2.0

package change_test

// T-602 acceptance criterion 4: "A computable remediation (bond slave down
// -> no; MTU mismatch -> yes) round-trips: finding -> fixing changeset ->
// applied -> finding clears." This is the same closed loop
// drift_closedloop_test.go proves for internal/drift.Service directly,
// re-run through the *unified* internal/findings.Engine — proving the
// unification didn't change the fix-lookup contract (Engine.FixOps still
// resolves to the exact ops drift.Service computed) or break the "finding
// disappears from the stream after its fix is applied" property.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

func TestFindings_MTUFix_ClosedLoop(t *testing.T) {
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

	graph := inventory.NewGraph()
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		ifaces, ifErr := client.ListNodeNetwork(context.Background(), node)
		if ifErr != nil {
			t.Fatalf("ListNodeNetwork(%s): %v", node, ifErr)
		}
		graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, inventory.FromPVENetwork(node, ifaces))
	}

	driftSvc := drift.New(drift.Config{Graph: graph})
	eng := findings.New(findings.Config{Graph: graph, Drift: driftSvc})

	finding, ok := findFixableMTUFindingUnified(eng.Findings())
	if !ok {
		t.Fatal("expected a fixable mtu_consistency finding (source=drift) for vmbr0's cross-node drift, in the unified stream")
	}
	if finding.Source != findings.SourceDrift {
		t.Fatalf("Source = %q, want %q", finding.Source, findings.SourceDrift)
	}

	ops, title, ok := eng.FixOps(finding.ID)
	if !ok || len(ops) == 0 {
		t.Fatalf("Engine.FixOps(%s) = ok=%v ops=%v, want a non-empty op list", finding.ID, ok, ops)
	}

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

	targetNode := ops[0].Target.Node
	reparsed, err := host.ParseInterfaces([]byte(agent.committedFile(targetNode)))
	if err != nil {
		t.Fatalf("ParseInterfaces(%s): %v", targetNode, err)
	}
	graph.ApplyPoll(inventory.SourceHostInterfaces, inventory.Scope{Node: targetNode, Kinds: []inventory.Kind{inventory.KindBridge}},
		inventory.FromInterfaces(targetNode, reparsed))

	after := eng.Findings()
	for _, af := range after {
		if af.ID == finding.ID {
			t.Fatalf("unified finding %s still present after applying its fix: %+v", finding.ID, af)
		}
	}
	if _, stillThere := findFixableMTUFindingUnified(after); stillThere {
		t.Fatalf("a fixable mtu_consistency finding for vmbr0 is still present in the unified stream after the fix: %+v", after)
	}
}

func findFixableMTUFindingUnified(fs []findings.Finding) (findings.Finding, bool) {
	for _, f := range fs {
		if f.Source == findings.SourceDrift && f.Check == drift.CheckMTUConsistency && f.Fixable {
			return f, true
		}
	}
	return findings.Finding{}, false
}
