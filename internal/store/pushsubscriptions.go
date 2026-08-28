// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PushSubscription is one row of the push_subscriptions table (T-2005,
// 0046_push_subscriptions.sql's doc comment): a browser's web-push
// registration, tied to the session that created it. EndpointEnc/
// P256dhEnc/AuthEnc are AES-256-GCM ciphertext (internal/store/cipher.go's
// SessionCipher, the same cipher webhooks.secret_enc/sessions.pve_ticket_enc
// already use) — decrypted only by cmd/vnproxd's composition-root adapter
// when a push is actually being sent, never returned by any route.
type PushSubscription struct {
	ID             string
	SessionID      string
	Username       string
	EndpointHash   string
	CategoriesJSON string
	DeviceLabel    string
	EndpointEnc    []byte
	P256dhEnc      []byte
	AuthEnc        []byte
	LastUsedAt     sql.NullInt64
	CreatedAt      int64
}

// PushSubscriptionRepo is the push_subscriptions table repository.
type PushSubscriptionRepo struct {
	db *DB
}

// NewPushSubscriptionRepo constructs a PushSubscriptionRepo.
func NewPushSubscriptionRepo(db *DB) *PushSubscriptionRepo { return &PushSubscriptionRepo{db: db} }

// Create inserts a new push subscription. Callers should check for an
// existing row with the same EndpointHash first (Get by hash) if they want
// resubscribe-to-same-endpoint to update in place rather than duplicate —
// internal/api/push.go's handler does this via Upsert below.
func (r *PushSubscriptionRepo) Create(ctx context.Context, s PushSubscription) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (id, session_id, username, endpoint_hash, endpoint_enc, p256dh_enc, auth_enc, categories_json, device_label, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		s.ID, s.SessionID, s.Username, s.EndpointHash, s.EndpointEnc, s.P256dhEnc, s.AuthEnc, s.CategoriesJSON, s.DeviceLabel, s.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: creating push subscription %s: %w", s.ID, err)
	}
	return nil
}

// GetByEndpointHash returns the subscription whose endpoint hashes to hash,
// or ErrNotFound. Used to detect "the browser resubscribed to the same push
// service endpoint" without ever decrypting an existing row.
func (r *PushSubscriptionRepo) GetByEndpointHash(ctx context.Context, hash string) (PushSubscription, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, session_id, username, endpoint_hash, endpoint_enc, p256dh_enc, auth_enc, categories_json, device_label, created_at, last_used_at
		FROM push_subscriptions WHERE endpoint_hash = ?`, hash,
	)
	s, err := scanPushSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PushSubscription{}, ErrNotFound
	}
	return s, err
}

// Get returns the subscription with the given id, or ErrNotFound.
func (r *PushSubscriptionRepo) Get(ctx context.Context, id string) (PushSubscription, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, session_id, username, endpoint_hash, endpoint_enc, p256dh_enc, auth_enc, categories_json, device_label, created_at, last_used_at
		FROM push_subscriptions WHERE id = ?`, id,
	)
	s, err := scanPushSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PushSubscription{}, ErrNotFound
	}
	return s, err
}

// ListByUsername returns every subscription created by username, newest
// first — GET /push/subscriptions' backing query, scoped to the caller's
// own devices the same way tokens.go's GET /tokens is scoped to the
// caller's own tokens.
func (r *PushSubscriptionRepo) ListByUsername(ctx context.Context, username string) ([]PushSubscription, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, username, endpoint_hash, endpoint_enc, p256dh_enc, auth_enc, categories_json, device_label, created_at, last_used_at
		FROM push_subscriptions WHERE username = ? ORDER BY created_at DESC`, username,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing push subscriptions for %s: %w", username, err)
	}
	defer func() { _ = rows.Close() }()

	var out []PushSubscription
	for rows.Next() {
		s, err := scanPushSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing push subscriptions for %s: %w", username, err)
	}
	return out, nil
}

// ListAll returns every live subscription, for the dispatcher's fan-out —
// filtering by opted-in category happens in internal/push, not here, so
// this repo stays a plain CRUD seam with no policy of its own.
func (r *PushSubscriptionRepo) ListAll(ctx context.Context) ([]PushSubscription, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, username, endpoint_hash, endpoint_enc, p256dh_enc, auth_enc, categories_json, device_label, created_at, last_used_at
		FROM push_subscriptions ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing push subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PushSubscription
	for rows.Next() {
		s, err := scanPushSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing push subscriptions: %w", err)
	}
	return out, nil
}

// Delete removes a push subscription. Not an error to delete an
// already-absent one (matches WebhookRepo.Delete's convention).
func (r *PushSubscriptionRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting push subscription %s: %w", id, err)
	}
	return nil
}

// DeleteByEndpointHash removes whatever subscription currently holds hash,
// if any — used by Upsert below and by a browser explicitly unsubscribing
// via the PushManager (which only ever knows its own endpoint, not this
// row's ULID).
func (r *PushSubscriptionRepo) DeleteByEndpointHash(ctx context.Context, hash string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint_hash = ?`, hash); err != nil {
		return fmt.Errorf("store: deleting push subscription by endpoint hash: %w", err)
	}
	return nil
}

// TouchLastUsed stamps last_used_at after a successful delivery attempt to
// id. Best-effort from the dispatcher's perspective — a failed stamp never
// blocks or fails the delivery itself.
func (r *PushSubscriptionRepo) TouchLastUsed(ctx context.Context, id string, now int64) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE push_subscriptions SET last_used_at = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("store: touching push subscription %s: %w", id, err)
	}
	return nil
}

func scanPushSubscription(row rowScanner) (PushSubscription, error) {
	var s PushSubscription
	err := row.Scan(&s.ID, &s.SessionID, &s.Username, &s.EndpointHash, &s.EndpointEnc, &s.P256dhEnc, &s.AuthEnc,
		&s.CategoriesJSON, &s.DeviceLabel, &s.CreatedAt, &s.LastUsedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PushSubscription{}, err
		}
		return PushSubscription{}, fmt.Errorf("store: scanning push subscription: %w", err)
	}
	return s, nil
}
