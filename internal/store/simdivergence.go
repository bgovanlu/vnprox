// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
)

// SimDivergenceFinding is one row of the sim_divergence_findings table
// (T-806, 0005_sim_divergence_findings.sql): a path-simulator tuple whose
// live guest-agent probe (POST /simulate/verify) disagreed with the static
// simulator's own verdict for the identical src/dst/proto/port. ID is the
// same content-derived key internal/findings.Finding.ID uses for this
// producer (Source: "probe", Check: "sim_divergence") — see
// internal/api/simulate.go's simDivergenceTupleKey.
type SimDivergenceFinding struct {
	ID               string
	SrcRef           string
	DstKind          string
	DstRef           string
	DstIP            string
	Proto            string
	Detail           string
	SimulatedVerdict string
	ObservedOutcome  string
	Port             int
	CreatedAt        int64
	UpdatedAt        int64
}

// SimDivergenceRepo is the sim_divergence_findings table repository.
type SimDivergenceRepo struct {
	db *DB
}

// NewSimDivergenceRepo constructs a SimDivergenceRepo.
func NewSimDivergenceRepo(db *DB) *SimDivergenceRepo { return &SimDivergenceRepo{db: db} }

// Upsert records f as persisting proof of a divergence — insert if this
// tuple's id is new, overwrite (refreshing updated_at, preserving the
// original created_at) if it already diverged before. Called from
// POST /simulate/verify's handler exactly once per request whose response
// carries `diverges: true`.
func (r *SimDivergenceRepo) Upsert(ctx context.Context, f SimDivergenceFinding) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sim_divergence_findings
			(id, src_ref, dst_kind, dst_ref, dst_ip, proto, port, simulated_verdict, observed_outcome, detail, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			simulated_verdict = excluded.simulated_verdict,
			observed_outcome  = excluded.observed_outcome,
			detail            = excluded.detail,
			updated_at        = excluded.updated_at`,
		f.ID, f.SrcRef, f.DstKind, f.DstRef, f.DstIP, f.Proto, f.Port,
		f.SimulatedVerdict, f.ObservedOutcome, f.Detail, f.CreatedAt, f.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting sim_divergence_finding %s: %w", f.ID, err)
	}
	return nil
}

// Clear removes a persisted divergence finding by id — called when a
// tuple that previously diverged is re-verified and no longer does (the
// finding should not keep claiming a divergence that's no longer true of
// the most recent live check). Not an error to clear an already-absent id.
func (r *SimDivergenceRepo) Clear(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sim_divergence_findings WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: clearing sim_divergence_finding %s: %w", id, err)
	}
	return nil
}

// List returns every persisted divergence finding, ordered by id for a
// stable, deterministic listing (internal/findings.sortFindings re-sorts
// the unified stream anyway, but a stable order here keeps this repo's own
// tests/behavior deterministic on its own).
func (r *SimDivergenceRepo) List(ctx context.Context) ([]SimDivergenceFinding, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, src_ref, dst_kind, dst_ref, dst_ip, proto, port, simulated_verdict, observed_outcome, detail, created_at, updated_at
		FROM sim_divergence_findings ORDER BY id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing sim_divergence_findings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []SimDivergenceFinding
	for rows.Next() {
		var f SimDivergenceFinding
		if err := rows.Scan(&f.ID, &f.SrcRef, &f.DstKind, &f.DstRef, &f.DstIP, &f.Proto, &f.Port,
			&f.SimulatedVerdict, &f.ObservedOutcome, &f.Detail, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning sim_divergence_finding: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing sim_divergence_findings: %w", err)
	}
	return out, nil
}
