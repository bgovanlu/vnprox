// SPDX-License-Identifier: Apache-2.0

// alertpending.go implements T-2407's durable deferral queue: the alerts an
// alert rule is holding because of quiet hours or a digest window, and which
// must survive a daemon restart.
//
// Durability is the whole point of the table. A quiet-hours window is
// routinely eight hours long; an in-memory queue would turn any restart
// inside one — a package upgrade, an OOM, a reboot — into silently dropped
// alerts, which is precisely what "deferred, not dropped" promises will not
// happen. See 0036_alert_quiet_hours.sql.

package store

import (
	"context"
	"fmt"
	"strings"
)

// AlertPending is one held alert event. FindingJSON is the whole finding as
// it fired, not a reference to a live one — see the migration's note.
type AlertPending struct {
	ID          string
	RuleID      string
	FindingID   string
	FindingJSON string
	Kind        string
	Reason      string
	At          int64
	FlushAt     int64
}

// AlertPendingRepo is the alert_pending table repository.
type AlertPendingRepo struct {
	db *DB
}

// NewAlertPendingRepo constructs an AlertPendingRepo.
func NewAlertPendingRepo(db *DB) *AlertPendingRepo { return &AlertPendingRepo{db: db} }

// Insert queues one held event. The id is assigned here rather than by the
// caller, matching this table's leaf-package seam (internal/findings never
// generates storage ids).
func (r *AlertPendingRepo) Insert(ctx context.Context, p AlertPending) (string, error) {
	id := p.ID
	if id == "" {
		id = NewULID()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO alert_pending (id, rule_id, finding_id, finding_json, kind, at, flush_at, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.RuleID, p.FindingID, p.FindingJSON, p.Kind, p.At, p.FlushAt, p.Reason,
	)
	if err != nil {
		return "", fmt.Errorf("store: queueing deferred alert for rule %s: %w", p.RuleID, err)
	}
	return id, nil
}

// EarliestFlushAt reports the soonest flush time already queued for a rule.
//
// It is what makes a digest window measure from its first event: without it,
// every new event would push the window out, and a steadily flapping link
// would defer its digest indefinitely — turning a noise-reduction feature
// into an alert-suppression one.
func (r *AlertPendingRepo) EarliestFlushAt(ctx context.Context, ruleID string) (int64, bool, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT flush_at FROM alert_pending WHERE rule_id = ? ORDER BY flush_at ASC LIMIT 1`, ruleID)
	if err != nil {
		return 0, false, fmt.Errorf("store: reading rule %s's pending queue: %w", ruleID, err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, false, fmt.Errorf("store: reading rule %s's pending queue: %w", ruleID, err)
		}
		return 0, false, nil
	}
	var at int64
	if err := rows.Scan(&at); err != nil {
		return 0, false, fmt.Errorf("store: scanning rule %s's pending queue: %w", ruleID, err)
	}
	return at, true, nil
}

// Due returns every held event whose flush time has arrived, oldest first.
func (r *AlertPendingRepo) Due(ctx context.Context, now int64) ([]AlertPending, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, rule_id, finding_id, finding_json, kind, at, flush_at, reason
		FROM alert_pending WHERE flush_at <= ? ORDER BY at ASC, id ASC`, now)
	if err != nil {
		return nil, fmt.Errorf("store: listing due deferred alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AlertPending
	for rows.Next() {
		var p AlertPending
		if err := rows.Scan(&p.ID, &p.RuleID, &p.FindingID, &p.FindingJSON, &p.Kind, &p.At, &p.FlushAt, &p.Reason); err != nil {
			return nil, fmt.Errorf("store: scanning a due deferred alert: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing due deferred alerts: %w", err)
	}
	return out, nil
}

// DeleteByIDs removes delivered events from the queue. An empty slice is a
// no-op rather than a query with an empty IN list, which SQLite accepts and
// which would silently match nothing — harmless here, but the kind of thing
// that reads as a bug later.
func (r *AlertPendingRepo) DeleteByIDs(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM alert_pending WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("store: clearing %d delivered deferred alert(s): %w", len(ids), err)
	}
	return nil
}

// DeleteByRule removes every event held for a rule. Called when the rule
// itself is deleted, so its queue does not outlive it.
func (r *AlertPendingRepo) DeleteByRule(ctx context.Context, ruleID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM alert_pending WHERE rule_id = ?`, ruleID); err != nil {
		return fmt.Errorf("store: clearing rule %s's deferred alerts: %w", ruleID, err)
	}
	return nil
}
