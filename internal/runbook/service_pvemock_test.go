// SPDX-License-Identifier: Apache-2.0

package runbook_test

// T-4003 acceptance criterion 3: "at least one runbook round-trips through
// internal/pvemock: finding fires, runbook prepares, the resulting
// changeset validates clean." This file is that round trip, exercising the
// real stack: a graph populated by an actual poll against pvemock (not a
// hand-built fixture), a real findings.Engine producing the finding via its
// genuine checkOrphanVnet production path (not a hand-built Finding), and a
// real change.Service validating the result.
//
// The orphan condition itself is injected on top of the polled graph via
// one extra ApplyPoll (single-node.yaml's own SDN config is empty) — the
// identical "poll real fixture data, then inject the one condition under
// test" pattern internal/drift/specdrift_test.go's own
// TestSpecDrift_ThreeNodeVlanFixture_MutatedBridgeMTU_OneFinding test uses.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/runbook"
	"github.com/bgovanlu/vnprox/internal/store"
)

const fixtureSingleNode = "../../testdata/clusters/single-node.yaml"

func TestService_Prepare_RoundTripsThroughPvemock(t *testing.T) {
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
	c, err := collect.New(collect.Config{
		PVE:   client,
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, refreshErr := c.RefreshNow(ctx, inventory.Scope{}); refreshErr != nil {
		t.Fatalf("RefreshNow: %v", refreshErr)
	}

	// Inject the orphan condition on top of the real poll: a VNet whose
	// zone the poll never reported (single-node.yaml ships no SDN config
	// at all, so this is additive, not a mutation of anything polled).
	vnetRef := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "orphvn"}
	graph.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{Kinds: []inventory.Kind{inventory.KindSDNVnet}},
		[]inventory.Entity{&inventory.SdnVnet{Ref: vnetRef, ID: "orphvn", Zone: "goneZone"}})

	// Real findings engine, real production check path.
	engine := findings.New(findings.Config{Graph: graph})
	var fired findings.Finding
	found := false
	for _, item := range engine.Findings() {
		if item.Check == "orphan_vnet" {
			fired = item
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("findings engine did not report orphan_vnet for the injected vnet; findings = %+v", engine.Findings())
	}

	// Real change.Service, same graph.
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	changeSvc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Inventory:  graph,
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	rbSvc := runbook.New(runbook.Config{
		Changes:   changeSvc,
		Findings:  singleFindingProvider{fired},
		Inventory: graph,
	})

	cs, err := rbSvc.Prepare(context.Background(), "alice@pam", fired.ID, runbook.DeleteOrphanVnet)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if cs.Status != change.StatusValidated {
		t.Fatalf("Status = %s, want %s; findings = %+v", cs.Status, change.StatusValidated, cs.Findings)
	}
	if len(cs.Ops) != 1 || cs.Ops[0].Type != change.OpSdnVnetDelete || cs.Ops[0].Target != vnetRef {
		t.Fatalf("Ops = %+v, want exactly one sdn.vnet.delete targeting %s", cs.Ops, vnetRef)
	}
}

type singleFindingProvider struct{ f findings.Finding }

func (s singleFindingProvider) Findings() []findings.Finding { return []findings.Finding{s.f} }
