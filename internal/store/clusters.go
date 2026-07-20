// clusters.go implements T-1201's clusters table (docs/data-model.md §2,
// migration 0021_clusters.sql). App-owned registration intent only per
// CLAUDE.md's storage rule: which PVE clusters this vnprox primary attaches
// and aggregates reads across, and how to authenticate to each — never a
// shadow copy of an attached cluster's own live network state, which is
// always fanned out and recomputed fresh by internal/federation.Aggregator.
//
// CredentialEnc is AES-256-GCM ciphertext (nonce||ciphertext||tag, see
// cipher.go's SessionCipher) — this repository stores/returns the opaque
// sealed bytes only; internal/federation owns sealing/unsealing, exactly
// like AlertRuleRepo does for target_secret_enc.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Cluster is one row of the clusters table.
type Cluster struct {
	ID            string
	Name          string
	APIURL        string
	Status        string
	AddedBy       string
	CredentialEnc []byte
	AddedAt       int64
}

// ClusterRepo is the clusters table repository.
type ClusterRepo struct {
	db *DB
}

// NewClusterRepo constructs a ClusterRepo.
func NewClusterRepo(db *DB) *ClusterRepo { return &ClusterRepo{db: db} }

// Insert creates a new clusters row (ID is caller-assigned, typically
// store.NewULID()).
func (r *ClusterRepo) Insert(ctx context.Context, c Cluster) error {
	if c.Status == "" {
		c.Status = "unknown"
	}
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO clusters (id, name, api_url, credential_enc, status, added_by, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.APIURL, c.CredentialEnc, c.Status, c.AddedBy, c.AddedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting cluster %s: %w", c.ID, err)
	}
	return nil
}

// Get returns one cluster by id, or ErrNotFound.
func (r *ClusterRepo) Get(ctx context.Context, id string) (Cluster, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, name, api_url, credential_enc, status, added_by, added_at
		FROM clusters WHERE id = ?`, id)
	c, err := scanCluster(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Cluster{}, ErrNotFound
	}
	return c, err
}

// List returns every registered cluster, ordered by added_at then id for a
// stable listing.
func (r *ClusterRepo) List(ctx context.Context) ([]Cluster, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, name, api_url, credential_enc, status, added_by, added_at
		FROM clusters ORDER BY added_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing clusters: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Cluster
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing clusters: %w", err)
	}
	return out, nil
}

// Update rewrites a cluster's mutable fields (name, api_url, credential_enc,
// status). It returns ErrNotFound if the cluster doesn't exist. A caller
// re-sealing an unchanged credential passes the existing CredentialEnc back
// through; there is no partial update.
func (r *ClusterRepo) Update(ctx context.Context, c Cluster) error {
	if c.Status == "" {
		c.Status = "unknown"
	}
	res, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE clusters SET name = ?, api_url = ?, credential_enc = ?, status = ?
		WHERE id = ?`,
		c.Name, c.APIURL, c.CredentialEnc, c.Status, c.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating cluster %s: %w", c.ID, err)
	}
	return checkRowAffected(res, "store: updating cluster %s", c.ID)
}

// UpdateStatus updates only a cluster's last-pass reachability cache
// (status) — never touches credential_enc. Not an error if id no longer
// exists (an aggregation pass racing a concurrent delete should not itself
// fail), mirroring K8sClusterRepo.UpdateStatus's convention.
func (r *ClusterRepo) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.db.sqlDB.ExecContext(ctx,
		`UPDATE clusters SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("store: updating cluster %s status: %w", id, err)
	}
	return nil
}

// Delete removes a cluster by id. Not an error to delete an already-absent
// one (mirrors IngressTargetRepo/K8sClusterRepo.Delete's convention).
func (r *ClusterRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM clusters WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting cluster %s: %w", id, err)
	}
	return nil
}

func scanCluster(row rowScanner) (Cluster, error) {
	var c Cluster
	if err := row.Scan(&c.ID, &c.Name, &c.APIURL, &c.CredentialEnc, &c.Status, &c.AddedBy, &c.AddedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Cluster{}, err
		}
		return Cluster{}, fmt.Errorf("store: scanning cluster: %w", err)
	}
	return c, nil
}
