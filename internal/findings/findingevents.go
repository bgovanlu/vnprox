// SPDX-License-Identifier: Apache-2.0

// findingevents.go implements T-1007's "History playback" event-marker
// feed: a Notifier (see notify.go's interface) that records every finding
// transition Engine already detects (evaluateNotifications/
// fireNotification) into the finding_events table
// (internal/store/migrations/0009_finding_events.sql) — for
// web/src/topology/history/HistoryTimeline.tsx's event markers, merged with
// changeset-lifecycle audit rows by GET /history/events
// (internal/api/history.go). Like webhook.go's WebhookNotifier, this is
// just another implementation of the existing Notifier interface, composed
// alongside PVENotifier/WebhookNotifier at the cmd/vnproxd composition root
// (multiNotifier) — it does not touch Engine's own notification-firing/
// once-per-transition logic in engine.go/notify.go at all, and it reuses
// that exact transition detection rather than re-deriving one from polled
// Findings() snapshots (which would race Engine's own bookkeeping and risk
// double-counting a transition).

package findings

import (
	"context"
	"log/slog"
	"time"
)

// FindingEventRecorder is the storage seam FindingEventsNotifier writes
// through — internal/store.FindingEventRepo satisfies this directly (the
// same "small interface, real type satisfies it for free" shape
// AlertRuleStore/AlertDeliveryStore establish in cmd/vnproxd/findings.go),
// declared here so internal/findings never imports internal/store (the
// same layering webhook.go's own doc comment documents for
// AlertRuleProvider/DeliveryRecorder).
type FindingEventRecorder interface {
	RecordFindingEvent(ctx context.Context, findingID string, at int64, transition string) error
}

// FindingEventsNotifier is a Notifier that records one finding_events row
// per transition Engine fires through it.
type FindingEventsNotifier struct {
	recorder FindingEventRecorder
	log      *slog.Logger
	now      func() time.Time
}

// NewFindingEventsNotifier builds a FindingEventsNotifier over recorder.
// log defaults to slog.Default() when nil, mirroring NewPVENotifier's/
// NewWebhookNotifier's own nil-logger convention.
func NewFindingEventsNotifier(recorder FindingEventRecorder, log *slog.Logger) *FindingEventsNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &FindingEventsNotifier{recorder: recorder, log: log, now: time.Now}
}

var _ Notifier = (*FindingEventsNotifier)(nil)

// Notify implements Notifier: records one finding_events row for this
// transition. A recording failure is logged (matching every other
// Notifier's own "a broken delivery path never stops the findings cycle"
// contract, per notify.go's package doc comment) but is also returned to
// the caller so cmd/vnproxd's multiNotifier can still surface the first
// error of a cycle through Engine.fireNotification's own log line.
func (n *FindingEventsNotifier) Notify(ctx context.Context, f Finding, kind TransitionKind) error {
	if n.recorder == nil {
		return nil
	}
	at := n.now().Unix()
	if err := n.recorder.RecordFindingEvent(ctx, f.ID, at, string(kind)); err != nil {
		n.log.Warn("findings: recording finding event", "finding_id", f.ID, "transition", string(kind), "error", err)
		return err
	}
	return nil
}
