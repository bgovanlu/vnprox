package findings

import (
	"strings"
	"testing"
)

type stubGitSync struct{ issues []GitSyncIssue }

func (s stubGitSync) GitSyncIssues() []GitSyncIssue { return s.issues }

// TestGitSyncFindings covers the adapter in both directions: a nil provider
// (every deployment that has not configured [gitsync]) contributes nothing
// at all, and a provider with issues renders them into the unified stream
// with stable ids and no fix path.
func TestGitSyncFindings(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name         string
		prov         GitSyncProvider
		wantIDs      []string
		wantSeverity map[string]string
	}{
		{name: "nil provider contributes nothing", prov: nil},
		{name: "empty provider contributes nothing", prov: stubGitSync{}},
		{
			name: "issues render as gitsync-sourced findings",
			prov: stubGitSync{issues: []GitSyncIssue{
				{Check: "gitsync_unreachable", Severity: SeverityWarning, Detail: "could not read cluster.yaml"},
				{Check: "gitsync_divergence", Severity: SeverityInfo, Detail: "draft cs-a is open for review"},
			}},
			wantIDs:      []string{"gitsync:gitsync_divergence", "gitsync:gitsync_unreachable"},
			wantSeverity: map[string]string{"gitsync_unreachable": SeverityWarning, "gitsync_divergence": SeverityInfo},
		},
		{
			name:         "an unrecognised severity becomes a warning, never silently the lowest rank",
			prov:         stubGitSync{issues: []GitSyncIssue{{Check: "gitsync_unreachable", Severity: "catastrophic", Detail: "x"}}},
			wantIDs:      []string{"gitsync:gitsync_unreachable"},
			wantSeverity: map[string]string{"gitsync_unreachable": SeverityWarning},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gitSyncFindings(tc.prov)
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("got %d finding(s), want %d: %+v", len(got), len(tc.wantIDs), got)
			}
			for i, f := range got {
				if f.ID != tc.wantIDs[i] {
					t.Errorf("finding[%d].ID = %q, want %q", i, f.ID, tc.wantIDs[i])
				}
				if f.Source != SourceGitSync {
					t.Errorf("finding[%d].Source = %q, want %q", i, f.Source, SourceGitSync)
				}
				if f.Fixable {
					t.Errorf("finding %q is fixable; every gitsync finding is detection-only — the action is the draft changeset", f.ID)
				}
				if !strings.HasPrefix(f.DocsLink, "docs/") {
					t.Errorf("finding %q has no docs link", f.ID)
				}
				if want := tc.wantSeverity[f.Check]; f.Severity != want {
					t.Errorf("finding %q severity = %q, want %q", f.ID, f.Severity, want)
				}
			}
		})
	}
}

// TestEngineIncludesGitSyncFindings proves the producer is actually wired
// into Engine.Findings rather than only existing — the failure mode a
// unit test of the adapter alone cannot catch.
func TestEngineIncludesGitSyncFindings(t *testing.T) {
	e := New(Config{GitSync: stubGitSync{issues: []GitSyncIssue{
		{Check: "gitsync_spec_unparseable", Severity: SeverityError, Detail: "network/cluster.yaml does not parse"},
	}}})
	var found bool
	for _, f := range e.Findings() {
		if f.Source == SourceGitSync {
			found = true
		}
	}
	if !found {
		t.Fatal("Engine.Findings did not include the gitsync producer's findings")
	}

	// The control: with no provider, the engine emits none of them.
	for _, f := range New(Config{}).Findings() {
		if f.Source == SourceGitSync {
			t.Fatalf("an unconfigured engine emitted a gitsync finding: %+v", f)
		}
	}
}
