// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Incident is one row of the incidents table (docs/data-model.md §2,
// T-2804): the window an operator is investigating, and nothing else.
//
// There is deliberately no event list here and no incident_events table
// behind it. An incident is a VIEW over history vnprox already records —
// finding_events, audit_log, capture_sessions, flow_samples — assembled at
// read time from Window. See the migration's own note for why an event
// table would break the feature rather than complete it.
type Incident struct {
	ID       string
	Title    string
	Status   string
	OpenedBy string
	// OpenedAt is when the record was created; StartedAt is when the window
	// begins. They differ for a retroactively-opened incident, which is the
	// case that proves this is a view over the past rather than a recorder
	// started in the present.
	OpenedAt  int64
	StartedAt int64
	// EndedAt is the inclusive end of the window, or 0 for "runs to now".
	EndedAt  int64
	ClosedAt int64
}

// Incident status values. Closed is a status, never a deletion.
const (
	IncidentStatusOpen   = "open"
	IncidentStatusClosed = "closed"
)

// IncidentAnnotation is one row of the incident_annotations table: an
// operator's own observation, timestamped on the same timeline as the
// machine-generated events.
type IncidentAnnotation struct {
	ID         string
	IncidentID string
	Author     string
	Body       string
	At         int64
}

// IncidentRepo is the incidents / incident_annotations repository.
type IncidentRepo struct {
	db *DB
}

// NewIncidentRepo constructs an IncidentRepo.
func NewIncidentRepo(db *DB) *IncidentRepo { return &IncidentRepo{db: db} }

const incidentCols = `id, title, status, opened_by, opened_at, started_at, ended_at, closed_at`

// Insert creates a new incident row (ID is caller-assigned, typically
// store.NewULID()).
func (r *IncidentRepo) Insert(ctx context.Context, i Incident) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO incidents (`+incidentCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.Title, i.Status, i.OpenedBy, i.OpenedAt, i.StartedAt, i.EndedAt, i.ClosedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting incident %s: %w", i.ID, err)
	}
	return nil
}

// Get returns one incident by id, or ErrNotFound.
func (r *IncidentRepo) Get(ctx context.Context, id string) (Incident, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+incidentCols+` FROM incidents WHERE id = ?`, id)
	var i Incident
	err := row.Scan(&i.ID, &i.Title, &i.Status, &i.OpenedBy, &i.OpenedAt, &i.StartedAt, &i.EndedAt, &i.ClosedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("store: reading incident %s: %w", id, err)
	}
	return i, nil
}

// List returns every incident, most recent window first. Incidents are
// operator-created records rather than a machine-generated stream, so there
// is no pagination contract and no retention prune: closing one is a status
// change, and nothing in this package ever deletes one.
func (r *IncidentRepo) List(ctx context.Context) ([]Incident, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+incidentCols+` FROM incidents ORDER BY started_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Incident{}
	for rows.Next() {
		var i Incident
		if scanErr := rows.Scan(&i.ID, &i.Title, &i.Status, &i.OpenedBy,
			&i.OpenedAt, &i.StartedAt, &i.EndedAt, &i.ClosedAt); scanErr != nil {
			return nil, fmt.Errorf("store: scanning incident: %w", scanErr)
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing incidents: %w", err)
	}
	return out, nil
}

// SetStatus updates only the three lifecycle columns — status, the window
// end, and the close instant.
//
// It cannot touch title, opened_by, opened_at or started_at: an incident's
// identity and the start of its window are set once at open. That is why
// this is not a general Update.
func (r *IncidentRepo) SetStatus(ctx context.Context, id, status string, endedAt, closedAt int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE incidents SET status = ?, ended_at = ?, closed_at = ? WHERE id = ?`,
		status, endedAt, closedAt, id)
	if err != nil {
		return fmt.Errorf("store: updating incident %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: updating incident %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertAnnotation records one operator observation against an incident.
func (r *IncidentRepo) InsertAnnotation(ctx context.Context, a IncidentAnnotation) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO incident_annotations (id, incident_id, at, author, body) VALUES (?, ?, ?, ?, ?)`,
		a.ID, a.IncidentID, a.At, a.Author, a.Body,
	)
	if err != nil {
		return fmt.Errorf("store: inserting annotation for incident %s: %w", a.IncidentID, err)
	}
	return nil
}

// ListAnnotations returns one incident's annotations, oldest first.
//
// Unlike the other four timeline sources this is not time-ranged: an
// annotation belongs to its incident, and an operator who back-dates a note
// outside the window is describing that incident anyway. Filtering it out
// would silently lose an observation nothing else records.
func (r *IncidentRepo) ListAnnotations(ctx context.Context, incidentID string) ([]IncidentAnnotation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, incident_id, at, author, body FROM incident_annotations
		WHERE incident_id = ? ORDER BY at ASC, id ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("store: listing annotations for incident %s: %w", incidentID, err)
	}
	defer func() { _ = rows.Close() }()

	out := []IncidentAnnotation{}
	for rows.Next() {
		var a IncidentAnnotation
		if scanErr := rows.Scan(&a.ID, &a.IncidentID, &a.At, &a.Author, &a.Body); scanErr != nil {
			return nil, fmt.Errorf("store: scanning incident annotation: %w", scanErr)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing annotations for incident %s: %w", incidentID, err)
	}
	return out, nil
}
