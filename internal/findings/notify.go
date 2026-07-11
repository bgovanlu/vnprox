// notify.go implements docs/features/monitoring.md §5's P1 notification
// hooks: "webhook/email via PVE notification system". AC5's contract is
// specific: a notification fires once per finding *transition* (new
// finding appears, or an existing finding's severity worsens into the
// notify threshold), never once per polling cycle just for a finding that's
// still sitting there unchanged — Engine tracks the last-notified severity
// per finding id (e.notified) precisely so a steady-state breach across N
// cycles produces exactly one notification, not N.

package findings

import (
	"context"
)

// TransitionKind names why a notification fired.
type TransitionKind string

const (
	// TransitionNew: a finding meeting the notify threshold appeared that
	// wasn't present (at any severity) last cycle.
	TransitionNew TransitionKind = "new"
	// TransitionEscalated: a finding that was already present (but below
	// threshold, or at a lower severity) crossed into/further into the
	// notify threshold.
	TransitionEscalated TransitionKind = "escalated"
	// TransitionResolved: a finding that had previously been notified about
	// is no longer present this cycle.
	TransitionResolved TransitionKind = "resolved"
)

// Notifier delivers one finding transition through whatever external
// channel it wraps (PVE's notification-target system, in production — see
// pvenotify.go). Notify errors are logged, never fatal to the findings
// cycle: a broken webhook must not stop the daemon from tracking findings.
type Notifier interface {
	Notify(ctx context.Context, f Finding, kind TransitionKind) error
}

// evaluateNotifications compares this cycle's findings against
// e.notified (the last severity at which each currently-or-previously
// -notified finding id fired) and calls e.notifier exactly once per
// transition, per the doc comment above. Called from cycle(), so it only
// ever runs on Engine's own RunLoop cadence (never from an on-demand
// Findings() call), keeping "once per transition" meaningful — an HTTP
// handler calling Findings() a hundred times a second for GET /findings
// must never itself trigger a notification.
func (e *Engine) evaluateNotifications(ctx context.Context, findings []Finding) {
	if e.notifier == nil {
		return
	}

	e.mu.Lock()
	prev := e.notified
	e.mu.Unlock()

	cur := make(map[string]Finding, len(findings))
	for _, f := range findings {
		cur[f.ID] = f
	}

	next := make(map[string]string, len(prev))
	for id, f := range cur {
		meets := severityAtLeast(f.Severity, e.notifyMin)
		prevSev, wasNotified := prev[id]

		switch {
		case !meets:
			// Below threshold: nothing to notify, and nothing to remember
			// (if it later crosses the threshold, that's a "new" transition,
			// not an "escalated" one — there's no prior notified severity).
		case !wasNotified:
			e.fireNotification(ctx, f, TransitionNew)
			next[id] = f.Severity
		case prevSev != f.Severity:
			e.fireNotification(ctx, f, TransitionEscalated)
			next[id] = f.Severity
		default:
			// Unchanged severity, already notified: carry state forward
			// without notifying again — the steady-state case AC5 requires.
			next[id] = prevSev
		}
	}

	// Anything notified last cycle but absent (or since resolved) now.
	for id, sev := range prev {
		if _, stillPresent := cur[id]; stillPresent {
			continue
		}
		resolvedFinding := Finding{ID: id, Severity: sev}
		e.fireNotification(ctx, resolvedFinding, TransitionResolved)
	}

	e.mu.Lock()
	e.notified = next
	e.mu.Unlock()
}

func (e *Engine) fireNotification(ctx context.Context, f Finding, kind TransitionKind) {
	if err := e.notifier.Notify(ctx, f, kind); err != nil {
		e.log.Warn("findings: notification hook failed", "finding_id", f.ID, "transition", string(kind), "error", err)
	}
}
