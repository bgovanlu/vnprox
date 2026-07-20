// switches.go implements T-1205's switch registry storage (docs/data-model.md,
// migration 0022_switches.sql). App-owned intent + credentials only per
// CLAUDE.md's storage rule — a switch's live port/VLAN/LACP state is never
// shadow-copied here. CredentialsEnc is AES-256-GCM ciphertext
// (nonce||ciphertext||tag, cipher.go's SessionCipher); this repository
// stores/returns the opaque sealed bytes only — internal/api and cmd/vnproxd
// own sealing/unsealing, exactly like AlertRuleRepo does for target_secret_enc.
// The plaintext credentials are never returned by any API response, log line,
// or audit detail (docs/security.md's switch credential-storage note).

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Switch is one row of the switches table.
type Switch struct {
	ID             string
	Name           string
	MgmtAddr       string
	DriverType     string
	CredentialsEnc []byte
	AddedBy        string
	AddedAt        int64
	Enabled        bool
}

// SwitchRepo is the switches-table repository.
type SwitchRepo struct {
	db *DB
}

// NewSwitchRepo constructs a SwitchRepo.
func NewSwitchRepo(db *DB) *SwitchRepo { return &SwitchRepo{db: db} }

// Insert creates a new switches row. ID is caller-assigned (store.NewULID()).
func (r *SwitchRepo) Insert(ctx context.Context, s Switch) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO switches (id, name, mgmt_addr, driver_type, credentials_enc, enabled, added_by, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.MgmtAddr, s.DriverType, s.CredentialsEnc, boolToInt(s.Enabled), s.AddedBy, s.AddedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting switch %s: %w", s.ID, err)
	}
	return nil
}

// Get returns one switch by id, or ErrNotFound.
func (r *SwitchRepo) Get(ctx context.Context, id string) (Switch, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, name, mgmt_addr, driver_type, credentials_enc, enabled, added_by, added_at
		FROM switches WHERE id = ?`, id)
	s, err := scanSwitch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Switch{}, ErrNotFound
	}
	return s, err
}

// List returns every switch, ordered by name for a stable listing.
func (r *SwitchRepo) List(ctx context.Context) ([]Switch, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, name, mgmt_addr, driver_type, credentials_enc, enabled, added_by, added_at
		FROM switches ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing switches: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Switch
	for rows.Next() {
		s, err := scanSwitch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing switches: %w", err)
	}
	return out, nil
}

// Update overwrites a switch's mutable fields. Returns ErrNotFound if absent.
func (r *SwitchRepo) Update(ctx context.Context, s Switch) error {
	res, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE switches SET name = ?, mgmt_addr = ?, driver_type = ?, credentials_enc = ?, enabled = ?
		WHERE id = ?`,
		s.Name, s.MgmtAddr, s.DriverType, s.CredentialsEnc, boolToInt(s.Enabled), s.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating switch %s: %w", s.ID, err)
	}
	return checkRowAffected(res, "store: updating switch %s", s.ID)
}

// Delete removes a switch by id. Not an error if already absent.
func (r *SwitchRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM switches WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting switch %s: %w", id, err)
	}
	return nil
}

func scanSwitch(row rowScanner) (Switch, error) {
	var s Switch
	var enabled int
	if err := row.Scan(&s.ID, &s.Name, &s.MgmtAddr, &s.DriverType, &s.CredentialsEnc, &enabled, &s.AddedBy, &s.AddedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Switch{}, err
		}
		return Switch{}, fmt.Errorf("store: scanning switch: %w", err)
	}
	s.Enabled = enabled != 0
	return s, nil
}
