// externalsubnets.go implements T-1203's external_subnets table
// (docs/data-model.md §2, migration 0023_external_subnets.sql). App-owned
// intent only per CLAUDE.md's storage rule: IP space vnprox tracks that
// Proxmox has no knowledge of (a physical LAN, an upstream transit range, a
// NetBox/phpIPAM-sourced prefix) — never a shadow copy of a PVE SDN subnet,
// which stays authoritative in PVE and is read live through internal/ipam.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// External-subnet provenance values (the external_subnets.source column). A
// row's provenance is distinct from GET /ipam/subnets' row-level source enum,
// whose external rows all render as "external" regardless of provenance.
const (
	ExternalSubnetSourceManual  = "manual"
	ExternalSubnetSourceNetbox  = "netbox"
	ExternalSubnetSourcePhpIPAM = "phpipam"
)

// ExternalSubnet is one row of the external_subnets table.
type ExternalSubnet struct {
	ID          string
	CIDR        string
	Label       string
	Source      string
	Description string
	CreatedBy   string
	CreatedAt   int64
	UpdatedAt   int64
}

// ExternalSubnetRepo is the external_subnets table repository.
type ExternalSubnetRepo struct {
	db *DB
}

// NewExternalSubnetRepo constructs an ExternalSubnetRepo.
func NewExternalSubnetRepo(db *DB) *ExternalSubnetRepo { return &ExternalSubnetRepo{db: db} }

// Insert creates a new external_subnets row (ID caller-assigned, typically
// store.NewULID()). A duplicate CIDR violates the unique index and returns a
// wrapped error the service maps to a validation failure.
func (r *ExternalSubnetRepo) Insert(ctx context.Context, e ExternalSubnet) error {
	if e.Source == "" {
		e.Source = ExternalSubnetSourceManual
	}
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO external_subnets (id, cidr, label, source, description, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.CIDR, e.Label, e.Source, e.Description, e.CreatedBy, e.CreatedAt, e.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting external subnet %s: %w", e.CIDR, err)
	}
	return nil
}

// Get returns one external subnet by id, or ErrNotFound.
func (r *ExternalSubnetRepo) Get(ctx context.Context, id string) (ExternalSubnet, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, cidr, label, source, description, created_by, created_at, updated_at
		FROM external_subnets WHERE id = ?`, id)
	e, err := scanExternalSubnet(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubnet{}, ErrNotFound
	}
	return e, err
}

// List returns every external subnet, ordered by cidr for a stable listing.
func (r *ExternalSubnetRepo) List(ctx context.Context) ([]ExternalSubnet, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, cidr, label, source, description, created_by, created_at, updated_at
		FROM external_subnets ORDER BY cidr ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing external subnets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ExternalSubnet
	for rows.Next() {
		e, err := scanExternalSubnet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing external subnets: %w", err)
	}
	return out, nil
}

// Update rewrites an external subnet's mutable fields (cidr, label, source,
// description, updated_at). Returns ErrNotFound if the row doesn't exist.
func (r *ExternalSubnetRepo) Update(ctx context.Context, e ExternalSubnet) error {
	if e.Source == "" {
		e.Source = ExternalSubnetSourceManual
	}
	res, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE external_subnets SET cidr = ?, label = ?, source = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		e.CIDR, e.Label, e.Source, e.Description, e.UpdatedAt, e.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating external subnet %s: %w", e.ID, err)
	}
	return checkRowAffected(res, "store: updating external subnet %s", e.ID)
}

// Delete removes an external subnet by id. Not an error to delete an
// already-absent one (mirrors ClusterRepo/K8sClusterRepo.Delete's convention).
func (r *ExternalSubnetRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM external_subnets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting external subnet %s: %w", id, err)
	}
	return nil
}

func scanExternalSubnet(row rowScanner) (ExternalSubnet, error) {
	var e ExternalSubnet
	if err := row.Scan(&e.ID, &e.CIDR, &e.Label, &e.Source, &e.Description, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExternalSubnet{}, err
		}
		return ExternalSubnet{}, fmt.Errorf("store: scanning external subnet: %w", err)
	}
	return e, nil
}
