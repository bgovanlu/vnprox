// SPDX-License-Identifier: Apache-2.0

package change

import (
	"errors"
	"strings"
	"testing"
)

// --- loading and validation (acceptance criterion 5) -----------------------

// TestParsePolicySet_MalformedFailsWithFileRuleAndField is acceptance
// criterion 5: every load failure names the file, the offending rule, and
// the offending field. The daemon refuses to start on any of these
// (cmd/vnproxd/server.go returns the error from LoadPolicyFile), so a
// message that does not say WHICH rule and WHICH field is a message an
// operator cannot act on.
func TestParsePolicySet_MalformedFailsWithFileRuleAndField(t *testing.T) {
	cases := []struct {
		name     string
		doc      string
		wantRule string
		wantFld  string
		wantMsg  string
	}{
		{
			name: "unknown field in rule",
			doc: `version: 1
rules:
  - id: r-typo
    description: whatever
    severty: deny
    match:
      - {field: op, op: eq, value: bridge.create}
`,
			wantRule: "r-typo",
			wantFld:  "severty",
			wantMsg:  "not found",
		},
		{
			name: "missing description",
			doc: `version: 1
rules:
  - id: r-nodesc
    severity: deny
    match:
      - {field: op, op: eq, value: bridge.create}
`,
			wantRule: "r-nodesc",
			wantFld:  "rules[0].description",
			wantMsg:  "description is required",
		},
		{
			name: "unknown severity",
			doc: `version: 1
rules:
  - id: r-sev
    description: d
    severity: block
    match:
      - {field: op, op: eq, value: bridge.create}
`,
			wantRule: "r-sev",
			wantFld:  "rules[0].severity",
			wantMsg:  "severity must be",
		},
		{
			name: "unknown operator",
			doc: `version: 1
rules:
  - id: r-op
    description: d
    severity: deny
    match:
      - {field: op, op: equals, value: bridge.create}
`,
			wantRule: "r-op",
			wantFld:  "rules[0].match[0].op",
			wantMsg:  "unknown operator",
		},
		{
			name: "unknown fact field",
			doc: `version: 1
rules:
  - id: r-field
    description: d
    severity: deny
    match:
      - {field: target.hostname, op: eq, value: pve1}
`,
			wantRule: "r-field",
			wantFld:  "rules[0].match[0].field",
			wantMsg:  "unknown field",
		},
		{
			name: "params field no matching op type can have",
			doc: `version: 1
rules:
  - id: r-params
    description: d
    severity: deny
    match:
      - {field: op, op: eq, value: bridge.create}
      - {field: params.vlaan, op: eq, value: 1}
`,
			wantRule: "r-params",
			wantFld:  "rules[0].match[1].field",
			wantMsg:  "no op type this rule can match has a params field",
		},
		{
			name: "op type that does not exist",
			doc: `version: 1
rules:
  - id: r-nosuchop
    description: d
    severity: deny
    match:
      - {field: op, op: eq, value: bridge.explode}
`,
			wantRule: "r-nosuchop",
			wantFld:  "rules[0].match",
			wantMsg:  "can never fire",
		},
		{
			name: "op glob matching nothing",
			doc: `version: 1
rules:
  - id: r-noglob
    description: d
    severity: deny
    match:
      - {field: op, op: matches, value: "storage.*"}
`,
			wantRule: "r-noglob",
			wantFld:  "rules[0].match",
			wantMsg:  "can never fire",
		},
		{
			name: "unknown entity kind",
			doc: `version: 1
rules:
  - id: r-kind
    description: d
    severity: deny
    match:
      - {field: target.kind, op: eq, value: switchport}
`,
			wantRule: "r-kind",
			wantFld:  "rules[0].match[0].value",
			wantMsg:  "unknown entity kind",
		},
		{
			name: "duplicate rule id",
			doc: `version: 1
rules:
  - id: dup
    description: d
    severity: deny
    match: [{field: op, op: eq, value: bridge.create}]
  - id: dup
    description: d2
    severity: warn
    match: [{field: op, op: eq, value: bridge.delete}]
`,
			wantRule: "dup",
			wantFld:  "rules[1].id",
			wantMsg:  "duplicate rule id",
		},
		{
			name: "empty match",
			doc: `version: 1
rules:
  - id: r-empty
    description: d
    severity: deny
    match: []
`,
			wantRule: "r-empty",
			wantFld:  "rules[0].match",
			wantMsg:  "at least one condition",
		},
		{
			name: "in requires a list",
			doc: `version: 1
rules:
  - id: r-in
    description: d
    severity: deny
    match: [{field: target.node, op: in, value: pve1}]
`,
			wantRule: "r-in",
			wantFld:  "rules[0].match[0].value",
			wantMsg:  "requires a list",
		},
		{
			name: "gte requires a number",
			doc: `version: 1
rules:
  - id: r-gte
    description: d
    severity: deny
    match: [{field: op, op: eq, value: bridge.create}]
    assert: [{field: target.uplinkCount, op: gte, value: "two"}]
`,
			wantRule: "r-gte",
			wantFld:  "rules[0].assert[0].value",
			wantMsg:  "requires a numeric value",
		},
		{
			name: "unsupported format version",
			doc: `version: 99
rules:
  - id: r
    description: d
    severity: deny
    match: [{field: op, op: eq, value: bridge.create}]
`,
			wantRule: "",
			wantFld:  "version",
			wantMsg:  "unsupported policy format version",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePolicySet("/etc/vnprox/policy.yaml", []byte(tc.doc))
			if err == nil {
				t.Fatalf("ParsePolicySet accepted a malformed document")
			}
			var loadErr *PolicyLoadError
			if !errors.As(err, &loadErr) {
				t.Fatalf("error is %T, want *PolicyLoadError: %v", err, err)
			}
			if loadErr.File != "/etc/vnprox/policy.yaml" {
				t.Errorf("File = %q, want the file it was loaded from", loadErr.File)
			}
			if loadErr.RuleID != tc.wantRule {
				t.Errorf("RuleID = %q, want %q", loadErr.RuleID, tc.wantRule)
			}
			if loadErr.Field != tc.wantFld {
				t.Errorf("Field = %q, want %q", loadErr.Field, tc.wantFld)
			}
			if !strings.Contains(loadErr.Msg, tc.wantMsg) {
				t.Errorf("Msg = %q, want it to contain %q", loadErr.Msg, tc.wantMsg)
			}
			// The rendered message must carry all three, since that is
			// what an operator actually reads.
			msg := loadErr.Error()
			if !strings.Contains(msg, "/etc/vnprox/policy.yaml") {
				t.Errorf("Error() = %q, want it to name the file", msg)
			}
			if tc.wantRule != "" && !strings.Contains(msg, tc.wantRule) {
				t.Errorf("Error() = %q, want it to name rule %q", msg, tc.wantRule)
			}
			if !strings.Contains(msg, tc.wantFld) {
				t.Errorf("Error() = %q, want it to name field %q", msg, tc.wantFld)
			}
		})
	}
}

func TestParsePolicySet_Valid(t *testing.T) {
	doc := `version: 1
rules:
  - id: ok
    description: a valid rule
    severity: warn
    tags: [a, b]
    match: [{field: op, op: matches, value: "fw.*"}]
    assert: [{field: params.action, op: in, value: [ACCEPT, DROP]}]
`
	set, err := ParsePolicySet("f.yaml", []byte(doc))
	if err != nil {
		t.Fatalf("ParsePolicySet: %v", err)
	}
	if len(set.Rules) != 1 || set.Rules[0].ID != "ok" {
		t.Fatalf("rules = %+v, want one rule 'ok'", set.Rules)
	}
	if set.Version != PolicyFormatVersion {
		t.Errorf("Version = %d, want %d", set.Version, PolicyFormatVersion)
	}
	if got := set.Rules[0].Tags; len(got) != 2 {
		t.Errorf("Tags = %v, want two", got)
	}
}

func TestParsePolicySet_EmptyDocument(t *testing.T) {
	_, err := ParsePolicySet("f.yaml", []byte("   \n"))
	var loadErr *PolicyLoadError
	if !errors.As(err, &loadErr) || !strings.Contains(loadErr.Msg, "empty") {
		t.Fatalf("err = %v, want a *PolicyLoadError about an empty file", err)
	}
}

// TestPolicySet_ZeroValueIsValid pins the property AC6 rests on: the zero
// value is a legal, empty policy set.
func TestPolicySet_ZeroValueIsValid(t *testing.T) {
	var set PolicySet
	if err := set.Validate(""); err != nil {
		t.Fatalf("the zero PolicySet must be valid, got %v", err)
	}
	if !set.IsEmpty() {
		t.Fatalf("the zero PolicySet must be empty")
	}
}

// --- rule-set diff (acceptance criterion 7) --------------------------------

func TestDiffPolicySets(t *testing.T) {
	rule := func(id, desc string, sev PolicySeverity) PolicyRule {
		return PolicyRule{
			ID: id, Description: desc, Severity: sev,
			Match: []PolicyCondition{{Field: policyFieldOpType, Op: PolicyOpEq, Value: "bridge.create"}},
		}
	}
	oldSet := PolicySet{Version: 1, Rules: []PolicyRule{rule("keep", "unchanged", PolicyDeny), rule("gone", "removed", PolicyWarn), rule("edit", "before", PolicyWarn)}}
	newSet := PolicySet{Version: 1, Rules: []PolicyRule{rule("keep", "unchanged", PolicyDeny), rule("edit", "after", PolicyDeny), rule("fresh", "added", PolicyWarn)}}

	d := DiffPolicySets(oldSet, newSet)
	if len(d.Added) != 1 || d.Added[0].ID != "fresh" {
		t.Errorf("Added = %+v, want [fresh]", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0].ID != "gone" {
		t.Errorf("Removed = %+v, want [gone]", d.Removed)
	}
	if len(d.Changed) != 1 {
		t.Fatalf("Changed = %+v, want one entry", d.Changed)
	}
	// AC7: BOTH sides are carried in full, so the audit entry alone
	// reconstructs the change without consulting any prior entry.
	if d.Changed[0].Before.Description != "before" || d.Changed[0].After.Description != "after" {
		t.Errorf("Changed[0] = %+v, want before/after descriptions carried in full", d.Changed[0])
	}
	if d.Changed[0].Before.Severity != PolicyWarn || d.Changed[0].After.Severity != PolicyDeny {
		t.Errorf("Changed[0] severities = %s/%s, want warn/deny", d.Changed[0].Before.Severity, d.Changed[0].After.Severity)
	}
	if d.IsEmpty() {
		t.Errorf("IsEmpty() = true for a non-empty diff")
	}

	if same := DiffPolicySets(oldSet, oldSet); !same.IsEmpty() {
		t.Errorf("DiffPolicySets(x, x) = %+v, want empty", same)
	}
}

func TestMarshalPolicySet_RoundTrips(t *testing.T) {
	set, err := ExamplePolicySet()
	if err != nil {
		t.Fatalf("ExamplePolicySet: %v", err)
	}
	data, err := MarshalPolicySet(set)
	if err != nil {
		t.Fatalf("MarshalPolicySet: %v", err)
	}
	back, err := ParsePolicySet("round-trip", data)
	if err != nil {
		t.Fatalf("re-parsing a marshaled policy set: %v", err)
	}
	if d := DiffPolicySets(set, back); !d.IsEmpty() {
		t.Errorf("round trip changed the rule set: %+v", d)
	}
}
