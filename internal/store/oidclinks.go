// oidclinks.go implements T-1207's oidc_pve_links table (migration
// 0024_oidc.sql): the admin-configured OIDC-group→PVE-identity linkage that
// resolves an OIDC-authenticated human to a per-cluster PVE authorization.
// App-owned intent only, per CLAUDE.md's storage rule — never a shadow copy
// of PVE's own ACLs, which stay authoritative and are re-derived live from
// the mapped credential's own GET /access/permissions on every login/refresh.
//
// CredentialEnc is AES-256-GCM ciphertext (nonce||ciphertext||tag, see
// cipher.go's SessionCipher) — this repository stores/returns the opaque
// sealed bytes only; internal/auth owns sealing/unsealing, exactly like
// ClusterRepo does for clusters.credential_enc.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// OIDCPVELink is one row of the oidc_pve_links table: an OIDC group claim
// mapped to a sealed PVE credential for one cluster.
type OIDCPVELink struct {
	ID            string
	ClusterID     string
	OIDCGroup     string
	PVEUsername   string
	CreatedBy     string
	CredentialEnc []byte
	CreatedAt     int64
}

// OIDCPVELinkRepo is the oidc_pve_links table repository.
type OIDCPVELinkRepo struct {
	db *DB
}

// NewOIDCPVELinkRepo constructs an OIDCPVELinkRepo.
func NewOIDCPVELinkRepo(db *DB) *OIDCPVELinkRepo { return &OIDCPVELinkRepo{db: db} }

// Upsert creates or replaces the linkage for (cluster_id, oidc_group). Re-
// linking a group replaces its credential rather than erroring on the unique
// index, so an admin rotating a mapped token just re-links it.
func (r *OIDCPVELinkRepo) Upsert(ctx context.Context, l OIDCPVELink) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO oidc_pve_links (id, cluster_id, oidc_group, pve_username, credential_enc, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (cluster_id, oidc_group) DO UPDATE SET
			pve_username = excluded.pve_username,
			credential_enc = excluded.credential_enc,
			created_by = excluded.created_by,
			created_at = excluded.created_at`,
		l.ID, l.ClusterID, l.OIDCGroup, l.PVEUsername, l.CredentialEnc, l.CreatedBy, l.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting oidc link %s/%s: %w", l.ClusterID, l.OIDCGroup, err)
	}
	return nil
}

// GetByGroup returns the linkage for one (cluster_id, oidc_group), or
// ErrNotFound if the group has no mapping on that cluster.
func (r *OIDCPVELinkRepo) GetByGroup(ctx context.Context, clusterID, group string) (OIDCPVELink, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, cluster_id, oidc_group, pve_username, credential_enc, created_by, created_at
		FROM oidc_pve_links WHERE cluster_id = ? AND oidc_group = ?`, clusterID, group)
	l, err := scanOIDCPVELink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OIDCPVELink{}, ErrNotFound
	}
	return l, err
}

// ListByCluster returns every linkage registered for one cluster, ordered by
// group for a stable listing.
func (r *OIDCPVELinkRepo) ListByCluster(ctx context.Context, clusterID string) ([]OIDCPVELink, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, cluster_id, oidc_group, pve_username, credential_enc, created_by, created_at
		FROM oidc_pve_links WHERE cluster_id = ? ORDER BY oidc_group ASC`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("store: listing oidc links for cluster %s: %w", clusterID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []OIDCPVELink
	for rows.Next() {
		l, scanErr := scanOIDCPVELink(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing oidc links for cluster %s: %w", clusterID, err)
	}
	return out, nil
}

// Delete removes one linkage by (cluster_id, oidc_group). Deleting an absent
// mapping is not an error, mirroring the other registry repos' convention.
func (r *OIDCPVELinkRepo) Delete(ctx context.Context, clusterID, group string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx,
		`DELETE FROM oidc_pve_links WHERE cluster_id = ? AND oidc_group = ?`, clusterID, group); err != nil {
		return fmt.Errorf("store: deleting oidc link %s/%s: %w", clusterID, group, err)
	}
	return nil
}

func scanOIDCPVELink(row rowScanner) (OIDCPVELink, error) {
	var l OIDCPVELink
	if err := row.Scan(&l.ID, &l.ClusterID, &l.OIDCGroup, &l.PVEUsername, &l.CredentialEnc, &l.CreatedBy, &l.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OIDCPVELink{}, err
		}
		return OIDCPVELink{}, fmt.Errorf("store: scanning oidc link: %w", err)
	}
	return l, nil
}
