package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Webhook is one row of the webhooks table (T-1104, docs/data-model.md new
// Webhooks section): a registered delivery target for the same envelope
// WS topic "events" carries. SecretEnc is AES-256-GCM ciphertext of the
// caller-supplied HMAC signing secret (internal/store/cipher.go's
// SessionCipher, the same cipher alert_rules.target_secret_enc uses) —
// decrypted only by cmd/vnproxd's composition-root adapter, never returned
// by any route. ConsecutiveFailures/LastAttemptAt/LastSuccessAt/LastError
// back the webhook_unhealthy finding (internal/findings' WebhookProvider
// seam recomputes it live from these columns each cycle).
type Webhook struct {
	ID                  string
	URL                 string
	CreatedBy           string
	EventsJSON          sql.NullString
	SecretEnc           []byte
	LastError           sql.NullString
	LastAttemptAt       sql.NullInt64
	LastSuccessAt       sql.NullInt64
	CreatedAt           int64
	ConsecutiveFailures int
}

// WebhookRepo is the webhooks table repository.
type WebhookRepo struct {
	db *DB
}

// NewWebhookRepo constructs a WebhookRepo.
func NewWebhookRepo(db *DB) *WebhookRepo { return &WebhookRepo{db: db} }

// Create inserts a new webhook registration.
func (r *WebhookRepo) Create(ctx context.Context, w Webhook) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO webhooks (id, url, events_json, secret_enc, created_by, created_at, consecutive_failures, last_attempt_at, last_success_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, 0, NULL, NULL, NULL)`,
		w.ID, w.URL, w.EventsJSON, w.SecretEnc, w.CreatedBy, w.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: creating webhook %s: %w", w.ID, err)
	}
	return nil
}

// Get returns the webhook with the given id, or ErrNotFound.
func (r *WebhookRepo) Get(ctx context.Context, id string) (Webhook, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, url, events_json, secret_enc, created_by, created_at, consecutive_failures, last_attempt_at, last_success_at, last_error
		FROM webhooks WHERE id = ?`, id,
	)
	w, err := scanWebhook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, ErrNotFound
	}
	return w, err
}

// List returns every registered webhook, newest-first.
func (r *WebhookRepo) List(ctx context.Context) ([]Webhook, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, url, events_json, secret_enc, created_by, created_at, consecutive_failures, last_attempt_at, last_success_at, last_error
		FROM webhooks ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing webhooks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing webhooks: %w", err)
	}
	return out, nil
}

// Delete removes a webhook registration. Not an error to delete an
// already-absent one (matches LayoutRepo.Delete's convention).
func (r *WebhookRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting webhook %s: %w", id, err)
	}
	return nil
}

// RecordSuccess resets id's consecutive-failure counter to 0 and stamps
// last_success_at/last_attempt_at — called after a delivery attempt that
// received a 2xx response. Returns ErrNotFound if id doesn't exist (e.g. it
// was deleted mid-delivery).
func (r *WebhookRepo) RecordSuccess(ctx context.Context, id string, now int64) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE webhooks SET consecutive_failures = 0, last_attempt_at = ?, last_success_at = ?, last_error = NULL WHERE id = ?`,
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("store: recording webhook %s success: %w", id, err)
	}
	return checkRowAffected(res, "store: recording webhook %s success", id)
}

// RecordFailure increments id's consecutive-failure counter and stamps
// last_attempt_at/last_error — called after a delivery attempt's retry
// sequence is exhausted. Returns the new consecutive-failure count so the
// caller (internal/automation's dispatcher) can log/trace it without a
// second round trip; ok is false if id doesn't exist.
func (r *WebhookRepo) RecordFailure(ctx context.Context, id string, now int64, errMsg string) (count int, err error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE webhooks SET consecutive_failures = consecutive_failures + 1, last_attempt_at = ?, last_error = ? WHERE id = ?`,
		now, errMsg, id,
	)
	if err != nil {
		return 0, fmt.Errorf("store: recording webhook %s failure: %w", id, err)
	}
	if rerr := checkRowAffected(res, "store: recording webhook %s failure", id); rerr != nil {
		return 0, rerr
	}
	w, err := r.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	return w.ConsecutiveFailures, nil
}

func scanWebhook(row rowScanner) (Webhook, error) {
	var w Webhook
	err := row.Scan(&w.ID, &w.URL, &w.EventsJSON, &w.SecretEnc, &w.CreatedBy, &w.CreatedAt,
		&w.ConsecutiveFailures, &w.LastAttemptAt, &w.LastSuccessAt, &w.LastError)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Webhook{}, err
		}
		return Webhook{}, fmt.Errorf("store: scanning webhook: %w", err)
	}
	return w, nil
}
