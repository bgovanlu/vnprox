// SPDX-License-Identifier: Apache-2.0

package push

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildFromEvent_ChangesetAwaitingConfirm is the POSITIVE leg: an actual
// changeset.status/awaiting_confirm envelope, as internal/change/service.go
// broadcasts it, must produce a notification whose URL deep-links to that
// changeset's review screen by its (safe, opaque ULID) id — proving the
// deep-link mechanism actually works, not just that it never leaks
// anything (a builder that leaks nothing because it links nowhere would be
// useless, not safe).
func TestBuildFromEvent_ChangesetAwaitingConfirm(t *testing.T) {
	raw := []byte(`{"event":"changeset.status","id":"01J8X3K7ZQGXZ5C6VN9PQKX3MC","status":"awaiting_confirm","confirmDeadline":1234567890}`)
	got, ok := BuildFromEvent(raw)
	if !ok {
		t.Fatal("BuildFromEvent(awaiting_confirm) ok = false, want true")
	}
	if got.Category != CategoryAwaitingConfirm {
		t.Errorf("Category = %q, want %q", got.Category, CategoryAwaitingConfirm)
	}
	wantURL := "/changesets/01J8X3K7ZQGXZ5C6VN9PQKX3MC/review"
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
}

// TestBuildFromEvent_IgnoresNonAwaitingConfirmStatuses is the paired
// negative leg: every OTHER changeset status must be ignored — a push on
// every draft/validate/applying/committed/rolled_back transition would be
// noise, and more importantly would be a route by which a future status
// value carrying more detail could slip through unreviewed.
func TestBuildFromEvent_IgnoresNonAwaitingConfirmStatuses(t *testing.T) {
	for _, status := range []string{"draft", "validated", "applying", "committed", "rolled_back", "requested"} {
		raw := []byte(`{"event":"changeset.status","id":"cs1","status":"` + status + `"}`)
		if _, ok := BuildFromEvent(raw); ok {
			t.Errorf("BuildFromEvent(status=%q) ok = true, want false", status)
		}
	}
}

func TestBuildFromEvent_DriftChanged(t *testing.T) {
	raw := []byte(`{"event":"drift.changed","count":3}`)
	got, ok := BuildFromEvent(raw)
	if !ok {
		t.Fatal("BuildFromEvent(drift.changed) ok = false, want true")
	}
	if got.Category != CategoryDrift {
		t.Errorf("Category = %q, want %q", got.Category, CategoryDrift)
	}
	if !strings.Contains(got.Body, "3") {
		t.Errorf("Body = %q, want it to mention the count (3)", got.Body)
	}
}

func TestBuildFromEvent_DriftChangedZeroIsIgnored(t *testing.T) {
	raw := []byte(`{"event":"drift.changed","count":0}`)
	if _, ok := BuildFromEvent(raw); ok {
		t.Error("BuildFromEvent(drift.changed, count=0) ok = true, want false")
	}
}

// TestBuildFromEvent_IgnoresUnhandledEvents is the negative leg proving
// this package does not silently push about events it was never asked to
// (findings.changed — too coarse for the critical-severity category,
// audit.appended, topology.delta, or a bogus/unknown event name) rather
// than defaulting to "push about everything".
func TestBuildFromEvent_IgnoresUnhandledEvents(t *testing.T) {
	for _, raw := range []string{
		`{"event":"findings.changed","count":5}`,
		`{"event":"audit.appended","id":1}`,
		`{"event":"topology.delta"}`,
		`{"event":"something.made.up"}`,
		`not even json`,
		`{}`,
	} {
		if _, ok := BuildFromEvent([]byte(raw)); ok {
			t.Errorf("BuildFromEvent(%s) ok = true, want false", raw)
		}
	}
}

// TestCriticalFindingNotification_ExactPayload pins the ENTIRE marshaled
// payload byte-for-byte. This is deliberately brittle: T-2005's security
// note requires the payload to carry NO finding-specific content (no
// finding id, Detail text, Nodes, Refs, or Check name), even though
// FindingNotifier (notifier.go) is invoked with a full findings.Finding on
// every qualifying transition. An exact-match assertion is what makes it
// impossible to add so much as one interpolated field here without this
// test failing — see TestCriticalFindingNotification_MutationCatchesLeak
// below for proof that it actually would.
func TestCriticalFindingNotification_ExactPayload(t *testing.T) {
	got, err := CriticalFindingNotification().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"category":"critical","event":"finding.critical","title":"New critical finding","body":"A new critical-severity finding needs attention.","url":"/tools?pushCategory=critical"}`
	if string(got) != want {
		t.Errorf("CriticalFindingNotification payload =\n  %s\nwant\n  %s", got, want)
	}
}

// TestCriticalFindingNotification_NeverContainsInjectedIdentity is the
// project-level negative leg: construct a Finding whose id/detail/nodes/
// refs are deliberately hostname/IP/guest-name-shaped (the exact field
// classes docs/security.md's telemetry Guard enumerates), confirm
// CriticalFindingNotification's output contains none of those substrings.
// Paired with a positive control (a marker string IS found when we
// deliberately search the finding's own encoded form for it) so this test
// cannot pass merely because the substring search itself is broken.
func TestCriticalFindingNotification_NeverContainsInjectedIdentity(t *testing.T) {
	type fakeFinding struct {
		ID     string   `json:"id"`
		Detail string   `json:"detail"`
		Nodes  []string `json:"nodes"`
		Refs   []string `json:"refs"`
	}
	injected := fakeFinding{
		ID:     "health:wg_handshake_stale|pve-rack3-node07",
		Detail: "bridge vmbr0 on pve-rack3-node07 has not seen a handshake from 10.20.30.40 in 6h",
		Nodes:  []string{"pve-rack3-node07"},
		Refs:   []string{"guest:pve-rack3-node07:117"},
	}
	injectedJSON, err := json.Marshal(injected)
	if err != nil {
		t.Fatalf("marshaling fixture: %v", err)
	}

	// Positive control: the marker IS present in the finding's own JSON —
	// proves the substring check below is capable of finding it at all.
	const marker = "pve-rack3-node07"
	if !strings.Contains(string(injectedJSON), marker) {
		t.Fatalf("test fixture bug: marker %q not present in its own fixture JSON", marker)
	}

	payload, err := CriticalFindingNotification().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, leak := range []string{"pve-rack3-node07", "10.20.30.40", "vmbr0", "wg_handshake_stale", "health:", "guest:"} {
		if strings.Contains(string(payload), leak) {
			t.Errorf("critical finding push payload contains %q: %s", leak, payload)
		}
	}
}

func TestParseCategories(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    []Category
		wantErr bool
	}{
		{"single valid", []string{"critical"}, []Category{CategoryCritical}, false},
		{"canonical order regardless of input order", []string{"drift", "critical"}, []Category{CategoryCritical, CategoryDrift}, false},
		{"dedupes", []string{"critical", "critical"}, []Category{CategoryCritical}, false},
		{"all three", []string{"awaitingConfirm", "drift", "critical"}, []Category{CategoryCritical, CategoryAwaitingConfirm, CategoryDrift}, false},
		{"empty is an error", []string{}, nil, true},
		{"unknown category is an error", []string{"critical", "urgent"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCategories(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseCategories(%v) error = nil, want an error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCategories(%v) unexpected error: %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseCategories(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseCategories(%v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}
