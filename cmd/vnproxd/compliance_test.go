// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/compliance"
	"github.com/bgovanlu/vnprox/internal/store"
)

// compliance_test.go covers the composition-root adapters T-2706 adds. They
// are projections, so what is worth asserting is exactly what a projection
// can lose: T-2601's probablyMisconfigured flag (which must reach the
// evaluator, or an unexercised rule would read as evidence), and the
// history's ordering/floor.

func TestComplianceHistoryAdapter_ProjectsTransitionsAndFloor(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewFindingEventRepo(db)

	for _, e := range []store.FindingEvent{
		{FindingID: "health:bond_slave_down|b", At: 1000, Transition: "new"},
		{FindingID: "health:bond_slave_down|b", At: 4000, Transition: "resolved"},
		{FindingID: "health:mgmt_single_path|n", At: 2000, Transition: "new"},
	} {
		if insertErr := repo.Insert(ctx, e); insertErr != nil {
			t.Fatalf("Insert: %v", insertErr)
		}
	}
	adapter := complianceHistoryAdapter{repo: repo}

	earliest, ok, err := adapter.Earliest(ctx)
	if err != nil || !ok {
		t.Fatalf("Earliest = (%v, %v, %v)", earliest, ok, err)
	}
	if earliest.Unix() != 1000 {
		t.Errorf("Earliest = %d, want 1000", earliest.Unix())
	}

	// A replay bounded to t=3000 must see the bond finding still open (its
	// resolution is later) and the mgmt one open too.
	events, err := adapter.Transitions(ctx, time.Unix(3000, 0))
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	open := compliance.ReplayOpen(events)
	if len(open) != 2 {
		t.Fatalf("ReplayOpen at t=3000 = %+v, want 2 open findings", open)
	}

	// Bounded past the resolution, only the mgmt finding remains.
	events, err = adapter.Transitions(ctx, time.Unix(5000, 0))
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	open = compliance.ReplayOpen(events)
	if len(open) != 1 || open[0].Check != "mgmt_single_path" {
		t.Errorf("ReplayOpen at t=5000 = %+v, want only mgmt_single_path", open)
	}
}

func TestSetupCompliance_LoadsTheShippedProfileAndCatalog(t *testing.T) {
	svc := setupCompliance(nil, nil, nil, nil, nil, "9.9.9-test", testLogger())
	if svc == nil {
		t.Fatal("setupCompliance returned nil for the shipped profile")
	}
	profiles := svc.ListProfiles()
	if len(profiles) != 1 || profiles[0].ID != compliance.GeneralProfileID {
		t.Fatalf("ListProfiles = %+v", profiles)
	}
	if len(svc.KnownChecks) == 0 {
		t.Error("the compliance service was wired with an empty check universe; nothing could ever be reported as unmapped")
	}

	// A daemon with no findings engine, no posture job and no policy store
	// must still produce a report — and none of its controls may pass.
	rep, err := svc.Report(context.Background(), compliance.GeneralProfileID, time.Time{})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if rep.ProductVersion != "9.9.9-test" {
		t.Errorf("report ProductVersion = %q", rep.ProductVersion)
	}
	if rep.Summary.Total != len(rep.Controls) || rep.Summary.Total == 0 {
		t.Errorf("summary = %+v over %d controls", rep.Summary, len(rep.Controls))
	}
	if rep.Summary.Unmapped == 0 {
		t.Error("the shipped profile produced no unmapped control")
	}
	if rep.Notice == "" {
		t.Error("the report carries no no-certification notice")
	}
}

// TestProjectPolicyStatus_CarriesProbablyMisconfigured is the end-to-end
// guard on T-2601's instruction: an installed rule that has never matched an
// op must not reach the evaluator looking like a healthy one, or a control
// evidenced by it would report `pass`.
//
// BREAK IT TO SEE IT FIRE: drop the ProbablyMisconfigured assignment from
// projectPolicyStatus.
func TestProjectPolicyStatus_CarriesProbablyMisconfigured(t *testing.T) {
	status := change.PolicyStatus{
		Revision: 7,
		Set: change.PolicySet{Version: change.PolicyFormatVersion, Rules: []change.PolicyRule{
			{ID: "guarding", Description: "d", Severity: change.PolicyDeny, Tags: []string{"change-control"}},
			{ID: "idle", Description: "d", Severity: change.PolicyDeny, Tags: []string{"change-control"}},
		}},
		Rules: []change.PolicyRuleStatus{
			{RuleID: "guarding", EvalCount: 400, MatchCount: 12, LastMatchedAt: 1_700_000_000},
			{RuleID: "idle", EvalCount: 400, MatchCount: 0, ProbablyMisconfigured: true},
		},
	}

	got := projectPolicyStatus(status)
	if !got.Configured || got.Revision != 7 || len(got.Rules) != 2 {
		t.Fatalf("projectPolicyStatus = %+v", got)
	}
	byID := map[string]compliance.PolicyRuleRef{}
	for _, r := range got.Rules {
		byID[r.ID] = r
	}
	if byID["guarding"].ProbablyMisconfigured {
		t.Error("an actively-matching rule was projected as probably misconfigured")
	}
	if !byID["idle"].ProbablyMisconfigured {
		t.Fatal("an unexercised rule lost its probablyMisconfigured flag in projection")
	}
	if !hasTagInProjection(byID["idle"].Tags, "change-control") {
		t.Error("rule tags were lost in projection; a general profile selects policy evidence by tag")
	}

	// And the consequence: a control evidenced by that tag must NOT pass.
	profile, err := compliance.ParseProfile("test.yaml",
		[]byte("formatVersion: 1\nid: t\ntitle: T\nversion: '1'\nnotice: not a certification\ncontrols:\n"+
			"  - id: C1\n    title: T\n    statement: S\n    evidence:\n      - kind: policy\n        tag: change-control\n"))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	rep := compliance.Evaluate(profile, compliance.Inputs{Now: time.Unix(1_700_000_000, 0), Policy: got})
	if rep.Controls[0].Stat.IsPassing() {
		t.Errorf("a control evidenced only by an unexercised rule reported %q", rep.Controls[0].Stat)
	}
}

func hasTagInProjection(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
