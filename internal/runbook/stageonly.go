// SPDX-License-Identifier: Apache-2.0

// stageonly.go holds this package's half of T-4003 acceptance criterion 2:
// the change-engine interface Service holds (changeCreator, service.go) has
// no apply, confirm, or rollback method — asserted at compile time, not by
// a runtime check, exactly like internal/mcp/stageonly.go's identical
// assertion for the MCP seam and internal/plugin/stager.go's Stager
// interface for the plugin seam. See either file's own doc comment for the
// full mechanism this repeats: Go cannot say "this interface must NOT have
// method X" directly, but it can say the equivalent from the other side —
// a type whose method set is EXACTLY the stage-only verbs must satisfy the
// interface, so widening the interface with Apply/Confirm/Rollback/Approve/
// Discard immediately breaks this file's build, naming the offending
// method, before any test runs.

package runbook

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/change"
)

// stageOnlyShape implements exactly the verbs a runbook is allowed to
// reach and NOTHING else. Do not add a method here to "fix" a build
// failure: a build failure here means someone widened changeCreator, and
// the fix is to un-widen it, not to grow this type to match.
type stageOnlyShape struct{}

func (stageOnlyShape) Create(context.Context, string, string, []change.Op) (change.Changeset, error) {
	panic("runbook: stageOnlyShape is a compile-time assertion, never a change engine")
}

func (stageOnlyShape) Validate(context.Context, string, string) (change.Changeset, error) {
	panic("runbook: stageOnlyShape is a compile-time assertion, never a change engine")
}

// THE assertion. If this line stops compiling, an apply/confirm/rollback-
// shaped method was added to changeCreator and this package's stage-only
// guarantee has been broken.
var _ changeCreator = stageOnlyShape{}

// The converse assertion: the real change engine still satisfies the
// narrow seam, so changeCreator is genuinely the view Service is handed (a
// seam nothing real implements would be a comment, not a constraint).
var _ changeCreator = (*change.Service)(nil)
