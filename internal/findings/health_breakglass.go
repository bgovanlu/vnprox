// SPDX-License-Identifier: Apache-2.0

// health_breakglass.go implements the "change_break_glass" check (T-2604):
// one error-severity finding per emergency break-glass invocation — the
// reasoned override that let a changeset in a protected op class apply
// without its required number of distinct approvers.
//
// This finding is the consequence half of the break-glass ceremony. Its
// point is that an override cannot be taken quietly: it appears in the same
// unified stream everything else does, at error severity, naming who took it
// and why — and it CANNOT BE ACKNOWLEDGED FOR 24 HOURS (Finding.AckableAt,
// enforced in ack.go), so the person who invoked it cannot silence it on
// their way out of the incident. The review it forces is by someone who was
// not in the room.
//
// Detection-only and hysteresis-exempt, for the same reason the rogue checks
// are: a break-glass invocation is a discrete, already-happened security
// event recorded once, not a noisy threshold a debouncer would smooth.

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
)

// CheckBreakGlass is the check name for an emergency two-person-rule
// override.
const CheckBreakGlass = "change_break_glass"

const breakGlassDocsLink = "docs/features/change-management.md#4-apply-confirm-rollback"

// BreakGlassProvider is the seam checkBreakGlass needs:
// change.Service.BreakGlassEvents, live over the current
// changeset_breakglass table state. Context-free, matching
// ScheduleMissedProvider's shape (a findings cycle has no request context to
// thread through) — cmd/vnproxd's wiring adapts *change.Service accordingly.
type BreakGlassProvider interface {
	BreakGlassEvents() []change.BreakGlassRecord
}

// checkBreakGlass emits one finding per recorded break-glass invocation. A
// nil provider (not wired) yields no findings — the same "quietly absent"
// degradation every other optional producer input documents.
//
// The finding does NOT age out on its own. It stays in the stream until an
// operator acknowledges it, which they cannot do until 24 hours have passed:
// an override that stopped being visible because enough time went by would
// be an override nobody ever had to answer for.
func checkBreakGlass(svc BreakGlassProvider) []Finding {
	if svc == nil {
		return nil
	}
	events := svc.BreakGlassEvents()
	if len(events) == 0 {
		return nil
	}

	out := make([]Finding, 0, len(events))
	for _, ev := range events {
		detail := fmt.Sprintf(
			"%s applied changeset %s under emergency break-glass, overriding the two-person rule on its protected op class: %q. This finding cannot be acknowledged until 24 hours after the override.",
			ev.InvokedBy, ev.ChangesetID, ev.Reason,
		)
		f := newHealthFinding(CheckBreakGlass, SeverityError, detail, nil, []string{ev.ChangesetID})
		f.DocsLink = breakGlassDocsLink
		f.AckableAt = ev.AckableAt
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
