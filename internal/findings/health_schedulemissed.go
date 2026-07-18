// health_schedulemissed.go implements the "schedule_missed" health check
// (T-1103): one finding per changeset whose scheduled maintenance window
// elapsed entirely without ever firing (missedWindowPolicy "skip" — the
// default), surfaced through the same unified findings stream every other
// producer feeds so an operator notices it without having to separately
// poll changeset schedules. Detection-only (no fix, exactly like
// mgmt_single_path): there is no single obviously-correct automated
// response to a missed maintenance window (reschedule? apply now anyway?),
// so this only tells the operator it happened.
//
// Hysteresis-exempt (mgmt_single_path-style): "missed" is a discrete,
// already-resolved event recorded once by change.Service.TickSchedules, not
// a noisy threshold a debouncer would need to smooth.

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
)

const CheckScheduleMissed = "schedule_missed"

const scheduleMissedDocsLink = "docs/features/change-management.md#4-apply-confirm-rollback"

// ScheduleMissedProvider is the seam checkScheduleMissed needs:
// change.Service.MissedSchedules, live over the current changeset_schedules
// table state. No context parameter, matching MgmtProvider/CorosyncProvider's
// context-free shape (Engine's own healthFindings cycle has no request
// context to thread through) — cmd/vnproxd's wiring adapts *change.Service
// accordingly.
type ScheduleMissedProvider interface {
	MissedSchedules() []change.MissedSchedule
}

// checkScheduleMissed evaluates every currently-missed schedule and flags
// one finding per changeset. A nil provider (not wired) yields no findings —
// detection-only, same "quietly absent" degradation docs/api.md documents
// for every other optional producer input.
func checkScheduleMissed(svc ScheduleMissedProvider) []Finding {
	if svc == nil {
		return nil
	}
	missed := svc.MissedSchedules()
	if len(missed) == 0 {
		return nil
	}

	out := make([]Finding, 0, len(missed))
	for _, m := range missed {
		detail := fmt.Sprintf(
			"changeset %s's scheduled maintenance window (%d-%d) elapsed without ever applying — reschedule it or apply it by hand",
			m.ChangesetID, m.WindowStart, m.WindowEnd,
		)
		f := newHealthFinding(CheckScheduleMissed, SeverityWarning, detail, nil, []string{m.ChangesetID})
		f.DocsLink = scheduleMissedDocsLink
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
