// SPDX-License-Identifier: Apache-2.0

package docexport_test

// postmortem_test.go covers T-4102's acceptance criteria against
// PostmortemDataOf/PostmortemMarkdown/PostmortemHTML: no structural drift
// between formats (AC1, checked the same way build_test.go and
// posture_test.go already check their own artifacts), honest handling of
// absent sections (table-driven, one case per distinguishable "why nothing
// is here"), and that neither rendered format ever carries secret-shaped
// material — the diff's dropped field VALUES and redact.Scrub over event
// summaries, both described in postmortem.go's own doc comment.

import (
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/incident"
	"github.com/bgovanlu/vnprox/internal/redact"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// secretDiffBefore/secretDiffAfter are shaped like a WireGuard private key
// (43 base64 characters plus a trailing `=`) — exactly what
// redact.valuePatterns' "base64-32-byte-key" rule matches, and exactly the
// shape postmortem.go's doc comment names as the reason diff field values
// are dropped rather than merely scrubbed.
const (
	secretDiffBefore = "aGVsbG8gd29ybGQhISBoZWxsbyB3b3JsZCEhIQ=="
	secretDiffAfter  = "cGFzc3dvcmQxMjM0NTY3ODkwMTIzNDU2Nzg5MDEyMw=="
	annotationSecret = "password=hunter2ShouldNeverAppear"
)

// curatedTimeline exercises every honesty distinction this card asks for in
// one fixture: a finding raised then resolved, a changeset apply, an
// operator annotation carrying a pattern-shaped secret, two sources
// unavailable (capture, flow) versus one source queried-and-empty
// (diagnosis), and a diff entry whose fields include one that must never
// render its value.
func curatedTimeline() *incident.Timeline {
	return &incident.Timeline{
		Incident: incident.Incident{
			ID: "inc-1", Title: "vmbr0 flapped", Status: "closed",
			OpenedBy: "brian@pam", OpenedAt: 1_700_000_100, StartedAt: 1_700_000_000,
			EndedAt: 1_700_003_600, ClosedAt: 1_700_003_700, Retroactive: true,
		},
		Window: incident.Window{From: 1_700_000_000, To: 1_700_003_600},
		Events: []incident.Event{
			{
				ID: "finding:1", At: 1_700_000_100, Source: incident.SourceFinding, Kind: "new",
				Summary: "finding f1 new", FindingID: "f1", Transition: "new", Ref: "physnic:pve1/eno1",
			},
			{
				ID: "finding:2", At: 1_700_000_500, Source: incident.SourceFinding, Kind: "resolved",
				Summary: "finding f1 resolved", FindingID: "f1", Transition: "resolved", Ref: "physnic:pve1/eno1",
			},
			{
				ID: "changeset:1", At: 1_700_000_200, Source: incident.SourceChangeset, Kind: "changeset.apply",
				Summary: "changeset.apply cs-1 (success)", Actor: "brian@pam", ChangesetID: "cs-1",
				Result: "success", Node: "pve1",
			},
			{
				ID: "annotation:a1", At: 1_700_000_300, Source: incident.SourceAnnotation, Kind: "note",
				Summary: "pulled the cable on pve1; console said " + annotationSecret,
				Actor:   "brian@pam", AnnotationID: "a1",
			},
		},
		Sources: []incident.SourceStatus{
			{Source: incident.SourceFinding, Status: incident.StatusOK, Count: 2},
			{Source: incident.SourceChangeset, Status: incident.StatusOK, Count: 1},
			{Source: incident.SourceDiagnosis, Status: incident.StatusOK, Count: 0},
			{Source: incident.SourceCapture, Status: incident.StatusUnavailable, Detail: "packet capture is not configured on this node"},
			{Source: incident.SourceFlow, Status: incident.StatusUnavailable, Detail: "no flow samples are collected on this node"},
			{Source: incident.SourceAnnotation, Status: incident.StatusOK, Count: 1},
		},
		Diff: &change.TopologyDiff{
			From: change.DiffPoint{At: 1_700_000_000},
			To:   change.DiffPoint{At: 1_700_003_600},
			Modified: []topology.EntityDiff{
				{
					Ref: "physnic:pve1/eno1", Kind: "physnic", Node: "pve1", Name: "eno1",
					Change: topology.DiffModified,
					Fields: []topology.FieldChange{
						{Field: "mtu", Before: "1500", After: "9000"},
						{Field: "wireguard-private-key", Before: secretDiffBefore, After: secretDiffAfter},
					},
					Attribution: topology.DiffAttribution{Attributed: true, ChangesetID: "cs-1", Actor: "brian@pam", At: 1_700_000_200},
				},
			},
			Coverage: change.DiffCoverage{
				Paths:        []string{"/etc/network/interfaces"},
				OmittedPaths: []string{"SDN entities"},
			},
		},
		Caveats: []string{"the point-in-time diff compared /etc/network/interfaces only"},
	}
}

func curatedData() docexport.PostmortemData {
	return docexport.PostmortemDataOf(curatedTimeline(), 1_700_004_000)
}

var postmortemHeadings = []string{
	docexport.HeadingPostmortemSummary,
	docexport.HeadingPostmortemAffected,
	docexport.HeadingPostmortemTimeline,
	docexport.HeadingPostmortemSources,
	docexport.HeadingPostmortemFindings,
	docexport.HeadingPostmortemDiff,
	docexport.HeadingPostmortemCaveats,
}

// TestPostmortemExport_NoStructuralDrift is AC1: both formats name every
// section identically, the same check build_test.go/posture_test.go already
// run for their own artifacts.
func TestPostmortemExport_NoStructuralDrift(t *testing.T) {
	d := curatedData()
	md := docexport.PostmortemMarkdown(d)
	htmlDoc := docexport.PostmortemHTML(d)

	for _, h := range postmortemHeadings {
		if !strings.Contains(md, "## "+h) {
			t.Errorf("markdown missing section heading %q", h)
		}
		if !strings.Contains(htmlDoc, ">"+h+"<") {
			t.Errorf("html missing section heading %q", h)
		}
	}
	// Facts present in one format must be present in the other.
	for _, must := range []string{d.IncidentID, "cs-1", "f1", "eno1", "pve1"} {
		if !strings.Contains(md, must) {
			t.Errorf("markdown missing %q", must)
		}
		if !strings.Contains(htmlDoc, must) {
			t.Errorf("html missing %q", must)
		}
	}
}

// TestPostmortemHTML_IsSelfContained mirrors this package's existing
// CSP-style check (compliance/posture/config-doc all run it): no external
// resource reference, no <script>, no <link>.
func TestPostmortemHTML_IsSelfContained(t *testing.T) {
	out := docexport.PostmortemHTML(curatedData())
	for _, forbidden := range []string{"<script", "<link", "src=\"http", "href=\"http", "@import"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("postmortem HTML contains %q; it must be standalone", forbidden)
		}
	}
}

// TestPostmortemData_AffectedCollectsNodesAndRefs checks "what was
// affected" is read off the events and the diff, deduplicated and sorted —
// not re-derived from any source the Timeline did not already carry.
func TestPostmortemData_AffectedCollectsNodesAndRefs(t *testing.T) {
	d := curatedData()
	wantNodes := []string{"pve1"}
	if !equalStrings(d.Affected.Nodes, wantNodes) {
		t.Errorf("Affected.Nodes = %v, want %v", d.Affected.Nodes, wantNodes)
	}
	wantRefs := []string{"eno1", "physnic:pve1/eno1"}
	if !equalStrings(d.Affected.Refs, wantRefs) {
		t.Errorf("Affected.Refs = %v, want %v", d.Affected.Refs, wantRefs)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestPostmortemData_DiffEntryDropsFieldValues is the diff half of the
// redaction contract: PostmortemDiffEntry carries field NAMES only. This is
// asserted at the Data layer (not just "the rendered text doesn't contain
// the secret") so a future renderer added to this package inherits the
// property structurally rather than by remembering to scrub.
func TestPostmortemData_DiffEntryDropsFieldValues(t *testing.T) {
	d := curatedData()
	if len(d.Diff.Entries) != 1 {
		t.Fatalf("Diff.Entries = %d, want 1", len(d.Diff.Entries))
	}
	entry := d.Diff.Entries[0]
	wantFields := []string{"mtu", "wireguard-private-key"}
	if !equalStrings(entry.Fields, wantFields) {
		t.Errorf("Diff.Entries[0].Fields = %v, want %v (names only)", entry.Fields, wantFields)
	}
}

// TestPostmortemData_ScrubsEventSummaries is the annotation half of the
// redaction contract: an operator's free-text note passes through
// redact.Scrub before it ever reaches a renderer.
func TestPostmortemData_ScrubsEventSummaries(t *testing.T) {
	d := curatedData()
	var annotationSummary string
	for _, e := range d.Events {
		if e.Source == string(incident.SourceAnnotation) {
			annotationSummary = e.Summary
		}
	}
	if annotationSummary == "" {
		t.Fatal("no annotation event in the projected Data")
	}
	if strings.Contains(annotationSummary, "hunter2ShouldNeverAppear") {
		t.Errorf("annotation summary still carries the raw secret: %q", annotationSummary)
	}
	if !strings.Contains(annotationSummary, redact.Placeholder) {
		t.Errorf("annotation summary was not marked as redacted: %q", annotationSummary)
	}
}

// TestPostmortemExport_ContainsNoSecretMaterial renders both formats and
// scans the actual bytes a person would read, mirroring the shape
// internal/backup's TestBundle_AC1_ContainsNoSecretClass uses: a control
// that must survive (so the scan proves something), and a secret that must
// not.
func TestPostmortemExport_ContainsNoSecretMaterial(t *testing.T) {
	d := curatedData()
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"markdown", docexport.PostmortemMarkdown(d)},
		{"html", docexport.PostmortemHTML(d)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Control: ordinary, non-secret content the render must still
			// carry — otherwise "no secret found" would be trivially true
			// because nothing was rendered at all.
			if !strings.Contains(tc.out, "pulled the cable") {
				t.Fatalf("CONTROL FAILED: an ordinary phrase from the annotation is missing from the %s "+
					"export; the secret-absence assertions below prove nothing", tc.name)
			}
			if !strings.Contains(tc.out, "mtu") {
				t.Fatalf("CONTROL FAILED: the non-secret changed field name is missing from the %s export", tc.name)
			}

			// The scrubbed annotation secret.
			if strings.Contains(tc.out, "hunter2ShouldNeverAppear") {
				t.Errorf("%s export contains the raw annotation secret in the clear", tc.name)
			}
			// The dropped diff field values.
			if strings.Contains(tc.out, secretDiffBefore) || strings.Contains(tc.out, secretDiffAfter) {
				t.Errorf("%s export contains a diff field VALUE; only field names may ever appear", tc.name)
			}
			// The field NAME that changed must still be there — dropping the
			// value must not have dropped the fact that it changed.
			if !strings.Contains(tc.out, "wireguard-private-key") {
				t.Errorf("%s export dropped the changed field's NAME along with its value", tc.name)
			}
		})
	}
}

// TestPostmortemExport_AbsentSectionsAreDistinguished is the table-driven
// core of the honesty requirement: an incident with no captures, or with
// flow ingestion disabled, must say which — never render an empty heading,
// and never collapse two different reasons for "nothing here" into the same
// sentence.
func TestPostmortemExport_AbsentSectionsAreDistinguished(t *testing.T) {
	cases := []struct {
		name        string
		source      incident.Source
		status      string
		detail      string
		wantContain string
		wantAbsent  string
		count       int
	}{
		{
			name:   "queried and empty reads as 'no events', not 'unavailable'",
			source: incident.SourceDiagnosis, status: incident.StatusOK, count: 0,
			wantContain: "no diagnosis events were recorded in this window",
			wantAbsent:  "unavailable",
		},
		{
			name:   "capture not wired reads as 'unavailable', names the node fact",
			source: incident.SourceCapture, status: incident.StatusUnavailable,
			detail:      "packet capture is not configured on this node",
			wantContain: "unavailable on this node: packet capture is not configured on this node",
			wantAbsent:  "no capture events were recorded",
		},
		{
			name:   "flow ingestion disabled reads as 'unavailable', not silence",
			source: incident.SourceFlow, status: incident.StatusUnavailable,
			detail:      "no flow samples are collected on this node",
			wantContain: "unavailable on this node: no flow samples are collected on this node",
			wantAbsent:  "no flow events were recorded",
		},
		{
			name:   "a failed source says so and is distinguishable from empty",
			source: incident.SourceFinding, status: incident.StatusError,
			detail:      "listing finding transitions failed: boom",
			wantContain: "query failed, events may be incomplete: listing finding transitions failed: boom",
			wantAbsent:  "no finding events were recorded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl := &incident.Timeline{
				Incident: incident.Incident{ID: "inc-2", Title: "quiet incident"},
				Window:   incident.Window{From: 1000, To: 2000},
				Sources: []incident.SourceStatus{
					{Source: tc.source, Status: tc.status, Count: tc.count, Detail: tc.detail},
				},
			}
			d := docexport.PostmortemDataOf(tl, 3000)
			md := docexport.PostmortemMarkdown(d)
			htmlDoc := docexport.PostmortemHTML(d)

			for _, out := range []struct {
				name string
				text string
			}{{"markdown", md}, {"html", htmlDoc}} {
				if !strings.Contains(out.text, tc.wantContain) {
					t.Errorf("%s: %s missing %q", tc.name, out.name, tc.wantContain)
				}
				if strings.Contains(out.text, tc.wantAbsent) {
					t.Errorf("%s: %s wrongly contains %q", tc.name, out.name, tc.wantAbsent)
				}
			}
		})
	}
}

// TestPostmortemExport_FindingsSectionDistinguishesEmptyFromUnavailable is
// the findings-specific case: the findings section is often read on its own,
// so it restates the same distinction rather than pointing back at the
// sources table.
func TestPostmortemExport_FindingsSectionDistinguishesEmptyFromUnavailable(t *testing.T) {
	cases := []struct {
		name        string
		status      string
		detail      string
		wantContain string
	}{
		{
			name:   "finding source ok, nothing raised",
			status: incident.StatusOK, wantContain: "no findings were raised in this window",
		},
		{
			name:   "finding source unavailable on this node",
			status: incident.StatusUnavailable, detail: "no finding-event history is recorded on this node",
			wantContain: "finding history is not available on this node: no finding-event history is recorded on this node",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl := &incident.Timeline{
				Incident: incident.Incident{ID: "inc-3", Title: "no findings"},
				Window:   incident.Window{From: 1000, To: 2000},
				Sources: []incident.SourceStatus{
					{Source: incident.SourceFinding, Status: tc.status, Detail: tc.detail},
				},
			}
			d := docexport.PostmortemDataOf(tl, 3000)
			md := docexport.PostmortemMarkdown(d)
			if !strings.Contains(md, tc.wantContain) {
				t.Errorf("markdown missing %q\n---\n%s", tc.wantContain, md)
			}
		})
	}
}

// TestPostmortemExport_DiffAbsentVsEmptyAreDistinctFacts: "no configuration
// differences were found" and "no diff could be computed for this window"
// must never render the same way.
func TestPostmortemExport_DiffAbsentVsEmptyAreDistinctFacts(t *testing.T) {
	t.Run("diff could not be computed", func(t *testing.T) {
		tl := &incident.Timeline{
			Incident:      incident.Incident{ID: "inc-4", Title: "no diff"},
			Window:        incident.Window{From: 1000, To: 2000},
			DiffError:     "no snapshot exists before 09:00; nearest is 09:14",
			DiffErrorCode: "no_snapshot_in_range",
		}
		d := docexport.PostmortemDataOf(tl, 3000)
		if d.Diff.Present {
			t.Fatal("Diff.Present is true for a timeline with a nil Diff")
		}
		md := docexport.PostmortemMarkdown(d)
		if !strings.Contains(md, "no snapshot exists before 09:00") {
			t.Error("markdown does not surface the diff engine's own refusal message")
		}
		if !strings.Contains(md, "no_snapshot_in_range") {
			t.Error("markdown does not surface the stable diff error code")
		}
	})

	t.Run("diff computed with no differences", func(t *testing.T) {
		tl := &incident.Timeline{
			Incident: incident.Incident{ID: "inc-5", Title: "quiet window"},
			Window:   incident.Window{From: 1000, To: 2000},
			Diff: &change.TopologyDiff{
				From: change.DiffPoint{At: 1000}, To: change.DiffPoint{At: 2000},
				Coverage: change.DiffCoverage{Paths: []string{"/etc/network/interfaces"}},
			},
		}
		d := docexport.PostmortemDataOf(tl, 3000)
		if !d.Diff.Present {
			t.Fatal("Diff.Present is false for a timeline with a non-nil Diff")
		}
		if len(d.Diff.Entries) != 0 {
			t.Fatalf("Diff.Entries = %d, want 0", len(d.Diff.Entries))
		}
		md := docexport.PostmortemMarkdown(d)
		if strings.Contains(md, "no snapshot exists") {
			t.Error("a computed empty diff must not read like a refusal")
		}
		if !strings.Contains(md, "Added: 0, Removed: 0, Modified: 0") {
			t.Error("markdown does not state the zero counts explicitly")
		}
	})
}

// TestPostmortemDataOf_GeneratedAtIsTheCallersClock confirms Data gathering
// takes the render instant as an argument rather than reading time.Now
// itself, keeping PostmortemDataOf a pure, table-testable projection.
func TestPostmortemDataOf_GeneratedAtIsTheCallersClock(t *testing.T) {
	tl := &incident.Timeline{Incident: incident.Incident{ID: "inc-6"}, Window: incident.Window{From: 1, To: 2}}
	fixed := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC).Unix()
	d := docexport.PostmortemDataOf(tl, fixed)
	if d.GeneratedAt != fixed {
		t.Errorf("GeneratedAt = %d, want %d", d.GeneratedAt, fixed)
	}
}
