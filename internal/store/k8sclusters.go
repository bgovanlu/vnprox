// SPDX-License-Identifier: Apache-2.0

// k8sclusters.go implements T-1501's k8s_clusters table (docs/data-model.md
// §2, migration 0019_k8s_clusters.sql). App-owned registration intent only
// per CLAUDE.md's storage rule: which k8s clusters vnprox polls, and how to
// authenticate to them — never a snapshot of the cluster's own live state.
// KubeconfigEnc is AES-256-GCM ciphertext (nonce||ciphertext||tag, see
// cipher.go's SessionCipher) — this repository stores/returns the opaque
// sealed bytes only; internal/api's k8s handlers own sealing/unsealing,
// exactly like AlertRuleRepo does for target_secret_enc and
// IngressTargetRepo does for credential_enc.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// K8sCluster is one row of the k8s_clusters table.
type K8sCluster struct {
	ID            string
	Name          string
	AddedBy       string
	CNIDetected   string
	Status        string
	KubeconfigEnc []byte
	AddedAt       int64
}

// K8sClusterRepo is the k8s_clusters table repository.
type K8sClusterRepo struct {
	db *DB
}

// NewK8sClusterRepo constructs a K8sClusterRepo.
func NewK8sClusterRepo(db *DB) *K8sClusterRepo { return &K8sClusterRepo{db: db} }

// Insert creates a new k8s_clusters row (ID is caller-assigned, typically
// store.NewULID()).
func (r *K8sClusterRepo) Insert(ctx context.Context, c K8sCluster) error {
	if c.Status == "" {
		c.Status = "unpolled"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO k8s_clusters (id, name, kubeconfig_enc, added_by, added_at, cni_detected, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.KubeconfigEnc, c.AddedBy, c.AddedAt, c.CNIDetected, c.Status,
	)
	if err != nil {
		return fmt.Errorf("store: inserting k8s cluster %s: %w", c.ID, err)
	}
	return nil
}

// Get returns one cluster by id, or ErrNotFound.
func (r *K8sClusterRepo) Get(ctx context.Context, id string) (K8sCluster, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, kubeconfig_enc, added_by, added_at, cni_detected, status
		FROM k8s_clusters WHERE id = ?`, id)
	c, err := scanK8sCluster(row)
	if errors.Is(err, sql.ErrNoRows) {
		return K8sCluster{}, ErrNotFound
	}
	return c, err
}

// List returns every registered cluster, ordered by added_at then id for a
// stable listing.
func (r *K8sClusterRepo) List(ctx context.Context) ([]K8sCluster, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, kubeconfig_enc, added_by, added_at, cni_detected, status
		FROM k8s_clusters ORDER BY added_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing k8s clusters: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []K8sCluster
	for rows.Next() {
		c, err := scanK8sCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing k8s clusters: %w", err)
	}
	return out, nil
}

// Delete removes a cluster by id. Not an error to delete an already-absent
// one (mirrors IngressTargetRepo.Delete's convention).
func (r *K8sClusterRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM k8s_clusters WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting k8s cluster %s: %w", id, err)
	}
	return nil
}

// UpdateStatus updates a cluster's last-poll cache (cni_detected/status)
// only — never touches kubeconfig_enc. Not an error if id no longer
// exists (a poll racing a concurrent delete should not itself fail).
func (r *K8sClusterRepo) UpdateStatus(ctx context.Context, id, cniDetected, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE k8s_clusters SET cni_detected = ?, status = ? WHERE id = ?`,
		cniDetected, status, id,
	)
	if err != nil {
		return fmt.Errorf("store: updating k8s cluster %s status: %w", id, err)
	}
	return nil
}

func scanK8sCluster(row rowScanner) (K8sCluster, error) {
	var c K8sCluster
	if err := row.Scan(&c.ID, &c.Name, &c.KubeconfigEnc, &c.AddedBy, &c.AddedAt, &c.CNIDetected, &c.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return K8sCluster{}, err
		}
		return K8sCluster{}, fmt.Errorf("store: scanning k8s cluster: %w", err)
	}
	return c, nil
}
