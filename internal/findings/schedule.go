// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Deferral reasons recorded in the delivery log (AlertDelivery.Detail), so
// "we never got paged" is answerable from the log alone.
const (
	// StatusDeferred marks an event held rather than delivered. It is not a
	// failure and deliberately not StatusFailed: an operator scanning the log
	// for problems must not see quiet hours as one.
	StatusDeferred = "deferred"
)

// PendingDelivery is one held event.
//
// It carries the whole Finding as it fired, not a reference to a live one: a
// held event describes something that was true at a moment, and re-reading
// the finding at flush time would deliver a different — possibly resolved,
// possibly vanished — fact under the original event's timestamp.
type PendingDelivery struct {
	At      time.Time
	FlushAt time.Time
	Finding Finding
	ID      string
	RuleID  string
	Kind    TransitionKind
	Reason  string
}

// PendingStore is the durable deferral queue. cmd/vnproxd adapts
// *store.AlertPendingRepo into it, the same seam pattern AckStore and
// AlertRuleProvider use so this package never imports internal/store.
//
// IDs are assigned by the store, not here — the same "leaf package does not
// generate storage ids" split AlertDelivery already follows.
type PendingStore interface {
	AddPending(ctx context.Context, p PendingDelivery) error
	// PendingFlushAt reports the earliest flush time already queued for a
	// rule. It is what makes a digest window measure from the *first* event
	// in the window rather than restarting on every arrival — without it a
	// steadily flapping link would defer its digest forever.
	PendingFlushAt(ctx context.Context, ruleID string) (time.Time, bool, error)
	DuePending(ctx context.Context, now time.Time) ([]PendingDelivery, error)
	DeletePending(ctx context.Context, ids []string) error
}

// SchedulerConfig configures a Scheduler.
type SchedulerConfig struct {
	Store    PendingStore
	Recorder DeliveryRecorder
	Logger   *slog.Logger
}

// Scheduler decides whether an alert goes out now or waits, and coalesces
// what waited into one delivery.
//
// It holds no state of its own: everything lives in PendingStore, so a daemon
// restart inside an eight-hour quiet window does not lose the alerts that
// window was deferring. That is the whole reason the queue is a table.
type Scheduler struct {
	store    PendingStore
	recorder DeliveryRecorder
	log      *slog.Logger
}

// NewScheduler builds a Scheduler. A nil Store disables deferral entirely —
// every event is delivered immediately, which is exactly the pre-T-2407
// behaviour and the right thing to degrade to.
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{store: cfg.Store, recorder: cfg.Recorder, log: logger}
}

// Decide reports whether an event for rule should be delivered now, and if
// not, enqueues it and says when it will go out.
//
// The order of the three questions is the policy, and it is deliberate:
//
//  1. Quiet hours, unless the rule lets this severity through. A window an
//     operator set to protect their night wins over a coalescing window they
//     set to reduce noise.
//  2. The digest window, which applies to every severity — including the
//     bypassing one. A digest window is minutes; deferring an urgent alert by
//     minutes to send one message instead of a hundred is the point of the
//     feature. Quiet hours are hours, which is why they are not applied to
//     the bypassing severity.
//  3. Otherwise: now, exactly as before this feature existed.
func (s *Scheduler) Decide(ctx context.Context, rule AlertRule, f Finding, kind TransitionKind, now time.Time) (deliverNow bool, err error) {
	if s.store == nil {
		return true, nil
	}

	quiet := rule.QuietHours
	bypassing := rule.BypassQuietHoursOnError && f.Severity == SeverityError

	if !quiet.Zero() && !bypassing {
		inside, containsErr := quiet.Contains(now)
		if containsErr != nil {
			// A misconfigured window must not swallow alerts. Deliver.
			s.log.Warn("findings: quiet hours unusable, delivering immediately",
				"rule_id", rule.ID, "error", containsErr)
			return true, nil
		}
		if inside {
			end, endErr := quiet.NextEnd(now)
			if endErr != nil {
				s.log.Warn("findings: quiet hours end unresolvable, delivering immediately",
					"rule_id", rule.ID, "error", endErr)
				return true, nil
			}
			reason := fmt.Sprintf("quiet hours %s-%s; held until %s",
				quiet.Start, quiet.End, end.Format(time.RFC3339))
			return false, s.hold(ctx, rule, f, kind, now, end, reason)
		}
	}

	if rule.DigestWindow > 0 {
		flushAt := now.Add(rule.DigestWindow)
		// Join an open digest window rather than starting a new one, so the
		// window measures from its first event.
		if existing, ok, flushErr := s.store.PendingFlushAt(ctx, rule.ID); flushErr != nil {
			return false, fmt.Errorf("findings: reading rule %s's pending queue: %w", rule.ID, flushErr)
		} else if ok && existing.Before(flushAt) {
			flushAt = existing
		}
		reason := fmt.Sprintf("digest window %s; held until %s",
			rule.DigestWindow, flushAt.Format(time.RFC3339))
		return false, s.hold(ctx, rule, f, kind, now, flushAt, reason)
	}

	return true, nil
}

func (s *Scheduler) hold(ctx context.Context, rule AlertRule, f Finding, kind TransitionKind, now, flushAt time.Time, reason string) error {
	if err := s.store.AddPending(ctx, PendingDelivery{
		RuleID: rule.ID, Finding: f, Kind: kind,
		At: now, FlushAt: flushAt, Reason: reason,
	}); err != nil {
		return fmt.Errorf("findings: deferring alert for rule %s: %w", rule.ID, err)
	}
	s.recordDeferral(ctx, rule.ID, f.ID, reason)
	return nil
}

func (s *Scheduler) recordDeferral(ctx context.Context, ruleID, findingID, reason string) {
	if s.recorder == nil {
		return
	}
	d := AlertDelivery{
		RuleID: ruleID, FindingID: findingID, At: time.Now(),
		Attempt: 0, Status: StatusDeferred, Detail: reason,
	}
	if err := s.recorder.RecordDelivery(ctx, d); err != nil {
		s.log.Warn("findings: recording a deferred alert failed",
			"rule_id", ruleID, "finding_id", findingID, "error", err)
	}
}

// Batch is everything one rule held, ready to go out as a single delivery.
type Batch struct {
	RuleID string
	Events []PendingDelivery
	IDs    []string
}

// Due returns the batches whose hold has expired, one per rule, oldest event
// first within each. A rule with nothing due does not appear.
func (s *Scheduler) Due(ctx context.Context, now time.Time) ([]Batch, error) {
	if s.store == nil {
		return nil, nil
	}
	pending, err := s.store.DuePending(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("findings: reading due deferrals: %w", err)
	}
	byRule := map[string][]PendingDelivery{}
	for _, p := range pending {
		byRule[p.RuleID] = append(byRule[p.RuleID], p)
	}

	ruleIDs := make([]string, 0, len(byRule))
	for id := range byRule {
		ruleIDs = append(ruleIDs, id)
	}
	sort.Strings(ruleIDs)

	out := make([]Batch, 0, len(ruleIDs))
	for _, id := range ruleIDs {
		events := byRule[id]
		sort.Slice(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
		ids := make([]string, 0, len(events))
		for _, e := range events {
			ids = append(ids, e.ID)
		}
		out = append(out, Batch{RuleID: id, Events: events, IDs: ids})
	}
	return out, nil
}

// Clear removes a delivered batch from the queue.
//
// Called only after a delivery attempt has terminated, successfully or not.
// Clearing on success alone would retry the whole batch on the next tick
// forever against a permanently broken target; clearing regardless matches
// deliverWithRetry's own bounded-attempts contract, where a failure after the
// last attempt is terminal.
func (s *Scheduler) Clear(ctx context.Context, b Batch) error {
	if s.store == nil || len(b.IDs) == 0 {
		return nil
	}
	if err := s.store.DeletePending(ctx, b.IDs); err != nil {
		return fmt.Errorf("findings: clearing rule %s's delivered deferrals: %w", b.RuleID, err)
	}
	return nil
}

// DigestFinding collapses a batch into the single Finding that will be
// delivered in its place.
//
// Reusing Finding rather than inventing a digest payload type is what lets
// every target kind — generic, Gotify, ntfy, Slack — format a digest through
// exactly the code path it already formats a single alert through. A separate
// type would mean four new formatters and four new ways to be wrong.
//
// A one-event batch returns that event's finding untouched: a "digest of 1"
// that looked different from an ordinary alert would make the digest window
// visible to anyone who set one, for no benefit.
func DigestFinding(b Batch) (Finding, TransitionKind) {
	if len(b.Events) == 1 {
		return b.Events[0].Finding, b.Events[0].Kind
	}

	bySeverity := map[string]int{}
	bySource := map[Source]int{}
	nodes := make([]string, 0, len(b.Events))
	worst := SeverityInfo
	for _, e := range b.Events {
		bySeverity[e.Finding.Severity]++
		bySource[e.Finding.Source]++
		nodes = append(nodes, e.Finding.Nodes...)
		if severityRank[e.Finding.Severity] > severityRank[worst] {
			worst = e.Finding.Severity
		}
	}

	first, last := b.Events[0].At, b.Events[len(b.Events)-1].At
	detail := fmt.Sprintf("%d alerts in %s: %s. Sources: %s.",
		len(b.Events),
		last.Sub(first).Round(time.Second),
		countsBySeverity(bySeverity),
		countsBySource(bySource))

	return Finding{
		// A stable, obviously-synthetic id. It is not a finding id and must
		// never be mistaken for one — nothing can be acked or fixed by it.
		ID:       "digest:" + b.RuleID + ":" + strconv.FormatInt(first.Unix(), 10),
		Source:   SourceHealth,
		Check:    "alert_digest",
		Severity: worst,
		Detail:   detail,
		Nodes:    sortedUnique(nodes),
	}, TransitionNew
}

// countsBySeverity renders "2 error, 8 warning" worst-first, so the number
// that decides whether to get out of bed is the first thing read.
func countsBySeverity(counts map[string]int) string {
	order := []string{SeverityError, SeverityWarning, SeverityInfo}
	parts := make([]string, 0, len(order))
	for _, sev := range order {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func countsBySource(counts map[Source]int) string {
	sources := make([]string, 0, len(counts))
	for src := range counts {
		sources = append(sources, string(src))
	}
	sort.Strings(sources)
	parts := make([]string, 0, len(sources))
	for _, src := range sources {
		parts = append(parts, fmt.Sprintf("%s (%d)", src, counts[Source(src)]))
	}
	return strings.Join(parts, ", ")
}

// encodePendingFinding / decodePendingFinding are the store adapter's shared
// encoding, kept here so the two halves cannot drift apart.
func encodePendingFinding(f Finding) (string, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("findings: encoding a deferred finding: %w", err)
	}
	return string(raw), nil
}

func decodePendingFinding(raw string) (Finding, error) {
	var f Finding
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return Finding{}, fmt.Errorf("findings: decoding a deferred finding: %w", err)
	}
	return f, nil
}

// EncodePendingFinding and DecodePendingFinding expose the encoding to the
// composition root's store adapter without exporting the whole scheduler's
// internals.
func EncodePendingFinding(f Finding) (string, error) { return encodePendingFinding(f) }
func DecodePendingFinding(s string) (Finding, error) { return decodePendingFinding(s) }
