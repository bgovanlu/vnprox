// SPDX-License-Identifier: Apache-2.0

package change_test

// T-2703 acceptance criterion 2: "'Restore intent' produces a changeset which,
// if applied, makes the drift finding clear; asserted by applying it against
// the mock and re-running the detector."
//
// This is deliberately the whole real chain and not a unit test of the op
// patch: a three-position drift finding (spec / config / live) -> the ops
// internal/drift computes for the "restore intent" action -> the real change
// engine (validate -> apply -> confirm, against pvemock) -> the next drift
// cycle, which re-ingests the interfaces file the apply actually wrote. The
// assertion is SEMANTIC in the way the card asks for: the detector is re-run
// against the post-apply state, rather than the ops being compared against an
// expected list.
//
// It sits in this package for the same reason drift_closedloop_test.go does:
// applying needs the real change.Service and its fake node agent, and the
// agent's committed file is the faithful stand-in for "the next poll sees it"
// (see that file's doc comment).

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
	"github.com/bgovanlu/vnprox/internal/spec"
	"github.com/bgovanlu/vnprox/internal/store"
)

// staticSpecPin is a drift.PinProvider over one fixed document — the "spec"
// position. In the daemon this is the document T-2701 fetched from git.
type staticSpecPin string

func (p staticSpecPin) Pin() (string, bool) { return string(p), true }

// TestReconcile_RestoreIntent_ClosedLoop is AC2.
func TestReconcile_RestoreIntent_ClosedLoop(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
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
	const node = "pve1"
	ifaces, err := client.ListNodeNetwork(context.Background(), node)
	if err != nil {
		t.Fatalf("ListNodeNetwork(%s): %v", node, err)
	}
	graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, inventory.FromPVENetwork(node, ifaces))

	// The spec position: the cluster exactly as it is, except that vmbr0 is
	// declared with an MTU it does not currently have — one entity whose spec
	// and config disagree, which "restore intent" resolves by moving the
	// cluster rather than the document.
	//
	// The single-node fixture is deliberate. The cross-node consistency
	// validator refuses a changeset that would leave same-named bridges at
	// different MTUs on different nodes, so on a multi-node fixture a
	// per-entity restore would be refused for a reason that has nothing to do
	// with this card — the divergence under test is node-local, and so is the
	// cluster it is tested on.
	const restoreMTU = 9000
	doc := spec.Export(graph.Snapshot())
	target := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	if !setBridgeMTU(&doc, target, restoreMTU) {
		t.Fatalf("fixture has no %s to diverge", target)
	}
	content, err := spec.Marshal(doc)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}

	driftSvc := drift.New(drift.Config{Graph: graph, Pins: staticSpecPin(content)})
	finding, ok := reconcileFindingFor(driftSvc.Findings(), target)
	if !ok {
		t.Fatalf("expected a spec_reconciliation finding for %s; findings: %+v", target, driftSvc.Findings())
	}
	if finding.Reconcile == nil || !finding.Reconcile.Actions.RestoreIntent {
		t.Fatalf("the finding does not offer restore intent: %+v", finding.Reconcile)
	}

	ops, title, ok := driftSvc.RestoreIntentOps(finding.ID)
	if !ok || len(ops) == 0 {
		t.Fatalf("RestoreIntentOps(%s) = ok=%v ops=%v, want a non-empty op patch", finding.ID, ok, ops)
	}

	// The real change engine, against the same pvemock server.
	db := openTestDB(t)
	agent := newFakeNodeAgent(pvemock.NewFixtureHostReader(srv), client)
	svc := newService(t, change.Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db), WS: &fakeBroadcaster{},
		Nodes: agent, Snapshots: store.NewSnapshotRepo(db), Blobs: store.NewBlobRepo(db),
		Refresher: &fakeRefresher{}, TimerFunc: (&fakeTimers{}).New,
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
		Inventory:     inventorySource{graph},
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

	// "The next drift cycle": re-ingest the interfaces file the apply wrote.
	reparsed, err := host.ParseInterfaces([]byte(agent.committedFile(target.Node)))
	if err != nil {
		t.Fatalf("ParseInterfaces(%s): %v", target.Node, err)
	}
	graph.ApplyPoll(inventory.SourceHostInterfaces,
		inventory.Scope{Node: target.Node, Kinds: []inventory.Kind{inventory.KindBridge}},
		inventory.FromInterfaces(target.Node, reparsed))

	// Re-run the detector. The finding must be gone — and gone because the
	// cluster moved to the spec, not because the detector stopped looking.
	after := driftSvc.Findings()
	for _, got := range after {
		if got.ID == finding.ID {
			t.Fatalf("finding %s still present after applying its restore-intent changeset: %+v", finding.ID, got)
		}
	}
	if _, stillThere := reconcileFindingFor(after, target); stillThere {
		t.Fatalf("a spec_reconciliation finding for %s survives the restore: %+v", target, after)
	}
	if got := bridgeDeclaredMTU(graph.Snapshot(), target); got != restoreMTU {
		t.Fatalf("%s declares MTU %d after the restore, want the spec's %d — the finding cleared for the wrong reason",
			target, got, restoreMTU)
	}
}

// setBridgeMTU rewrites one bridge's declared MTU in a spec document.
func setBridgeMTU(doc *spec.Spec, ref inventory.Ref, mtu int) bool {
	for i := range doc.Nodes {
		if doc.Nodes[i].Name != ref.Node {
			continue
		}
		for j := range doc.Nodes[i].Bridges {
			if doc.Nodes[i].Bridges[j].Name == ref.ID {
				doc.Nodes[i].Bridges[j].MTU = mtu
				return true
			}
		}
	}
	return false
}

func reconcileFindingFor(findings []drift.Finding, ref inventory.Ref) (drift.Finding, bool) {
	for _, f := range findings {
		if f.Check != drift.CheckSpecReconciliation {
			continue
		}
		for _, r := range f.Refs {
			if r == ref.String() {
				return f, true
			}
		}
	}
	return drift.Finding{}, false
}

func bridgeDeclaredMTU(snap inventory.Snapshot, ref inventory.Ref) int {
	e, ok := snap.Get(ref)
	if !ok {
		return 0
	}
	br, ok := e.(*inventory.Bridge)
	if !ok {
		return 0
	}
	return br.MTUDeclared
}
