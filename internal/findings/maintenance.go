// SPDX-License-Identifier: Apache-2.0

// maintenance.go implements T-4007's node maintenance-mode suppression:
// marking (never removing) findings for a node currently inside a declared
// change.MaintenanceWindow.
//
// SUPPRESSION IS NOT REMOVAL — see Finding.Suppressed's own doc comment,
// the same invariant ack.go states for acknowledgement and quiethours.go
// states for a deferred delivery. Nothing here stops a check running, and
// nothing here removes a finding from the stream Engine.Findings returns:
// decorateMaintenance only ever ADDS the Suppressed/SuppressedWindow fields
// to a finding that is already there.
//
// EXPIRY IS EVALUATED AT READ TIME, never by a sweeper — the identical
// discipline ack.go documents for the identical reason. decorateMaintenance
// takes `now` as a parameter and is called fresh from Engine.Findings on
// EVERY call (both the RunLoop's own ticks and any on-demand GET /findings
// read), so a window that has ended stops suppressing on the very next
// evaluation with no background job and no manual "end maintenance" action.
//
// SCOPING IS EXACT. A window suppresses a finding only when one of the
// finding's OWN Nodes equals the window's Node — never by source, check, or
// cluster. A window declared for node A must never suppress a finding
// raised for node B, which is exactly the failure mode the task's own
// warning names.

package findings

import (
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
)

// MaintenanceProvider is the seam decorateMaintenance needs:
// change.Service.MaintenanceWindows, live over the current
// maintenance_windows table state. Context-free, matching
// BreakGlassProvider/ScheduleMissedProvider's shape (a findings cycle has
// no request context to thread through) — cmd/vnproxd's wiring adapts
// *change.Service accordingly.
type MaintenanceProvider interface {
	MaintenanceWindows() []change.MaintenanceWindow
}

// decorateMaintenance returns a copy of fs with Suppressed/SuppressedWindow
// set on every finding whose Nodes intersects a currently-active
// maintenance window's Node, evaluated at now. A nil provider or an empty/
// all-inactive window set returns fs unchanged (same slice, no copy) — the
// common case, kept cheap.
//
// When more than one active window covers the same node, the one ending
// SOONEST is attached: the more urgent "why", and a deterministic choice so
// two evaluations at the same instant never disagree (mirrors
// change.Service.MaintenanceState's identical tie-break).
func decorateMaintenance(fs []Finding, provider MaintenanceProvider, now time.Time) []Finding {
	if provider == nil {
		return fs
	}
	windows := provider.MaintenanceWindows()
	if len(windows) == 0 {
		return fs
	}

	active := make(map[string]change.MaintenanceWindow, len(windows))
	for _, w := range windows {
		if !w.Active(now) {
			continue
		}
		cur, ok := active[w.Node]
		if !ok || w.End < cur.End {
			active[w.Node] = w
		}
	}
	if len(active) == 0 {
		return fs
	}

	out := make([]Finding, len(fs))
	copy(out, fs)
	for i := range out {
		for _, node := range out[i].Nodes {
			w, ok := active[node]
			if !ok {
				continue
			}
			out[i].Suppressed = true
			out[i].SuppressedWindow = &Suppression{
				Node: w.Node, WindowID: w.ID, Reason: w.Reason, StartsAt: w.Start, EndsAt: w.End,
			}
			break
		}
	}
	return out
}
