// haleases.go implements T-1704's ha_lease table (migration 0031_ha.sql,
// docs/data-model.md §2): the singleton leader-lease/fencing record for an
// active/standby vnproxd pair. App-owned HA coordination state per CLAUDE.md's
// storage rule — never a shadow copy of PVE config. internal/ha owns the
// arbitration semantics (renew/promote/fence); this repository is only the
// durable, replicable persistence for one daemon's best-known view of the
// lease.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// haLeaseSingletonID is the fixed primary key of the one ha_lease row a
// daemon ever holds (a daemon owns at most one leader lease at a time). Kept
// here rather than in internal/ha so the row shape is entirely a store
// concern; internal/ha only ever sees the HALease aggregate.
const haLeaseSingletonID = "singleton"

// HALease is the ha_lease singleton row: which instance (holder) holds the
// leader lease for the current fencing term, and until when (expiresAt, an
// absolute unix-seconds deadline — never a relative duration, so it survives
// replication and restart verbatim). Term is a monotonically-increasing
// fencing token: a promotion only ever writes a strictly-higher term, and a
// heartbeat/action carrying an older term than one already observed is
// rejected. AcquiredAt records when the current holder first won this term;
// UpdatedAt is the last renew/observe write.
type HALease struct {
	Holder     string
	Term       int64
	ExpiresAt  int64
	AcquiredAt int64
	UpdatedAt  int64
}

// HALeaseRepo is the ha_lease table repository.
type HALeaseRepo struct {
	db *DB
}

// NewHALeaseRepo constructs an HALeaseRepo.
func NewHALeaseRepo(db *DB) *HALeaseRepo { return &HALeaseRepo{db: db} }

// Get returns the current lease view, or ErrNotFound if this daemon has never
// recorded one (a fresh install that has not yet acquired or observed a
// lease).
func (r *HALeaseRepo) Get(ctx context.Context) (HALease, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT holder, term, expires_at, acquired_at, updated_at
		FROM ha_lease WHERE id = ?`, haLeaseSingletonID,
	)
	var l HALease
	err := row.Scan(&l.Holder, &l.Term, &l.ExpiresAt, &l.AcquiredAt, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HALease{}, ErrNotFound
	}
	if err != nil {
		return HALease{}, fmt.Errorf("store: reading ha_lease: %w", err)
	}
	return l, nil
}

// Set upserts the singleton lease row to l. internal/ha calls this on every
// renew (the active bumping its own lease's expiresAt), on promotion (writing
// a strictly-higher term), and when adopting a newer term observed from the
// peer — the state-machine correctness of *which* lease to write lives in
// internal/ha, never here.
func (r *HALeaseRepo) Set(ctx context.Context, l HALease) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO ha_lease (id, holder, term, expires_at, acquired_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			holder      = excluded.holder,
			term        = excluded.term,
			expires_at  = excluded.expires_at,
			acquired_at = excluded.acquired_at,
			updated_at  = excluded.updated_at`,
		haLeaseSingletonID, l.Holder, l.Term, l.ExpiresAt, l.AcquiredAt, l.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: writing ha_lease: %w", err)
	}
	return nil
}
