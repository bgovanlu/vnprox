// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"encoding/json"
	"testing"
)

// Phase 36. The frontend registry keys off Remediation.Action, so these
// strings are a wire contract, not an implementation detail: a producer that
// emits an action the SPA does not know renders no button, and the finding
// still looks perfectly fine. That failure is invisible, so it gets a test.
func TestRemediationActionConstantsAreStable(t *testing.T) {
	// Field order is fieldalignment's, not the reading order.
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"mgmt redundancy wizard", RemedyActionMgmtRedundancy, "mgmt.redundancy"},
		{"in-app navigation", RemedyActionNavigate, "navigate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("action = %q, want %q — this string is matched by web/src/findings/remediation.ts", tt.got, tt.want)
			}
		})
	}
}

// The JSON shape is docs/api.md's GET /findings contract.
func TestRemediationJSONShape(t *testing.T) {
	b, err := json.Marshal(Remediation{
		Action: RemedyActionMgmtRedundancy,
		Kind:   RemedyNavigate,
		Label:  "Add a redundant path",
		Params: map[string]string{"node": "pve1"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"action":"mgmt.redundancy","kind":"navigate","label":"Add a redundant path","params":{"node":"pve1"}}`
	if string(b) != want {
		t.Errorf("json = %s, want %s", b, want)
	}
}

// A detection-only finding must serialise with no `remedy` key at all —
// not `"remedy":null`. An older SPA reading a newer daemon should see a
// byte-identical payload for every finding that has no remedy.
func TestFindingWithoutRemedyOmitsTheKey(t *testing.T) {
	b, err := json.Marshal(newHealthFinding("some_check", SeverityWarning, "detail", []string{"pve1"}, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); contains(got, "remedy") {
		t.Errorf("json = %s, want no remedy key", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
