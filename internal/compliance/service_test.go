// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// --- fakes ------------------------------------------------------------

type fakeFindings struct {
	err  error
	refs []FindingRef
}

func (f fakeFindings) ComplianceFindings(context.Context) ([]FindingRef, error) { return f.refs, f.err }

type fakeHistory struct {
	earliest time.Time
	err      error
	events   []Transition
	hasAny   bool
}

func (h fakeHistory) Transitions(_ context.Context, until time.Time) ([]Transition, error) {
	if h.err != nil {
		return nil, h.err
	}
	var out []Transition
	for _, e := range h.events {
		if e.At <= until.Unix() {
			out = append(out, e)
		}
	}
	return out, nil
}

func (h fakeHistory) Earliest(context.Context) (time.Time, bool, error) {
	return h.earliest, h.hasAny, h.err
}

type fakePosture struct {
	history []posture.Posture
	latest  posture.Posture
	ok      bool
}

func (p fakePosture) Latest(context.Context) (posture.Posture, bool, error) {
	return p.latest, p.ok, nil
}
func (p fakePosture) History(context.Context, int) ([]posture.Posture, error) { return p.history, nil }

type fakePolicy struct{ state PolicyState }

func (p fakePolicy) CompliancePolicy(context.Context) (PolicyState, error) { return p.state, nil }

func testService(t *testing.T) *Service {
	t.Helper()
	return &Service{
		Profiles:       []Profile{testProfile(checkControl("C1", "bond_slave_down"))},
		ProductVersion: "1.2.3-test",
		KnownChecks:    []string{"bond_slave_down", "mgmt_single_path"},
		CheckUniverse:  "test catalog",
		Now:            func() time.Time { return testNow },
	}
}

// --- tests ------------------------------------------------------------

func TestService_UnknownProfileNamesWhatIsInstalled(t *testing.T) {
	svc := testService(t)
	_, err := svc.Report(context.Background(), "nope", time.Time{})
	var unknown *ErrUnknownProfile
	if !errors.As(err, &unknown) {
		t.Fatalf("Report for an unknown profile returned %v, want *ErrUnknownProfile", err)
	}
	if !slices.Contains(unknown.Available, "test-profile") {
		t.Errorf("error does not name the installed profiles: %v", unknown.Available)
	}
}

func TestService_LiveReportUsesTheLiveSurfaces(t *testing.T) {
	svc := testService(t)
	svc.Findings = fakeFindings{refs: []FindingRef{
		{ID: "health:bond_slave_down|b", Check: "bond_slave_down", Severity: "warning"},
	}}
	svc.Policy = fakePolicy{state: PolicyState{Configured: true}}

	rep, err := svc.Report(context.Background(), "test-profile", time.Time{})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if rep.AsOf != 0 {
		t.Errorf("live report carries AsOf %d", rep.AsOf)
	}
	if rep.ProductVersion != "1.2.3-test" {
		t.Errorf("report ProductVersion = %q", rep.ProductVersion)
	}
	if got := rep.Controls[0].Stat; got != StatusFail {
		t.Errorf("C1 = %q, want %q", got, StatusFail)
	}
	if !slices.Contains(rep.UnmappedChecks, "mgmt_single_path") {
		t.Errorf("unmapped checks %v do not list the catalogued but unmapped check", rep.UnmappedChecks)
	}
}

// TestService_AsOfOutsideRetentionRefusesAndNamesTheEarliestDate is AC5.
// A date the evidence does not cover produces a stated error naming the
// earliest available date — never a report assembled from evidence that
// does not exist, which would read as "these controls were in this state on
// that date" when nothing was observed at all.
//
// BREAK IT TO SEE IT FIRE: in historicalFindings, drop the
// `asOf.Before(earliest)` branch and let the replay return an empty set.
func TestService_AsOfOutsideRetentionRefusesAndNamesTheEarliestDate(t *testing.T) {
	earliest := testNow.Add(-7 * 24 * time.Hour)

	tests := []struct {
		name        string
		history     HistorySource
		asOf        time.Time
		wantMsg     []string
		wantEarlies bool
	}{
		{
			name:        "before the earliest retained record",
			history:     fakeHistory{earliest: earliest, hasAny: true},
			asOf:        testNow.Add(-30 * 24 * time.Hour),
			wantMsg:     []string{earliest.UTC().Format(time.RFC3339), "the evidence does not cover"},
			wantEarlies: true,
		},
		{
			name:    "nothing retained at all",
			history: fakeHistory{hasAny: false},
			asOf:    testNow.Add(-time.Hour),
			wantMsg: []string{"no finding history is retained"},
		},
		{
			name:    "no history source wired",
			history: nil,
			asOf:    testNow.Add(-time.Hour),
			wantMsg: []string{"no finding history is retained"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := testService(t)
			svc.History = tc.history
			rep, err := svc.Report(context.Background(), "test-profile", tc.asOf)

			var outside *ErrOutsideRetention
			if !errors.As(err, &outside) {
				t.Fatalf("Report returned (%+v, %v), want *ErrOutsideRetention", rep, err)
			}
			if len(rep.Controls) != 0 {
				t.Errorf("a refused as-of request still produced %d controls; a partial report is the failure mode", len(rep.Controls))
			}
			if outside.HasEarliest != tc.wantEarlies {
				t.Errorf("HasEarliest = %v, want %v", outside.HasEarliest, tc.wantEarlies)
			}
			for _, want := range tc.wantMsg {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if !strings.Contains(err.Error(), tc.asOf.UTC().Format(time.RFC3339)) {
				t.Errorf("error %q does not name the requested date", err)
			}
		})
	}
}

func TestService_AsOfInTheFutureIsRefused(t *testing.T) {
	svc := testService(t)
	svc.History = fakeHistory{earliest: testNow.Add(-time.Hour), hasAny: true}
	_, err := svc.Report(context.Background(), "test-profile", testNow.Add(time.Hour))
	var future *ErrFutureAsOf
	if !errors.As(err, &future) {
		t.Fatalf("Report for a future date returned %v, want *ErrFutureAsOf", err)
	}
}

func TestService_AsOfWithinRetentionReconstructsFromTransitions(t *testing.T) {
	earliest := testNow.Add(-7 * 24 * time.Hour)
	svc := testService(t)
	svc.Profiles = []Profile{testProfile(checkControl("C1", "bond_slave_down"), checkControl("C2", "mgmt_single_path"))}
	svc.History = fakeHistory{
		earliest: earliest, hasAny: true,
		events: []Transition{
			// Open before the target instant, never resolved.
			{FindingID: "health:bond_slave_down|bond0", At: earliest.Unix() + 60, Transition: TransitionNew},
			// Opened and resolved before the target instant.
			{FindingID: "health:mgmt_single_path|n1", At: earliest.Unix() + 60, Transition: TransitionNew},
			{FindingID: "health:mgmt_single_path|n1", At: earliest.Unix() + 120, Transition: TransitionResolved},
			// After the target instant: must not be visible.
			{FindingID: "health:mgmt_single_path|n2", At: testNow.Unix() - 10, Transition: TransitionNew},
		},
	}
	asOf := testNow.Add(-24 * time.Hour)

	rep, err := svc.Report(context.Background(), "test-profile", asOf)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if rep.AsOf != asOf.Unix() {
		t.Errorf("report AsOf = %d, want %d", rep.AsOf, asOf.Unix())
	}
	if got := controlByID(t, rep, "C1").Stat; got != StatusFail {
		t.Errorf("C1 = %q, want %q: its finding was open at that instant", got, StatusFail)
	}
	if got := controlByID(t, rep, "C2").Stat; got != StatusPass {
		t.Errorf("C2 = %q, want %q: its finding was resolved before that instant, and the later one had not happened yet", got, StatusPass)
	}
}

func TestReplayOpen(t *testing.T) {
	tests := []struct {
		name   string
		events []Transition
		want   []FindingRef
	}{
		{name: "empty", want: []FindingRef{}},
		{
			name:   "new then resolved is closed",
			events: []Transition{{FindingID: "health:a|1", At: 1, Transition: TransitionNew}, {FindingID: "health:a|1", At: 2, Transition: TransitionResolved}},
			want:   []FindingRef{},
		},
		{
			name:   "resolved then new again is open",
			events: []Transition{{FindingID: "health:a|1", At: 1, Transition: TransitionNew}, {FindingID: "health:a|1", At: 2, Transition: TransitionResolved}, {FindingID: "health:a|1", At: 3, Transition: TransitionNew}},
			want:   []FindingRef{{ID: "health:a|1", Check: "a"}},
		},
		{
			name:   "escalation keeps it open",
			events: []Transition{{FindingID: "health:a|1", At: 1, Transition: TransitionNew}, {FindingID: "health:a|1", At: 2, Transition: TransitionEscalated}},
			want:   []FindingRef{{ID: "health:a|1", Check: "a"}},
		},
		{
			// An unknown transition kind must not silently clear a finding.
			name:   "an unknown transition leaves the last known state",
			events: []Transition{{FindingID: "health:a|1", At: 1, Transition: TransitionNew}, {FindingID: "health:a|1", At: 2, Transition: "shrugged"}},
			want:   []FindingRef{{ID: "health:a|1", Check: "a"}},
		},
		{
			name:   "an id carrying no check yields an empty check name rather than a misattribution",
			events: []Transition{{FindingID: "opaque-id", At: 1, Transition: TransitionNew}},
			want:   []FindingRef{{ID: "opaque-id", Check: ""}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ReplayOpen(tc.events)
			if len(got) != len(tc.want) {
				t.Fatalf("ReplayOpen = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ReplayOpen[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestCheckFromFindingID(t *testing.T) {
	tests := map[string]string{
		"health:mgmt_single_path|node:pve1":             "mgmt_single_path",
		"drift:mtu_consistency|a,b":                     "mtu_consistency",
		"lldp:vlan_cross_check_missing_on_switch|br|nb": "vlan_cross_check_missing_on_switch",
		"wan:wan_degraded|link1":                        "wan_degraded",
		"no-separator":                                  "",
		"":                                              "",
	}
	for id, want := range tests {
		if got := CheckFromFindingID(id); got != want {
			t.Errorf("CheckFromFindingID(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestService_PostureAtPicksTheNewestScoreAtOrBeforeTheDate(t *testing.T) {
	svc := testService(t)
	svc.Profiles = []Profile{testProfile(Control{ID: "C1", Title: "t", Statement: "s",
		Evidence: []Evidence{{Kind: EvidencePosture, Factor: posture.FactorSegmentation, MinScore: 50}}})}
	svc.History = fakeHistory{earliest: testNow.Add(-7 * 24 * time.Hour), hasAny: true}
	svc.Posture = fakePosture{history: []posture.Posture{
		// Newest first, as the store returns them.
		{ComputedAt: testNow.Unix(), Factors: []posture.Factor{{Name: posture.FactorSegmentation, ScorePct: 90, Evaluated: true}}},
		{ComputedAt: testNow.Add(-48 * time.Hour).Unix(), Factors: []posture.Factor{{Name: posture.FactorSegmentation, ScorePct: 10, Evaluated: true}}},
		{ComputedAt: testNow.Add(-96 * time.Hour).Unix(), Factors: []posture.Factor{{Name: posture.FactorSegmentation, ScorePct: 99, Evaluated: true}}},
	}}

	rep, err := svc.Report(context.Background(), "test-profile", testNow.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if got := controlByID(t, rep, "C1").Stat; got != StatusFail {
		t.Errorf("C1 = %q, want %q: the score in force at that date was 10/100, not today's 90", got, StatusFail)
	}
}

func TestService_ListProfiles(t *testing.T) {
	svc := testService(t)
	got := svc.ListProfiles()
	if len(got) != 1 {
		t.Fatalf("ListProfiles returned %d entries", len(got))
	}
	if got[0].ID != "test-profile" || got[0].ControlCount != 1 || got[0].Notice == "" {
		t.Errorf("ListProfiles()[0] = %+v", got[0])
	}
}

// TestService_DegradedDependenciesStillProduceAReport asserts a daemon with
// no posture job, no policy store and no findings source produces a report
// whose controls are not evaluated — never one whose controls pass by
// default because nothing contradicted them.
func TestService_DegradedDependenciesStillProduceAReport(t *testing.T) {
	svc := testService(t)
	svc.Profiles = []Profile{testProfile(
		Control{ID: "C1", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePosture, Factor: posture.FactorSPOF, MinScore: 1}}},
		Control{ID: "C2", Title: "t", Statement: "s", Evidence: []Evidence{{Kind: EvidencePolicy, Tag: "change-control"}}},
	)}
	rep, err := svc.Report(context.Background(), "test-profile", time.Time{})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	for _, c := range rep.Controls {
		if c.Stat.IsPassing() {
			t.Errorf("control %s passed on a daemon with no posture score and no policy store", c.ID)
		}
	}
	if len(rep.Caveats) == 0 {
		t.Error("a report built from absent surfaces carries no caveats")
	}
}
