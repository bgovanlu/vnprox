// SPDX-License-Identifier: Apache-2.0

// switch_snmp_targets.go implements T-4013's per-switch SNMP poll-config
// storage (docs/data-model.md, migration 0054_switch_snmp_targets.sql).
// App-owned intent + credentials only per CLAUDE.md's storage rule — a
// switch's live counters are never shadow-copied here, only the operator's
// opt-in and how to reach the switch. CommunityEnc is AES-256-GCM
// ciphertext (nonce||ciphertext||tag, cipher.go's SessionCipher); this
// repository stores/returns the opaque sealed bytes only — internal/api and
// cmd/vnproxd own sealing/unsealing, exactly like SwitchRepo does for
// credentials_enc. The plaintext community string is never returned by any
// API response, log line, or audit detail.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SwitchSNMPTarget is one row of the switch_snmp_targets table.
type SwitchSNMPTarget struct {
	ID            string
	ChassisID     string
	ChassisIDType string
	MgmtAddr      string
	AddedBy       string
	CommunityEnc  []byte
	AddedAt       int64
	Port          int
	Enabled       bool
}

// SwitchSNMPTargetRepo is the switch_snmp_targets-table repository.
type SwitchSNMPTargetRepo struct {
	db *DB
}

// NewSwitchSNMPTargetRepo constructs a SwitchSNMPTargetRepo.
func NewSwitchSNMPTargetRepo(db *DB) *SwitchSNMPTargetRepo { return &SwitchSNMPTargetRepo{db: db} }

// Insert creates a new switch_snmp_targets row. ID is caller-assigned
// (store.NewULID()).
func (r *SwitchSNMPTargetRepo) Insert(ctx context.Context, t SwitchSNMPTarget) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO switch_snmp_targets (id, chassis_id, chassis_id_type, mgmt_addr, port, community_enc, enabled, added_by, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.ChassisID, t.ChassisIDType, t.MgmtAddr, t.Port, t.CommunityEnc, boolToInt(t.Enabled), t.AddedBy, t.AddedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting switch SNMP target %s: %w", t.ID, err)
	}
	return nil
}

// Get returns one target by id, or ErrNotFound.
func (r *SwitchSNMPTargetRepo) Get(ctx context.Context, id string) (SwitchSNMPTarget, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, chassis_id, chassis_id_type, mgmt_addr, port, community_enc, enabled, added_by, added_at
		FROM switch_snmp_targets WHERE id = ?`, id)
	t, err := scanSwitchSNMPTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SwitchSNMPTarget{}, ErrNotFound
	}
	return t, err
}

// GetByChassisID returns one target by its LLDP ChassisID, or ErrNotFound —
// the lookup internal/ifcounters' TargetStore implementation uses.
func (r *SwitchSNMPTargetRepo) GetByChassisID(ctx context.Context, chassisID string) (SwitchSNMPTarget, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, chassis_id, chassis_id_type, mgmt_addr, port, community_enc, enabled, added_by, added_at
		FROM switch_snmp_targets WHERE chassis_id = ?`, chassisID)
	t, err := scanSwitchSNMPTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SwitchSNMPTarget{}, ErrNotFound
	}
	return t, err
}

// List returns every target, ordered by chassis_id for a stable listing.
func (r *SwitchSNMPTargetRepo) List(ctx context.Context) ([]SwitchSNMPTarget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, chassis_id, chassis_id_type, mgmt_addr, port, community_enc, enabled, added_by, added_at
		FROM switch_snmp_targets ORDER BY chassis_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing switch SNMP targets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SwitchSNMPTarget
	for rows.Next() {
		t, err := scanSwitchSNMPTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing switch SNMP targets: %w", err)
	}
	return out, nil
}

// ListEnabled returns every target with enabled=1 — internal/ifcounters
// polls exactly this set (further narrowed to whichever also have a live
// LLDP neighbor relationship this tick; see that package's doc.go).
func (r *SwitchSNMPTargetRepo) ListEnabled(ctx context.Context) ([]SwitchSNMPTarget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, chassis_id, chassis_id_type, mgmt_addr, port, community_enc, enabled, added_by, added_at
		FROM switch_snmp_targets WHERE enabled = 1 ORDER BY chassis_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing enabled switch SNMP targets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SwitchSNMPTarget
	for rows.Next() {
		t, err := scanSwitchSNMPTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing enabled switch SNMP targets: %w", err)
	}
	return out, nil
}

// Update overwrites a target's mutable fields. Returns ErrNotFound if
// absent.
func (r *SwitchSNMPTargetRepo) Update(ctx context.Context, t SwitchSNMPTarget) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE switch_snmp_targets SET chassis_id_type = ?, mgmt_addr = ?, port = ?, community_enc = ?, enabled = ?
		WHERE id = ?`,
		t.ChassisIDType, t.MgmtAddr, t.Port, t.CommunityEnc, boolToInt(t.Enabled), t.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating switch SNMP target %s: %w", t.ID, err)
	}
	return checkRowAffected(res, "store: updating switch SNMP target %s", t.ID)
}

// Delete removes a target by id. Not an error if already absent.
func (r *SwitchSNMPTargetRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM switch_snmp_targets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting switch SNMP target %s: %w", id, err)
	}
	return nil
}

// DeleteByChassisID removes a target by its LLDP ChassisID. Not an error if
// already absent.
func (r *SwitchSNMPTargetRepo) DeleteByChassisID(ctx context.Context, chassisID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM switch_snmp_targets WHERE chassis_id = ?`, chassisID); err != nil {
		return fmt.Errorf("store: deleting switch SNMP target for chassis %s: %w", chassisID, err)
	}
	return nil
}

func scanSwitchSNMPTarget(row rowScanner) (SwitchSNMPTarget, error) {
	var t SwitchSNMPTarget
	var enabled int
	if err := row.Scan(&t.ID, &t.ChassisID, &t.ChassisIDType, &t.MgmtAddr, &t.Port, &t.CommunityEnc, &enabled, &t.AddedBy, &t.AddedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SwitchSNMPTarget{}, err
		}
		return SwitchSNMPTarget{}, fmt.Errorf("store: scanning switch SNMP target: %w", err)
	}
	t.Enabled = enabled != 0
	return t, nil
}
