// ingress.go implements T-1406's ingress_targets storage (docs/data-model.md
// §2, migration 0017_ingress_targets.sql). App-owned intent only per
// CLAUDE.md's storage rule: which reverse-proxy targets to poll, and how to
// authenticate to them — never a snapshot of any target's own live
// discovered state. CredentialEnc is AES-256-GCM ciphertext (nonce||
// ciphertext||tag, see cipher.go's SessionCipher) — this repository
// stores/returns the opaque sealed bytes only; internal/api's ingress
// handlers own sealing/unsealing, exactly like AlertRuleRepo does for
// target_secret_enc and WireGuardRepo does for private_key_enc.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// IngressTarget is one row of the ingress_targets table.
type IngressTarget struct {
	ID            string
	Kind          string
	Address       string
	AddedBy       string
	CredentialEnc []byte
	AddedAt       int64
}

// IngressTargetRepo is the ingress_targets table repository.
type IngressTargetRepo struct {
	db *DB
}

// NewIngressTargetRepo constructs an IngressTargetRepo.
func NewIngressTargetRepo(db *DB) *IngressTargetRepo { return &IngressTargetRepo{db: db} }

// Insert creates a new ingress_targets row (ID is caller-assigned,
// typically store.NewULID()).
func (r *IngressTargetRepo) Insert(ctx context.Context, t IngressTarget) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO ingress_targets (id, kind, address, credential_enc, added_by, added_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.Kind, t.Address, t.CredentialEnc, t.AddedBy, t.AddedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting ingress target %s: %w", t.ID, err)
	}
	return nil
}

// Get returns one target by id, or ErrNotFound.
func (r *IngressTargetRepo) Get(ctx context.Context, id string) (IngressTarget, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, kind, address, credential_enc, added_by, added_at
		FROM ingress_targets WHERE id = ?`, id)
	t, err := scanIngressTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return IngressTarget{}, ErrNotFound
	}
	return t, err
}

// List returns every ingress target, ordered by added_at then id for a
// stable listing.
func (r *IngressTargetRepo) List(ctx context.Context) ([]IngressTarget, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, kind, address, credential_enc, added_by, added_at
		FROM ingress_targets ORDER BY added_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing ingress targets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []IngressTarget
	for rows.Next() {
		t, err := scanIngressTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing ingress targets: %w", err)
	}
	return out, nil
}

// Delete removes an ingress target by id. It is not an error to delete an
// already-absent one (mirrors AlertRuleRepo.Delete's convention).
func (r *IngressTargetRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM ingress_targets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting ingress target %s: %w", id, err)
	}
	return nil
}

func scanIngressTarget(row rowScanner) (IngressTarget, error) {
	var t IngressTarget
	if err := row.Scan(&t.ID, &t.Kind, &t.Address, &t.CredentialEnc, &t.AddedBy, &t.AddedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IngressTarget{}, err
		}
		return IngressTarget{}, fmt.Errorf("store: scanning ingress target: %w", err)
	}
	return t, nil
}
