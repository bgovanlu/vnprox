// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// TestChangesetView_JSONSchema_Stable is a regression guard in the same
// family as internal/sim.TestRuleRef_JSONSchema_Stable and
// internal/change/ifaces.TestChangesetDiff_JSONSchema_Stable: changesetView
// is the frozen `changesets.create`/`changesets.validate` MCP tools'
// payload shape (handleChangesetCreate/handleChangesetValidate both return
// toChangesetView(c)), so docs/architecture.md §13.1's additive-only policy
// applies to it too. T-2003 deliberately did NOT add its new review-surface
// fields (Comments, Approval — see change.Changeset's doc comments) to this
// view: this test is the checked-in evidence that decision was verified,
// not assumed — changesetView carries no `ops` field at all (Op.ID, the
// other T-2003 addition, is therefore not reachable here either), and every
// pre-existing field is still present, byte for byte.
func TestChangesetView_JSONSchema_Stable(t *testing.T) {
	view := toChangesetView(change.Changeset{
		ID: "cs1", Title: "t", Author: "root@pam", Status: change.StatusDraft,
		Origin: change.OriginMCP, OriginTokenID: "tok-1",
		Findings:  []change.Finding{{Severity: change.SeverityWarning, Code: "x", Message: "y"}},
		CreatedAt: 1, UpdatedAt: 2,
	})

	got, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"id", "title", "author", "status", "origin", "originTokenId", "findings", "createdAt", "updatedAt"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("changesetView JSON missing frozen field %q (got %v)", field, generic)
		}
	}
	if _, ok := generic["ops"]; ok {
		t.Error("changesetView JSON unexpectedly carries an \"ops\" field — changesets.create/validate has never exposed ops, and T-2003's Op.ID addition must not become reachable here")
	}
	if _, ok := generic["comments"]; ok {
		t.Error("changesetView JSON unexpectedly carries T-2003's review-surface \"comments\" field — that addition is deliberately scoped to the HTTP API's GET /changesets/{id}, not this MCP view")
	}
}

// TestChangesetView_OriginTool_SurvivesForStageTools is T-3204's addition:
// the test above never sets OriginTool, so it never actually proves that
// field reaches the wire — and OriginTool is the ONE field that
// distinguishes the four frozen `changesets.stage.*` tools' payload
// (internal/mcp/stage.go's stage(), "return toChangesetView(c), nil") from
// `changesets.create`/`changesets.validate`'s (docs/api.md: "the same
// changesets.create/validate payload, plus originTool"). Since all six
// tools share this one function verbatim, this one extra assertion closes
// the gap for all four stage tools at once rather than needing a fifth
// near-duplicate test per tool.
func TestChangesetView_OriginTool_SurvivesForStageTools(t *testing.T) {
	view := toChangesetView(change.Changeset{
		ID: "cs1", Title: "t", Author: "root", Status: change.StatusDraft,
		Origin: change.OriginMCP, OriginTokenID: "tok-1", OriginTool: "changesets.stage.bridge",
		CreatedAt: 1, UpdatedAt: 2,
	})
	got, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if generic["originTool"] != "changesets.stage.bridge" {
		t.Errorf("changesetView JSON missing/wrong frozen field \"originTool\" for a stage-tool-created draft (got %v)", generic["originTool"])
	}
}
