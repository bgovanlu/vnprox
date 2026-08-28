// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/digest"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/posture"
	"github.com/bgovanlu/vnprox/internal/store"
)

// digest.go wires T-2807's scheduled digest reports.
//
// Every adapter here is a PROJECTION of a surface that already exists, not a
// second path to it: the posture score comes from the same read adapter GET
// /posture serves, the capacity forecasts and unresolved drift from the same
// findings engine GET /findings serves, and the opened/closed history from the
// same finding_events repo GET /history/events reads. A digest that could
// disagree with the page it summarises would be worse than no digest.
//
// Delivery is the same *findings.WebhookNotifier T-2407 built, constructed
// from the same setupAlertWebhookNotifier-shaped config and sharing the same
// deferral queue and the same delivery log. The ONE difference is its rule
// provider: digest.RecipientFilter narrows the fan-out to the schedule's
// recipient list. That is what makes recipients configurable without a second
// address book.
//
// NOTE ON THE FLUSH LOOP: the digest notifier deliberately does NOT get its
// own RunFlushLoop actor. Deferred deliveries live in one shared
// alert_pending table, and the alerting notifier's flush loop already drains
// it — a second loop over the same table would race the first for the same
// rows and double-deliver whatever it won.

// digestStoreAdapter adapts *store.DigestRepo into digest.Store.
type digestStoreAdapter struct {
	repo *store.DigestRepo
	id   string
}

func (a digestStoreAdapter) Schedule(ctx context.Context) (digest.Schedule, bool, error) {
	row, err := a.repo.Schedule(ctx, a.id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// No schedule row is the ordinary state of a daemon nobody has
			// configured a digest on — not a failure.
			return digest.Schedule{}, false, nil
		}
		return digest.Schedule{}, false, err
	}
	return digest.Schedule{
		Enabled: row.Enabled,
		Every:   time.Duration(row.EverySec) * time.Second,
		RuleIDs: row.RuleIDs,
	}, true, nil
}

func (a digestStoreAdapter) LatestRun(ctx context.Context) (digest.Run, bool, error) {
	row, err := a.repo.LatestRun(ctx, a.id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The no-baseline case: the digest package renders "no previous
			// digest to compare against" rather than a delta against zero.
			return digest.Run{}, false, nil
		}
		return digest.Run{}, false, err
	}
	return digest.Run{
		PeriodStart:    row.PeriodStart,
		PeriodEnd:      row.PeriodEnd,
		GeneratedAt:    row.GeneratedAt,
		PostureOverall: row.PostureOverall,
		Opened:         row.OpenedCount,
		Closed:         row.ClosedCount,
		Drift:          row.DriftCount,
		Capacity:       row.CapacityCount,
		Quiet:          row.Quiet,
		Status:         row.Status,
		Detail:         row.Detail,
	}, true, nil
}

func (a digestStoreAdapter) RecordRun(ctx context.Context, r digest.Run) error {
	return a.repo.RecordRun(ctx, store.DigestRun{
		ID:             store.NewULID(),
		ScheduleID:     a.id,
		PeriodStart:    r.PeriodStart,
		PeriodEnd:      r.PeriodEnd,
		GeneratedAt:    r.GeneratedAt,
		PostureOverall: r.PostureOverall,
		OpenedCount:    r.Opened,
		ClosedCount:    r.Closed,
		DriftCount:     r.Drift,
		CapacityCount:  r.Capacity,
		Quiet:          r.Quiet,
		Status:         r.Status,
		Detail:         r.Detail,
	})
}

// digestPostureAdapter narrows postureReadAdapter to the one method the digest
// needs. Declared rather than passing postureReadAdapter directly so
// internal/digest's seam stays a one-method interface it can be tested
// against.
type digestPostureAdapter struct {
	read postureReadAdapter
}

func (a digestPostureAdapter) Latest(ctx context.Context) (posture.Posture, bool, error) {
	return a.read.Latest(ctx)
}

// digestHistoryAdapter projects finding_events onto the transitions a digest
// reports as opened/closed in its period.
type digestHistoryAdapter struct {
	repo *store.FindingEventRepo
}

func (a digestHistoryAdapter) Transitions(ctx context.Context, from, to int64) ([]digest.Transition, error) {
	rows, err := a.repo.ListByTimeRange(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]digest.Transition, 0, len(rows))
	for _, r := range rows {
		out = append(out, digest.Transition{FindingID: r.FindingID, At: r.At, Transition: r.Transition})
	}
	return out, nil
}

// setupDigest wires the scheduled digest and returns its run-group actor.
//
// The actor is ALWAYS registered and always blocks until shutdown, even with
// no schedule configured: runGroup cancels every actor as soon as one returns,
// so an actor must either block for the daemon's lifetime or not exist. With
// no digest_schedules row, every tick is a cheap no-op read.
func setupDigest(
	db *store.DB,
	alertRules *store.AlertRuleRepo,
	alertDeliveries *store.AlertDeliveryRepo,
	alertPending *store.AlertPendingRepo,
	cipher *store.SessionCipher,
	postureRead postureReadAdapter,
	findingsEngine *findings.Engine,
	events *store.FindingEventRepo,
	logger *slog.Logger,
) func(context.Context) error {
	digestStore := digestStoreAdapter{repo: store.NewDigestRepo(db), id: store.DefaultDigestScheduleID}

	// T-2407's notifier, verbatim, over a recipient-narrowed view of the same
	// alert_rules. Same retry, same quiet hours, same alert_deliveries log.
	recorder := alertDeliveryRecorderAdapter{repo: alertDeliveries}
	notifier := findings.NewWebhookNotifier(findings.WebhookNotifierConfig{
		Rules: digest.RecipientFilter{
			Rules: alertRuleProviderAdapter{repo: alertRules, cipher: cipher, logger: logger},
			Store: digestStore,
		},
		Recorder: recorder,
		Scheduler: findings.NewScheduler(findings.SchedulerConfig{
			Store:    alertPendingStoreAdapter{repo: alertPending},
			Recorder: recorder,
			Logger:   logger,
		}),
		Logger: logger,
	})

	cfg := digest.Config{
		Store:    digestStore,
		Posture:  digestPostureAdapter{read: postureRead},
		Notifier: notifier,
		Logger:   logger,
	}
	if findingsEngine != nil {
		cfg.Findings = findingsEngine
	}
	if events != nil {
		cfg.History = digestHistoryAdapter{repo: events}
	}

	svc := digest.New(cfg)
	return func(ctx context.Context) error {
		return svc.RunLoop(ctx, digest.DefaultTickInterval)
	}
}
