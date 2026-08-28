// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

// assertDocumentedJSON is the one call site every `-o json` command's test
// uses to pin its output against docs/cli-json.md (T-4011): decode raw,
// load that command's fenced table, and fail with every mismatch named if
// the two disagree in either direction.
func assertDocumentedJSON(t *testing.T, cmd string, raw []byte) {
	t.Helper()
	fields, err := jsonShape(raw)
	if err != nil {
		t.Fatalf("%s: %v (output: %s)", cmd, err, raw)
	}
	md, err := loadCLIJSONDocMarkdown()
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	rows, err := parseCLIJSONDocTable(md, cmd)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	if err := compareCLIJSONDoc(cmd, fields, rows); err != nil {
		t.Errorf("%s: documented JSON shape does not match actual output: %v", cmd, err)
	}
}

func TestCoarseJSONType(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "null"},
		{true, "boolean"},
		{float64(3), "number"},
		{"x", "string"},
		{map[string]any{"a": 1}, "object"},
		{[]any{}, "array"},
		{[]any{"a", "b"}, "array of string"},
		{[]any{map[string]any{}, map[string]any{}}, "array of object"},
		{[]any{"a", float64(1)}, "array"},
	}
	for _, c := range cases {
		if got := coarseJSONType(c.v); got != c.want {
			t.Errorf("coarseJSONType(%#v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestJSONShape_RejectsNonObjectTopLevel(t *testing.T) {
	if _, err := jsonShape([]byte(`[1,2,3]`)); err == nil {
		t.Error("jsonShape of an array of scalars: want an error (an array command must contain objects)")
	}
	if _, err := jsonShape([]byte(`42`)); err == nil {
		t.Error("jsonShape of a bare scalar: want an error")
	}
}

// TestJSONShape_ArrayOfObjectsUnionsFields covers the array-shaped commands
// (snapshots list, remote drift, remote changesets list, verify --list):
// an omitempty field present on only some elements must still surface once,
// not be dropped or force a mismatch.
func TestJSONShape_ArrayOfObjectsUnionsFields(t *testing.T) {
	fields, err := jsonShape([]byte(`[{"id":"a","note":"x"},{"id":"b"}]`))
	if err != nil {
		t.Fatalf("jsonShape: %v", err)
	}
	names := map[string]string{}
	for _, f := range fields {
		names[f.Name] = f.Type
	}
	if names["id"] != "string" || names["note"] != "string" {
		t.Errorf("fields = %+v, want id and note both present as string", fields)
	}
}

// TestCLIJSONDoc_ParseAndCompareRoundTrip proves the parser/comparator pair
// itself is correct, independent of docs/cli-json.md's real content —
// exactly the "test the harness, not just the data" step
// internal/telemetry/docs_test.go and internal/perfbudget/docs_test.go each
// take for their own copy of this idiom.
func TestCLIJSONDoc_ParseAndCompareRoundTrip(t *testing.T) {
	md := "" +
		cliJSONDocTableBegin("t-test") + "\n" +
		"| Field | Type | Description |\n" +
		"|---|---|---|\n" +
		"| `a` | string | the a field |\n" +
		"| `b` | number | the b field |\n" +
		cliJSONDocTableEnd("t-test") + "\n"

	rows, err := parseCLIJSONDocTable(md, "t-test")
	if err != nil {
		t.Fatalf("parseCLIJSONDocTable: %v", err)
	}
	if len(rows) != 2 || rows[0].Field != "a" || rows[1].Field != "b" {
		t.Fatalf("rows = %+v, want a, b", rows)
	}

	fields, err := jsonShape([]byte(`{"a":"x","b":1}`))
	if err != nil {
		t.Fatalf("jsonShape: %v", err)
	}
	if err := compareCLIJSONDoc("t-test", fields, rows); err != nil {
		t.Errorf("matching shape reported a mismatch: %v", err)
	}

	// An undocumented field must be caught.
	extra, _ := jsonShape([]byte(`{"a":"x","b":1,"c":true}`))
	if err := compareCLIJSONDoc("t-test", extra, rows); err == nil {
		t.Error("an undocumented field 'c' should have been caught")
	}

	// A documented-but-absent field must be caught.
	missing, _ := jsonShape([]byte(`{"a":"x"}`))
	if err := compareCLIJSONDoc("t-test", missing, rows); err == nil {
		t.Error("a documented field 'b' missing from the actual output should have been caught")
	}

	// A type mismatch must be caught.
	wrongType, _ := jsonShape([]byte(`{"a":"x","b":"not a number"}`))
	if err := compareCLIJSONDoc("t-test", wrongType, rows); err == nil {
		t.Error("a type mismatch on 'b' should have been caught")
	}
}

func TestCLIJSONDoc_MissingTableIsAnError(t *testing.T) {
	if _, err := parseCLIJSONDocTable("no tables here", "nope"); err == nil {
		t.Error("want an error when the command's table is entirely absent")
	}
}

// TestEveryDataEmittingCommandSupportsOJSON is T-4011 acceptance criterion 1
// for the whole binary (T-1105's TestEveryRemoteCommandSupportsOJSON/
// TestEveryLegacyCommandSupportsOJSON in remote_test.go cover its own
// narrower vintage; this table is the T-4011 superset, including every
// command this card added -o json to: certs, policy lint, telemetry
// send/reset-id, and the whole new spec family).
//
// Three commands are deliberately absent, each for a reason recorded once
// here rather than at every call site: `telemetry preview` already prints
// the payload's exact JSON bytes with no wrapping envelope (adding -o json
// would mean deciding what "JSON of already-JSON" even is); `policy
// examples` prints the shipped example policy as raw YAML, not JSON, so
// there is no JSON shape for -o to select between; `completion bash|zsh`
// emits a shell script, not data.
func TestEveryDataEmittingCommandSupportsOJSON(t *testing.T) {
	commands := [][]string{
		{"status"},
		{"snapshots", "list"},
		{"snapshots", "restore"},
		{"rollback-now"},
		{"backup"},
		{"restore"},
		{"certs"},
		{"doctor"},
		{"verify"},
		{"support-bundle"},
		{"telemetry", "status"},
		{"telemetry", "send"},
		{"telemetry", "reset-id"},
		{"remote", "topology"},
		{"remote", "changesets", "list"},
		{"remote", "changesets", "get"},
		{"remote", "changesets", "diff"},
		{"remote", "changesets", "create"},
		{"remote", "changesets", "validate"},
		{"remote", "changesets", "apply"},
		{"remote", "changesets", "confirm"},
		{"remote", "changesets", "rollback"},
		{"remote", "changesets", "discard"},
		{"remote", "findings"},
		{"remote", "drift"},
		{"remote", "audit"},
		{"apply"},
		{"policy", "test"},
		{"policy", "lint"},
		{"gitsync", "status"},
		{"hub", "publish"},
		{"hub", "index"},
		{"hub", "revoke"},
		{"hub", "verify"},
		{"hub", "keygen"},
		{"hub", "mirror"},
		{"hub", "pull"},
		{"plugin", "scaffold"},
		{"spec", "export"},
		{"spec", "import"},
		{"spec", "pin"},
		{"spec", "unpin"},
	}
	for _, cmd := range commands {
		t.Run(strings.Join(cmd, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(append([]string{}, cmd...), "-h")
			_ = run(args, &stdout, &stderr)
			if !strings.Contains(stderr.String(), "-o string") {
				t.Errorf("%v -h stderr = %q, want it to document the -o flag", cmd, stderr.String())
			}
		})
	}
}
