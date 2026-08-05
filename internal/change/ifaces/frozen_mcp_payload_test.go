package ifaces

import (
	"encoding/json"
	"testing"
)

// TestChangesetDiff_JSONSchema_Stable is a regression guard against the
// exact mistake T-2002 almost shipped for internal/sim.RuleRef (see that
// package's TestRuleRef_JSONSchema_Stable and planning/reports/T-2002.md):
// *ChangesetDiff is not just this package's own return value — it is also
// the frozen `changesets.diff` MCP tool's payload, returned VERBATIM
// (internal/mcp/server.go's handleChangesetDiff: `diff, err :=
// s.deps.Staging.Diff(ctx, id); ... return diff, nil` — no projection, no
// narrowing view). docs/architecture.md §13.1 (decision D10) makes this an
// additive-only contract: no field is ever removed or renamed without a
// version bump. T-2003 extended what Files may CONTAIN (SDN config diffs
// alongside node-file ones — diff_sdn.go, internal/change) without touching
// either struct's shape; this test golden-checks that FileDiff's and
// OpSummary's documented field names are still present, byte for byte, so a
// future "clean up this struct" refactor gets caught here rather than
// silently breaking an external MCP client this repo has no way to grep for.
func TestChangesetDiff_JSONSchema_Stable(t *testing.T) {
	diff := ChangesetDiff{
		Files: []FileDiff{{Node: "pve1", Path: "/etc/network/interfaces", Unified: "--- a\n+++ b\n", Changed: true}},
		Ops:   []OpSummary{{Op: "bridge.create", Target: "bridge:pve1:vmbr1", Node: "pve1", Summary: "Create bridge vmbr1"}},
	}

	got, err := json.Marshal(diff)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"files", "ops"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("ChangesetDiff JSON missing frozen top-level field %q (got %s)", field, got)
		}
	}

	files, _ := generic["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v, want one entry", files)
	}
	fileEntry, _ := files[0].(map[string]any)
	for _, field := range []string{"node", "path", "unified", "changed"} {
		if _, ok := fileEntry[field]; !ok {
			t.Errorf("FileDiff JSON missing frozen field %q (got %v)", field, fileEntry)
		}
	}

	ops, _ := generic["ops"].([]any)
	if len(ops) != 1 {
		t.Fatalf("ops = %v, want one entry", ops)
	}
	opEntry, _ := ops[0].(map[string]any)
	for _, field := range []string{"op", "target", "node", "summary"} {
		if _, ok := opEntry[field]; !ok {
			t.Errorf("OpSummary JSON missing frozen field %q (got %v)", field, opEntry)
		}
	}
}
