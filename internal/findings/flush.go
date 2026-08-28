// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DefaultFlushInterval is how often the daemon looks for deferrals whose hold
// has expired.
//
// Thirty seconds is deliberately coarse. The two things being waited on are a
// digest window measured in minutes and a quiet-hours window measured in
// hours; resolving either to the second would buy nothing and cost a wakeup
// every second forever.
const DefaultFlushInterval = 30 * time.Second

// Flush delivers every deferral whose hold has expired, one delivery per
// rule, and clears them from the queue.
//
// It returns the number of *deliveries* made, not the number of events
// flushed — ten coalesced events are one delivery, and that difference is the
// whole feature.
func (w *WebhookNotifier) Flush(ctx context.Context) (int, error) {
	if w.scheduler == nil || w.rules == nil {
		return 0, nil
	}

	batches, err := w.scheduler.Due(ctx, w.now())
	if err != nil {
		return 0, err
	}
	if len(batches) == 0 {
		return 0, nil
	}

	rules, err := w.rules.AlertRules(ctx)
	if err != nil {
		return 0, fmt.Errorf("findings: listing alert rules to flush deferrals: %w", err)
	}
	byID := make(map[string]AlertRule, len(rules))
	for _, rule := range rules {
		byID[rule.ID] = rule
	}

	delivered := 0
	for _, batch := range batches {
		rule, ok := byID[batch.RuleID]
		if !ok || !rule.Enabled {
			// The rule was deleted or disabled while its events were held.
			// Clear them: retrying forever against a rule that no longer
			// exists would make this table grow without bound, and delivering
			// to a disabled rule would ignore the operator's most recent
			// instruction.
			w.log.Info("findings: discarding deferred alerts for a rule that is gone or disabled",
				"rule_id", batch.RuleID, "events", len(batch.Events))
			w.clear(ctx, batch)
			continue
		}

		f, kind := DigestFinding(batch)
		detail := fmt.Sprintf("coalesced %d event(s) held since %s",
			len(batch.Events), batch.Events[0].At.Format(time.RFC3339))
		if err := w.deliverWithRetryDetail(ctx, rule, f, kind, detail); err != nil {
			w.log.Warn("findings: deferred alert delivery failed after retries",
				"rule_id", rule.ID, "rule_name", rule.Name, "events", len(batch.Events), "error", err)
		}
		delivered++

		// Cleared whether or not delivery succeeded — see Scheduler.Clear.
		w.clear(ctx, batch)
	}
	return delivered, nil
}

func (w *WebhookNotifier) clear(ctx context.Context, batch Batch) {
	if err := w.scheduler.Clear(ctx, batch); err != nil {
		// Not fatal, but it does mean the batch will be re-delivered on the
		// next tick, so it is a warning rather than a debug line.
		w.log.Warn("findings: clearing delivered deferrals failed; they may be delivered again",
			"rule_id", batch.RuleID, "error", err)
	}
}

// RunFlushLoop is the daemon actor that drives Flush until ctx is cancelled.
//
// It flushes once on entry, before the first tick. A daemon that has just
// restarted inside a quiet window may be holding events whose hold expired
// while it was down, and making them wait another interval would compound the
// outage that already delayed them.
func (w *WebhookNotifier) RunFlushLoop(ctx context.Context, interval time.Duration, log *slog.Logger) error {
	if log == nil {
		log = w.log
	}
	if w.scheduler == nil {
		// Nothing can ever be deferred, so there is nothing to flush — but
		// this is a run-group actor, and an actor that returns cancels every
		// other actor and takes the daemon down with it. T-2401 shipped that
		// exact bug. Block until shutdown instead.
		<-ctx.Done()
		return nil
	}
	if interval <= 0 {
		interval = DefaultFlushInterval
	}

	flush := func() {
		n, err := w.Flush(ctx)
		if err != nil {
			log.Warn("findings: flushing deferred alerts failed", "error", err)
			return
		}
		if n > 0 {
			log.Info("findings: delivered deferred alerts", "deliveries", n)
		}
	}

	flush()

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// nil, not ctx.Err(): a cancelled context is how the daemon
			// shuts down, and runGroup surfaces a non-nil actor error as
			// runDaemon's return value — which cmd/vnproxd's own tests read
			// as "the daemon failed". Every other actor in this codebase
			// returns nil here for the same reason.
			return nil
		case <-t.C:
			flush()
		}
	}
}
