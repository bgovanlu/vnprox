// SPDX-License-Identifier: Apache-2.0

package runbook_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/runbook"
	"github.com/bgovanlu/vnprox/internal/store"
)

// newRealChangeService builds a genuine *change.Service (no pvemock/HTTP
// needed — Validate only ever consults graph, an in-process
// inventory.Graph) backed by a fresh temp-file store, mirroring
// internal/change's own service_test.go newTestService helper. Used so
// this package's Service.Prepare tests exercise the actual validator
// pipeline (T-4003 acceptance criterion 1's "produces a status: draft
// changeset ... never status: applied or confirmed"), not a stand-in.
func newRealChangeService(t *testing.T, graph *inventory.Graph) *change.Service {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Inventory:  graph,
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

// fakeFindingsProvider is a minimal FindingsProvider for tests that don't
// need a real findings.Engine.
type fakeFindingsProvider []findings.Finding

func (f fakeFindingsProvider) Findings() []findings.Finding { return f }

// fakeFwAnalytics is a minimal findings.FwAnalyticsProvider returning a
// fixed Analytics value regardless of the requested window.
type fakeFwAnalytics struct {
	analytics fwlog.Analytics
}

func (f fakeFwAnalytics) Analytics(time.Time, time.Duration, int) fwlog.Analytics { return f.analytics }

func TestService_Prepare_HappyPath_DeleteOrphanVnet(t *testing.T) {
	g := newGraph()
	vnetRef := addSdnVnet(g, "vnetx", "goneZone")
	f := findings.Finding{Check: "orphan_vnet", ID: "health:orphan_vnet|" + vnetRef.String(), Refs: []string{vnetRef.String()}}

	svc := runbook.New(runbook.Config{
		Changes:   newRealChangeService(t, g),
		Findings:  fakeFindingsProvider{f},
		Inventory: g,
	})

	cs, err := svc.Prepare(context.Background(), "alice@pam", f.ID, runbook.DeleteOrphanVnet)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if cs.Status != change.StatusValidated {
		t.Errorf("Status = %s, want %s (a clean sdn.vnet.delete against a graph with no other issues should validate)", cs.Status, change.StatusValidated)
	}
	if len(cs.Ops) != 1 || cs.Ops[0].Type != change.OpSdnVnetDelete {
		t.Fatalf("Ops = %+v, want exactly one sdn.vnet.delete", cs.Ops)
	}
	if cs.Author != "alice@pam" {
		t.Errorf("Author = %q, want alice@pam", cs.Author)
	}
	assertNeverApplied(t, cs)
}

// TestService_Prepare_TOCTOU_FailsAtValidate_NotApply is T-4003's own
// required test: "a runbook whose template produces an invalid changeset
// must fail at validate, not at apply." The scenario is a realistic
// time-of-check/time-of-use gap the read-check cannot close by itself: the
// firewall-log analytics read-check (rule has recorded no hits) still says
// "still unused" — nothing about traffic hit history changed — but the
// rule was deleted from the live ruleset by a concurrent edit between the
// finding firing and Prepare running. Render has no way to see that (it
// only re-verifies against analytics), so it proposes fw.rule.delete at a
// position that no longer exists; the ordinary validator
// (validate_referential.go's checkFwPos) is what actually catches it.
func TestService_Prepare_TOCTOU_FailsAtValidate_NotApply(t *testing.T) {
	g := newGraph()
	guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: "pve1", Kinds: []inventory.Kind{inventory.KindGuest}},
		[]inventory.Entity{&inventory.Guest{Ref: guestRef, Name: "g100", Type: "qemu", Node: "pve1", Status: "running", VMID: 100}})
	// Deliberately no FwRuleset polled at all for this guest -- standing in
	// for "the rule (and the whole ruleset) is gone by the time Prepare
	// runs", the sharpest version of the race.

	findingID := "health:fw_rule_unused|" + guestRef.String() + "|guest|0"
	f := findings.Finding{Check: "fw_rule_unused", ID: findingID, Refs: []string{guestRef.String()}}

	svc := runbook.New(runbook.Config{
		Changes:   newRealChangeService(t, g),
		Findings:  fakeFindingsProvider{f},
		Inventory: g,
		// Stale analytics: still reports the rule as unused, exactly as it
		// would have when the finding first fired.
		FwAnalytics: fakeFwAnalytics{analytics: fwlog.Analytics{UnusedRules: []fwlog.UnusedRule{
			{Rule: fwlog.RuleRef{GuestRef: guestRef.String(), Origin: "guest", Pos: 0}},
		}}},
	})

	cs, err := svc.Prepare(context.Background(), "alice@pam", f.ID, runbook.DeleteUnusedFwRule)
	if err != nil {
		t.Fatalf("Prepare: %v (want a staged-but-invalid changeset, not a Go error)", err)
	}
	if cs.Status != change.StatusDraft {
		t.Errorf("Status = %s, want %s (validate should have failed and left it draft)", cs.Status, change.StatusDraft)
	}
	foundError := false
	for _, finding := range cs.Findings {
		if finding.Severity == change.SeverityError {
			foundError = true
		}
	}
	if !foundError {
		t.Errorf("Findings = %+v, want at least one error-severity finding", cs.Findings)
	}
	assertNeverApplied(t, cs)
}

func TestService_Prepare_FindingNotFound(t *testing.T) {
	svc := runbook.New(runbook.Config{
		Changes:   newRealChangeService(t, newGraph()),
		Findings:  fakeFindingsProvider(nil),
		Inventory: newGraph(),
	})
	_, err := svc.Prepare(context.Background(), "alice@pam", "no-such-finding", runbook.DeleteOrphanVnet)
	if !errors.Is(err, runbook.ErrFindingNotFound) {
		t.Fatalf("err = %v, want ErrFindingNotFound", err)
	}
}

func TestService_Prepare_RunbookNotFound(t *testing.T) {
	f := findings.Finding{Check: "orphan_vnet", ID: "f1"}
	svc := runbook.New(runbook.Config{
		Changes:   newRealChangeService(t, newGraph()),
		Findings:  fakeFindingsProvider{f},
		Inventory: newGraph(),
	})
	_, err := svc.Prepare(context.Background(), "alice@pam", "f1", "no-such-runbook")
	if !errors.Is(err, runbook.ErrRunbookNotFound) {
		t.Fatalf("err = %v, want ErrRunbookNotFound", err)
	}
}

func TestService_Prepare_NotAttached(t *testing.T) {
	f := findings.Finding{Check: "trunk_unused_vlans", ID: "f1"}
	svc := runbook.New(runbook.Config{
		Changes:   newRealChangeService(t, newGraph()),
		Findings:  fakeFindingsProvider{f},
		Inventory: newGraph(),
	})
	// DeleteOrphanVnet is attached to orphan_vnet, not trunk_unused_vlans.
	_, err := svc.Prepare(context.Background(), "alice@pam", "f1", runbook.DeleteOrphanVnet)
	if !errors.Is(err, runbook.ErrNotAttached) {
		t.Fatalf("err = %v, want ErrNotAttached", err)
	}
}

// assertNeverApplied is the structural half of T-4003 acceptance criterion
// 1: whatever Prepare returns, its Status is only ever draft or validated
// — never applying, awaiting_confirm, or committed. This package has no
// method that could have moved a changeset further (proven separately, at
// compile time, by stageonly.go and reflectively by surface_test.go); this
// is the runtime corroboration on every actual Prepare call this test file
// makes.
func assertNeverApplied(t *testing.T, cs change.Changeset) {
	t.Helper()
	if cs.Status != change.StatusDraft && cs.Status != change.StatusValidated {
		t.Fatalf("Status = %s, want draft or validated only -- a runbook must never apply", cs.Status)
	}
}
