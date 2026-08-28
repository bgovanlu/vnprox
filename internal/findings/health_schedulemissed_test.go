// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// fakeScheduleProvider is a minimal findings.ScheduleMissedProvider stand-in
// — the actual "is this schedule missed" decision is change.Service's job
// (covered by internal/change's own T-1103 tests), so here we only need to
// prove the check correctly turns a pre-computed missed-schedule list into
// findings.
type fakeScheduleProvider struct {
	missed []change.MissedSchedule
}

func (f fakeScheduleProvider) MissedSchedules() []change.MissedSchedule { return f.missed }

func TestScheduleMissed_NilProvider_NoFindings(t *testing.T) {
	engine := findings.New(findings.Config{})
	for _, f := range engine.Findings() {
		if f.Check == findings.CheckScheduleMissed {
			t.Fatalf("nil ScheduleMissedProvider produced a finding: %+v", f)
		}
	}
}

func TestScheduleMissed_EmptyList_NoFindings(t *testing.T) {
	engine := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Schedule: fakeScheduleProvider{}})
	for _, f := range engine.Findings() {
		if f.Check == findings.CheckScheduleMissed {
			t.Fatalf("empty MissedSchedules() produced a finding: %+v", f)
		}
	}
}

// TestScheduleMissed_OneEntry_OneFinding is T-1103 AC4's findings-side half:
// a missed schedule surfaces exactly one warning finding naming the
// changeset, with a stable id and a docs link (detection-only, no fix).
func TestScheduleMissed_OneEntry_OneFinding(t *testing.T) {
	engine := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Schedule: fakeScheduleProvider{
		missed: []change.MissedSchedule{{ChangesetID: "cs-123", WindowStart: 1000, WindowEnd: 1060}},
	}})

	var got *findings.Finding
	for _, f := range engine.Findings() {
		if f.Check == findings.CheckScheduleMissed {
			f := f
			got = &f
		}
	}
	if got == nil {
		t.Fatal("expected exactly one schedule_missed finding, got none")
	}
	if got.Severity != findings.SeverityWarning {
		t.Errorf("severity = %s, want warning", got.Severity)
	}
	if got.Fixable {
		t.Error("Fixable = true, want false (detection-only)")
	}
	if got.DocsLink == "" {
		t.Error("DocsLink is empty, want a remediation link")
	}
	if len(got.Refs) != 1 || got.Refs[0] != "cs-123" {
		t.Errorf("Refs = %+v, want [cs-123]", got.Refs)
	}

	// Re-running Findings() against the same (unchanged) provider state
	// yields the exact same id — no flapping.
	var second string
	for _, f := range engine.Findings() {
		if f.Check == findings.CheckScheduleMissed {
			second = f.ID
		}
	}
	if second != got.ID {
		t.Errorf("id changed across repeated cycles: %s vs %s", got.ID, second)
	}
}

func TestScheduleMissed_ClearsWhenNoLongerMissed(t *testing.T) {
	provider := &mutableScheduleProvider{missed: []change.MissedSchedule{{ChangesetID: "cs-1", WindowStart: 1, WindowEnd: 2}}}
	engine := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Schedule: provider})

	found := false
	for _, f := range engine.Findings() {
		if f.Check == findings.CheckScheduleMissed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a schedule_missed finding while the provider reports one")
	}

	provider.missed = nil
	for _, f := range engine.Findings() {
		if f.Check == findings.CheckScheduleMissed {
			t.Fatalf("finding did not clear once the provider reported no missed schedules: %+v", f)
		}
	}
}

type mutableScheduleProvider struct{ missed []change.MissedSchedule }

func (p *mutableScheduleProvider) MissedSchedules() []change.MissedSchedule { return p.missed }
