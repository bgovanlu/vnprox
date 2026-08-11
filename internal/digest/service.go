package digest

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// DefaultTickInterval is how often the runner asks "is a digest due".
//
// A minute is deliberately much finer than the cadence being waited on (a week,
// by default) and deliberately much coarser than a clock. It is what bounds how
// long a SCHEDULE CHANGE takes to be noticed: the schedule is re-read on every
// tick, so an operator who shortens the cadence sees it take effect within one
// tick, without a restart.
const DefaultTickInterval = time.Minute

// DefaultEvery is the cadence a schedule gets when none is set: weekly.
const DefaultEvery = 7 * 24 * time.Hour

// Config configures a Service. Store and Notifier are required; every source
// is optional and a nil one simply contributes nothing, which is this
// codebase's standard "nil dependency -> feature quietly absent" convention
// and is what lets a degraded daemon still send an honest, emptier digest
// rather than none at all.
type Config struct {
	Store    Store
	Posture  PostureSource
	Findings FindingsSource
	History  HistorySource
	Notifier Notifier
	Logger   *slog.Logger
	Now      func() time.Time
	// NewID assigns a delivered digest's synthetic finding id suffix when the
	// default (the period end) is not wanted. Unset uses the period end, which
	// is stable and reproducible — re-rendering the same digest produces the
	// same id, the same property internal/findings requires of every finding
	// id.
	NewID func() string
}

// Service generates and delivers scheduled digests.
type Service struct {
	store    Store
	posture  PostureSource
	findings FindingsSource
	history  HistorySource
	notifier Notifier
	log      *slog.Logger
	now      func() time.Time
	newID    func() string
}

// New builds a Service, applying the documented defaults for every unset
// field.
func New(cfg Config) *Service {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:    cfg.Store,
		posture:  cfg.Posture,
		findings: cfg.Findings,
		history:  cfg.History,
		notifier: cfg.Notifier,
		log:      logger,
		now:      now,
		newID:    cfg.NewID,
	}
}

// Tick evaluates the schedule once and, if a digest is due, generates and
// delivers one. It reports whether a digest was generated.
//
// THE SCHEDULE IS READ HERE, ON EVERY CALL. Not in New, not cached on the
// Service. That is the entire mechanism behind "schedule changes take effect
// without a restart" (T-2807 AC5): the next tick after an operator changes the
// cadence already uses the new one, against the same clock and the same
// running Service. Caching it would be a small, sensible-looking optimisation
// that silently costs the feature its acceptance criterion.
func (s *Service) Tick(ctx context.Context) (bool, error) {
	if s.store == nil {
		return false, nil
	}
	sched, ok, err := s.store.Schedule(ctx)
	if err != nil {
		return false, fmt.Errorf("digest: reading the digest schedule: %w", err)
	}
	if !ok || !sched.Enabled {
		return false, nil
	}
	if sched.Every <= 0 {
		sched.Every = DefaultEvery
	}

	prev, hasPrev, err := s.store.LatestRun(ctx)
	if err != nil {
		return false, fmt.Errorf("digest: reading the previous digest: %w", err)
	}

	now := s.now()
	var prevPtr *Run
	if hasPrev {
		prevPtr = &prev
		// Due when a full interval has elapsed since the last digest's own
		// window closed — measured from the previous digest rather than from
		// "when the daemon last started", so a restart cannot make digests
		// arrive early or drift later every week.
		if now.Unix() < prev.PeriodEnd+int64(sched.Every/time.Second) {
			return false, nil
		}
	}
	// With no previous digest the first one goes out on the first tick after
	// the schedule is enabled, rather than a week later. An operator who turns
	// this on wants to see what it looks like; making them wait a full cadence
	// to find out is how a feature gets turned off again before it has ever
	// run.

	return true, s.generate(ctx, now, sched, prevPtr)
}

// generate builds, delivers and records exactly one digest.
//
// The run is recorded whether or not delivery succeeded, and that is a
// decision rather than an oversight: the period WAS covered, and its window
// must not be reported twice because a webhook target happened to be down.
// The failure is recorded in the run's own status/detail, and separately —
// attempt by attempt — in alert_deliveries by the notifier itself.
func (s *Service) generate(ctx context.Context, now time.Time, sched Schedule, prev *Run) error {
	report := s.build(ctx, now, sched, prev)

	status, detail := StatusDelivered, s.deliveredDetail(report)
	deliverErr := s.deliver(ctx, report)
	if deliverErr != nil {
		status = StatusFailed
		detail = deliverErr.Error()
		s.log.Warn("digest: delivering the scheduled digest failed",
			"period", docexport.DigestPeriodLabel(report), "quiet", report.Quiet(), "error", deliverErr)
	}

	if err := s.store.RecordRun(ctx, runFrom(report, status, detail)); err != nil {
		// A digest that was delivered but not recorded would be re-sent, and
		// the next one would compute its delta against the wrong baseline.
		// Surfaced, joined with any delivery error so neither is lost.
		if deliverErr != nil {
			return fmt.Errorf("digest: recording the digest run after a failed delivery (%v): %w", deliverErr, err)
		}
		return fmt.Errorf("digest: recording the digest run: %w", err)
	}
	return deliverErr
}

func (s *Service) deliveredDetail(r docexport.DigestReport) string {
	if r.Quiet() {
		return "quiet period; nothing to report"
	}
	return fmt.Sprintf("%d opened, %d closed, %d unresolved drift, %d capacity projection(s)",
		len(r.Opened), len(r.Closed), len(r.Drift), len(r.Capacity))
}

// deliver hands the digest to T-2407's delivery path as an ordinary Finding.
//
// This one call is the whole of "delivery reuses T-2407's path" (AC3), and
// every property that follows is inherited rather than reimplemented here:
// quiet hours defer it, the digest window coalesces it, a failure retries with
// bounded backoff, and every attempt lands in alert_deliveries. Calling
// findings.Deliver directly instead would be a handful of lines shorter and
// would silently opt the digest out of all four.
func (s *Service) deliver(ctx context.Context, r docexport.DigestReport) error {
	if s.notifier == nil {
		return nil
	}
	if err := s.notifier.Notify(ctx, s.digestFinding(r), findings.TransitionNew); err != nil {
		return fmt.Errorf("digest: delivering the digest for %s: %w", docexport.DigestPeriodLabel(r), err)
	}
	return nil
}

// digestFinding wraps the rendered digest in the Finding shape T-2407's
// delivery path speaks.
//
// Reusing Finding rather than inventing a digest payload is what lets every
// target kind — generic, Gotify, ntfy, Slack — format a digest through exactly
// the code path it already formats an alert through; findings.DigestFinding
// makes the same choice for the same reason.
//
// Severity is INFO, always. A digest is a report, not an alarm: a weekly
// summary that pages as an error would be the loudest thing in the delivery
// log and the least urgent.
func (s *Service) digestFinding(r docexport.DigestReport) findings.Finding {
	id := "digest:" + strconv.FormatInt(r.PeriodEnd, 10)
	if s.newID != nil {
		id = "digest:" + s.newID()
	}
	return findings.Finding{
		// Obviously synthetic, and stable: re-rendering the same period
		// produces the same id. It is not a finding id — nothing can be acked
		// or fixed by it.
		ID:       id,
		Source:   findings.SourceHealth,
		Check:    CheckScheduledDigest,
		Severity: findings.SeverityInfo,
		Detail:   docexport.DigestMarkdown(r),
	}
}

// RunLoop drives Tick until ctx is cancelled — the run-group actor the daemon
// registers.
//
// It never returns a non-nil error for a failed pass: a run-group actor that
// returns takes the daemon down with it, and a webhook target that was
// unreachable on Tuesday is not a reason to stop serving the UI. Failures are
// logged and the loop continues, matching every other scheduled job in this
// codebase.
func (s *Service) RunLoop(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultTickInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// nil, not ctx.Err(): cancellation is how the daemon shuts down,
			// and runGroup reads a non-nil actor error as "the daemon failed".
			return nil
		case <-t.C:
			sent, err := s.Tick(ctx)
			if err != nil {
				s.log.Warn("digest: scheduled digest pass failed", "error", err)
				continue
			}
			if sent {
				s.log.Info("digest: scheduled digest generated")
			}
		}
	}
}
