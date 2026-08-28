// SPDX-License-Identifier: Apache-2.0

// alertrules.go implements T-1005's alert-routing rule storage
// (docs/data-model.md §2, migration 0008_alert_rules.sql). An AlertRule
// routes findings/drift transitions to a webhook target (generic JSON,
// Gotify, ntfy, or Slack incoming-webhook) — see
// internal/findings/webhook.go for the delivery logic this table feeds.
// App-owned data per CLAUDE.md's storage rule: never a shadow copy of any
// PVE-authoritative config.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// AlertRule is one row of the alert_rules table. SourceFilter/
// SeverityFilter are nil/empty to mean "no filter on this dimension" (every
// finding matches), mirroring every other optional/ANDed filter contract in
// this codebase. TargetSecretEnc is AES-256-GCM ciphertext
// (nonce||ciphertext||tag, see cipher.go's SessionCipher) or nil when the
// target needs no secret; internal/api's alert-rules handlers own
// encrypting/decrypting it, this repository only stores/returns the opaque
// bytes.
type AlertRule struct {
	QuietStart       string
	Name             string
	TargetKind       string
	TargetURL        string
	QuietTZ          string
	QuietEnd         string
	ID               string
	TargetSecretEnc  []byte
	SeverityFilter   []string
	SourceFilter     []string
	UpdatedAt        int64
	CreatedAt        int64
	DigestWindowSec  int64
	Enabled          bool
	QuietBypassError bool
}

// DefaultQuietBypassError is the default for AlertRule.QuietBypassError,
// matching the column default in 0036_alert_quiet_hours.sql. Named rather
// than written as a bare `true` at each construction site so the default has
// exactly one place to be read from — and one place for a test to assert.
const DefaultQuietBypassError = true

// AlertRuleRepo is the alert_rules table repository.
type AlertRuleRepo struct {
	db *DB
}

// NewAlertRuleRepo constructs an AlertRuleRepo.
func NewAlertRuleRepo(db *DB) *AlertRuleRepo { return &AlertRuleRepo{db: db} }

// marshalFilter encodes a filter slice as JSON, storing NULL for an empty/
// nil slice (rather than the literal string "[]" or "null") so the SQL
// column stays NULL-means-"no filter" throughout, matching this table's own
// doc comment.
func marshalFilter(ss []string) (any, error) {
	if len(ss) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(ss)
	if err != nil {
		return nil, fmt.Errorf("store: marshaling filter: %w", err)
	}
	return string(data), nil
}

// nullIfEmpty stores an empty string as SQL NULL, so "unset" is one value in
// the column rather than two indistinguishable ones.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func unmarshalFilter(raw sql.NullString) ([]string, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return nil, fmt.Errorf("store: unmarshaling filter %q: %w", raw.String, err)
	}
	return out, nil
}

// Insert creates a new alert_rules row (ID is caller-assigned, typically
// store.NewULID()).
func (r *AlertRuleRepo) Insert(ctx context.Context, a AlertRule) error {
	sourceJSON, err := marshalFilter(a.SourceFilter)
	if err != nil {
		return err
	}
	severityJSON, err := marshalFilter(a.SeverityFilter)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO alert_rules
			(id, name, enabled, source_filter_json, severity_filter_json, target_kind, target_url, target_secret_enc, created_at, updated_at, quiet_start, quiet_end, quiet_tz, quiet_bypass_error, digest_window_sec)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Enabled, sourceJSON, severityJSON, a.TargetKind, a.TargetURL, a.TargetSecretEnc, a.CreatedAt, a.UpdatedAt,
		nullIfEmpty(a.QuietStart), nullIfEmpty(a.QuietEnd), nullIfEmpty(a.QuietTZ), a.QuietBypassError, a.DigestWindowSec,
	)
	if err != nil {
		return fmt.Errorf("store: inserting alert rule %s: %w", a.ID, err)
	}
	return nil
}

// Get returns one alert rule by id, or ErrNotFound.
func (r *AlertRuleRepo) Get(ctx context.Context, id string) (AlertRule, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, enabled, source_filter_json, severity_filter_json, target_kind, target_url, target_secret_enc, created_at, updated_at, quiet_start, quiet_end, quiet_tz, quiet_bypass_error, digest_window_sec
		FROM alert_rules WHERE id = ?`, id,
	)
	a, err := scanAlertRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	return a, err
}

// List returns every alert rule, ordered by name then id for a stable
// listing.
func (r *AlertRuleRepo) List(ctx context.Context) ([]AlertRule, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, enabled, source_filter_json, severity_filter_json, target_kind, target_url, target_secret_enc, created_at, updated_at, quiet_start, quiet_end, quiet_tz, quiet_bypass_error, digest_window_sec
		FROM alert_rules ORDER BY name ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing alert rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AlertRule
	for rows.Next() {
		a, err := scanAlertRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing alert rules: %w", err)
	}
	return out, nil
}

// Update overwrites every mutable column of an existing alert rule. It
// returns ErrNotFound if the rule doesn't exist.
func (r *AlertRuleRepo) Update(ctx context.Context, a AlertRule) error {
	sourceJSON, err := marshalFilter(a.SourceFilter)
	if err != nil {
		return err
	}
	severityJSON, err := marshalFilter(a.SeverityFilter)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE alert_rules SET
			name = ?, enabled = ?, source_filter_json = ?, severity_filter_json = ?,
			target_kind = ?, target_url = ?, target_secret_enc = ?, updated_at = ?,
			quiet_start = ?, quiet_end = ?, quiet_tz = ?, quiet_bypass_error = ?, digest_window_sec = ?
		WHERE id = ?`,
		a.Name, a.Enabled, sourceJSON, severityJSON, a.TargetKind, a.TargetURL, a.TargetSecretEnc, a.UpdatedAt,
		nullIfEmpty(a.QuietStart), nullIfEmpty(a.QuietEnd), nullIfEmpty(a.QuietTZ), a.QuietBypassError, a.DigestWindowSec,
		a.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating alert rule %s: %w", a.ID, err)
	}
	return checkRowAffected(res, "store: updating alert rule %s", a.ID)
}

// Delete removes an alert rule by id. It is not an error to delete an
// already-absent one (mirrors AnnotationRepo.Delete's convention).
func (r *AlertRuleRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting alert rule %s: %w", id, err)
	}
	return nil
}

func scanAlertRule(row rowScanner) (AlertRule, error) {
	var a AlertRule
	var sourceJSON, severityJSON, quietStart, quietEnd, quietTZ sql.NullString
	var enabled, bypass int
	if err := row.Scan(&a.ID, &a.Name, &enabled, &sourceJSON, &severityJSON, &a.TargetKind, &a.TargetURL, &a.TargetSecretEnc, &a.CreatedAt, &a.UpdatedAt,
		&quietStart, &quietEnd, &quietTZ, &bypass, &a.DigestWindowSec); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AlertRule{}, err
		}
		return AlertRule{}, fmt.Errorf("store: scanning alert rule: %w", err)
	}
	a.Enabled = enabled != 0
	a.QuietBypassError = bypass != 0
	a.QuietStart = quietStart.String
	a.QuietEnd = quietEnd.String
	a.QuietTZ = quietTZ.String
	var err error
	if a.SourceFilter, err = unmarshalFilter(sourceJSON); err != nil {
		return AlertRule{}, err
	}
	if a.SeverityFilter, err = unmarshalFilter(severityJSON); err != nil {
		return AlertRule{}, err
	}
	return a, nil
}
