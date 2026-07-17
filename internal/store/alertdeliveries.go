// alertdeliveries.go implements T-1005's delivery log storage
// (docs/data-model.md §2, migration 0008_alert_rules.sql): one row per
// webhook delivery *attempt*, feeding the Settings UI's delivery log
// (GET /alert-deliveries) and internal/findings/webhook.go's own retry
// bookkeeping (it needs no in-memory retry state beyond what it writes
// here — a crash mid-retry simply stops the sequence, the same "no
// indefinite retry" bound the migration's doc comment describes).

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// AlertDelivery is one row of the alert_deliveries table: a single HTTP
// attempt within a rule+finding delivery sequence. Status is one of
// "retrying" (failed, another attempt is scheduled), "delivered" (this
// attempt succeeded — terminal), or "failed" (this attempt failed and was
// the last one — terminal).
type AlertDelivery struct {
	ID        string
	RuleID    string
	FindingID string
	Status    string
	Error     string
	At        int64
	Attempt   int
}

// AlertDeliveryRepo is the alert_deliveries table repository.
type AlertDeliveryRepo struct {
	db *DB
}

// NewAlertDeliveryRepo constructs an AlertDeliveryRepo.
func NewAlertDeliveryRepo(db *DB) *AlertDeliveryRepo { return &AlertDeliveryRepo{db: db} }

// Insert records one delivery attempt (ID is caller-assigned, typically
// store.NewULID()).
func (r *AlertDeliveryRepo) Insert(ctx context.Context, d AlertDelivery) error {
	var errCol sql.NullString
	if d.Error != "" {
		errCol = sql.NullString{String: d.Error, Valid: true}
	}
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO alert_deliveries (id, rule_id, finding_id, at, attempt, status, error)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.RuleID, d.FindingID, d.At, d.Attempt, d.Status, errCol,
	)
	if err != nil {
		return fmt.Errorf("store: inserting alert delivery %s: %w", d.ID, err)
	}
	return nil
}

// List returns delivery rows newest-first, optionally filtered by ruleID
// and/or status — both independently optional and ANDed (empty string
// matches every value for that filter), the same convention every other
// filtered list route in this codebase follows (e.g. GET /findings'
// ?source=&severity=).
func (r *AlertDeliveryRepo) List(ctx context.Context, ruleID, status string) ([]AlertDelivery, error) {
	var b strings.Builder
	b.WriteString(`SELECT id, rule_id, finding_id, at, attempt, status, error FROM alert_deliveries WHERE 1=1`)
	args := make([]any, 0, 2)
	if ruleID != "" {
		b.WriteString(` AND rule_id = ?`)
		args = append(args, ruleID)
	}
	if status != "" {
		b.WriteString(` AND status = ?`)
		args = append(args, status)
	}
	b.WriteString(` ORDER BY at DESC, id DESC`)

	rows, err := r.db.sqlDB.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing alert deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AlertDelivery
	for rows.Next() {
		d, err := scanAlertDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing alert deliveries: %w", err)
	}
	return out, nil
}

func scanAlertDelivery(row rowScanner) (AlertDelivery, error) {
	var d AlertDelivery
	var errCol sql.NullString
	if err := row.Scan(&d.ID, &d.RuleID, &d.FindingID, &d.At, &d.Attempt, &d.Status, &errCol); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AlertDelivery{}, err
		}
		return AlertDelivery{}, fmt.Errorf("store: scanning alert delivery: %w", err)
	}
	d.Error = errCol.String
	return d, nil
}
