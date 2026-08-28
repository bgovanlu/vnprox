// SPDX-License-Identifier: Apache-2.0

// stageonly.go holds T-2705 acceptance criterion 2: the change-engine
// interface this package holds has no apply, confirm, or approve method —
// **asserted at compile time**, not by a runtime check.
//
// Go cannot say "this interface must NOT have method X" directly. It can say
// the equivalent, from the other side: a type whose method set is EXACTLY the
// stage-only verbs must satisfy ChangesetStager. Adding any method to that
// interface — Apply, Confirm, Approve, Rollback, Discard, or anything else —
// immediately makes stageOnlyShape stop satisfying it, and `go build` fails,
// naming the offending method:
//
//	cannot use stageOnlyShape{} (value of type stageOnlyShape) as ChangesetStager
//	value in variable declaration: stageOnlyShape does not implement
//	ChangesetStager (missing method Apply)
//
// That is a stronger guarantee than the reflection test in registry_test.go
// (which catches the same edit, but only when the suite runs): a build that can
// apply through MCP cannot be produced at all. Both are kept — the reflection
// test states the invariant in the suite where a reader looks for it, and this
// file makes ignoring it impossible.
//
// stageOnlyShape is never instantiated for use; the methods panic because
// calling one would mean something had wired this placeholder as a real change
// engine, which is a programming error, not a runtime condition.

package mcp

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/change/ifaces"
)

// stageOnlyShape implements exactly the verbs an MCP tool is allowed to reach
// and NOTHING else. Do not add a method here to "fix" a build failure: a build
// failure here means someone widened ChangesetStager, and the fix is to
// un-widen it.
type stageOnlyShape struct{}

func (stageOnlyShape) CreateWithOrigin(context.Context, string, string, []change.Op, string, string) (change.Changeset, error) {
	panic("mcp: stageOnlyShape is a compile-time assertion, never a change engine")
}

func (stageOnlyShape) CreateWithProvenance(context.Context, string, string, []change.Op, change.Provenance) (change.Changeset, error) {
	panic("mcp: stageOnlyShape is a compile-time assertion, never a change engine")
}

func (stageOnlyShape) UpdateDraft(context.Context, string, string, *string, []change.Op) (change.Changeset, error) {
	panic("mcp: stageOnlyShape is a compile-time assertion, never a change engine")
}

func (stageOnlyShape) Validate(context.Context, string, string) (change.Changeset, error) {
	panic("mcp: stageOnlyShape is a compile-time assertion, never a change engine")
}

func (stageOnlyShape) Diff(context.Context, string) (*ifaces.ChangesetDiff, error) {
	panic("mcp: stageOnlyShape is a compile-time assertion, never a change engine")
}

func (stageOnlyShape) List(context.Context, string) ([]change.Changeset, error) {
	panic("mcp: stageOnlyShape is a compile-time assertion, never a change engine")
}

// THE assertion. If this line stops compiling, an apply/confirm/approve-shaped
// method was added to ChangesetStager and the MCP surface's stage-only
// guarantee has been broken.
var _ ChangesetStager = stageOnlyShape{}

// The converse assertion: the real change engine still satisfies the narrow
// seam, so the interface above is genuinely the view *change.Service is handed
// (a seam nothing implements would be a comment, not a constraint).
var _ ChangesetStager = (*change.Service)(nil)

// And the policy seam, likewise: T-2601's evaluator is what a staging tool is
// policy-checked by, in-process, with no second implementation of what a rule
// means.
var _ PolicyChecker = (*change.Service)(nil)
