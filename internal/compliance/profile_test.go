// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/posture"
)

func TestParseProfile_Rejects(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		// wantField is the field the error must name, so the operator can
		// find it. Empty means "do not assert the field".
		wantField string
		wantMsg   string
	}{
		{
			name:      "unsupported format version",
			yaml:      "formatVersion: 99\nid: x\ntitle: X\nversion: '1'\nnotice: n\ncontrols: []\n",
			wantField: "formatVersion",
		},
		{
			name:      "missing notice",
			yaml:      "id: x\ntitle: X\nversion: '1'\ncontrols:\n  - id: C\n    title: T\n    statement: S\n    unmappedReason: R\n",
			wantField: "notice",
		},
		{
			name:      "no controls",
			yaml:      "id: x\ntitle: X\nversion: '1'\nnotice: n\ncontrols: []\n",
			wantField: "controls",
		},
		{
			name:      "duplicate control id",
			yaml:      profileWith("  - id: C\n    title: T\n    statement: S\n    unmappedReason: R\n  - id: C\n    title: T\n    statement: S\n    unmappedReason: R\n"),
			wantField: "controls[1].id",
			wantMsg:   "duplicate",
		},
		{
			// The card's own safety property, enforced at load: a control
			// with no evidence must say why, or the reader cannot tell it
			// from an unwritten mapping.
			name:      "unmapped control with no stated reason",
			yaml:      profileWith("  - id: C\n    title: T\n    statement: S\n"),
			wantField: "controls[0].unmappedReason",
		},
		{
			name:      "unknown evidence kind",
			yaml:      profileWith("  - id: C\n    title: T\n    statement: S\n    evidence:\n      - kind: vibes\n        check: x\n"),
			wantField: "controls[0].evidence[0].kind",
		},
		{
			name:      "check evidence carrying a posture selector",
			yaml:      profileWith("  - id: C\n    title: T\n    statement: S\n    evidence:\n      - kind: check\n        check: x\n        factor: segmentation\n"),
			wantField: "controls[0].evidence[0].factor",
		},
		{
			name:      "posture evidence with an out-of-range minimum",
			yaml:      profileWith("  - id: C\n    title: T\n    statement: S\n    evidence:\n      - kind: posture\n        factor: segmentation\n        minScore: 140\n"),
			wantField: "controls[0].evidence[0].minScore",
		},
		{
			name:      "policy evidence naming both rule and tag",
			yaml:      profileWith("  - id: C\n    title: T\n    statement: S\n    evidence:\n      - kind: policy\n        rule: r\n        tag: t\n"),
			wantField: "controls[0].evidence[0].rule",
		},
		{
			name:      "check evidence with an unknown severity threshold",
			yaml:      profileWith("  - id: C\n    title: T\n    statement: S\n    evidence:\n      - kind: check\n        check: x\n        failAt: catastrophic\n"),
			wantField: "controls[0].evidence[0].failAt",
		},
		{
			name:    "unknown top-level field",
			yaml:    "id: x\ntitle: X\nversion: '1'\nnotice: n\nsurprise: true\ncontrols: []\n",
			wantMsg: "cannot be parsed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProfile("test.yaml", []byte(tc.yaml))
			if err == nil {
				t.Fatal("ParseProfile accepted a document it must reject")
			}
			if !strings.Contains(err.Error(), "test.yaml") {
				t.Errorf("error %q does not name the file", err)
			}
			if tc.wantField != "" && !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("error %q does not name field %q", err, tc.wantField)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not contain %q", err, tc.wantMsg)
			}
		})
	}
}

func profileWith(controls string) string {
	return "formatVersion: 1\nid: x\ntitle: X\nversion: '1'\nnotice: n\ncontrols:\n" + controls
}

func TestParseProfile_AcceptsAWellFormedDocument(t *testing.T) {
	p, err := ParseProfile("test.yaml", []byte(profileWith(
		"  - id: C1\n    title: T\n    statement: S\n    evidence:\n"+
			"      - kind: check\n        check: mgmt_single_path\n        note: why\n"+
			"      - kind: posture\n        factor: segmentation\n        minScore: 70\n"+
			"      - kind: policy\n        tag: change-control\n"+
			"  - id: C2\n    title: T2\n    statement: S2\n    unmappedReason: nothing observes this\n")))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if got := p.MappedChecks(); !slices.Equal(got, []string{"mgmt_single_path"}) {
		t.Errorf("MappedChecks() = %v, want [mgmt_single_path]", got)
	}
	if got := p.Controls[0].Evidence[2].Key(); got != "policy:tag:change-control" {
		t.Errorf("policy-by-tag evidence key = %q", got)
	}
}

// TestBuiltinProfiles_AreUsable is the shipped profile's own gate: it must
// parse, it must be the ONE general profile (a directory of framework-named
// files would be the certification claim this feature must not make), and
// every selector it names must be one this build actually produces.
func TestBuiltinProfiles_AreUsable(t *testing.T) {
	profiles, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("vnprox ships %d profiles; exactly one general profile is the deliverable", len(profiles))
	}
	p := profiles[0]
	if p.ID != GeneralProfileID {
		t.Errorf("shipped profile id = %q, want %q", p.ID, GeneralProfileID)
	}

	catalog := map[string]bool{}
	for _, name := range findings.AllCheckNames() {
		catalog[name] = true
	}
	factors := map[string]bool{
		posture.FactorSPOF: true, posture.FactorSegmentation: true, posture.FactorExposedPorts: true,
		posture.FactorAnomalyRate: true, posture.FactorDriftHygiene: true,
	}
	unmapped := 0
	for _, c := range p.Controls {
		if len(c.Evidence) == 0 {
			unmapped++
		}
		for _, e := range c.Evidence {
			switch e.Kind {
			case EvidenceCheck:
				if !catalog[e.Check] {
					t.Errorf("control %s maps check %q, which findings.AllCheckNames() does not list", c.ID, e.Check)
				}
			case EvidencePosture:
				if !factors[e.Factor] {
					t.Errorf("control %s maps posture factor %q, which internal/posture does not produce", c.ID, e.Factor)
				}
			case EvidencePolicy:
				if e.Rule != "" {
					t.Errorf("control %s maps policy rule id %q; a GENERAL profile cannot know a cluster's rule ids, so it must select by tag", c.ID, e.Rule)
				}
			}
		}
	}
	if unmapped == 0 {
		t.Error("the shipped profile has no unmapped control; a profile that claims vnprox can evidence everything is the liability this card exists to avoid")
	}
}

// TestBuiltinProfile_MakesNoCertificationClaim is the arc risk register's
// mitigation, asserted rather than assumed: the shipped profile must not name
// a published framework anywhere in its text.
//
// BREAK IT TO SEE IT FIRE: put "CIS Benchmark" in the profile's description.
func TestBuiltinProfile_MakesNoCertificationClaim(t *testing.T) {
	profiles, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	// Whole words only: "decision" contains "cis" and "administrative"
	// contains "nist", and a gate that fires on those is a gate nobody
	// keeps. The notice itself legitimately uses "certification" and
	// "attestation" — in the negative — so those are asserted separately
	// below rather than banned outright.
	forbidden := regexp.MustCompile(`(?i)\b(cis|pci|dss|hipaa|soc ?2|iso ?27001|nist|fedramp|gdpr|cmmc|accredited)\b`)

	for _, p := range profiles {
		var b strings.Builder
		b.WriteString(p.Title + " " + p.Description + " " + p.Notice)
		for _, c := range p.Controls {
			b.WriteString(" " + c.Title + " " + c.Statement + " " + c.UnmappedReason)
			for _, e := range c.Evidence {
				b.WriteString(" " + e.Note)
			}
		}
		if m := forbidden.FindString(b.String()); m != "" {
			t.Errorf("shipped profile %s mentions %q; this feature ships a format, not a claim of accreditation", p.ID, m)
		}
		if !strings.Contains(strings.ToLower(p.Notice), "not a certification") {
			t.Errorf("shipped profile %s's notice does not say it is not a certification", p.ID)
		}
	}
}
