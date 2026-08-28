// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EntityLock is one row of the entity_locks table (docs/data-model.md §2,
// T-2805): the advisory lock a staged draft holds on one entity.
//
// Advisory is the operative word, and it is a property of who reads this
// row rather than of the row itself: the only readers are the staging path
// (which warns) and the presence read surface (which reports). No apply
// path reads it — see the migration's own comment for why that is
// structural rather than conventional.
type EntityLock struct {
	Ref         string
	ChangesetID string
	Holder      string
	SessionID   string
	AcquiredAt  int64
	ExpiresAt   int64
}

// EntityLockRepo is the entity_locks table repository.
type EntityLockRepo struct {
	db *DB
}

// NewEntityLockRepo constructs an EntityLockRepo.
func NewEntityLockRepo(db *DB) *EntityLockRepo { return &EntityLockRepo{db: db} }

// Get returns the lock currently recorded for ref, or ErrNotFound.
//
// It returns a row regardless of expiry: expiry is a read-time judgement the
// caller makes against its own clock (internal/presence.Service), not a
// storage-layer one, so that a single injected clock decides it everywhere.
func (r *EntityLockRepo) Get(ctx context.Context, ref string) (EntityLock, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT ref, changeset_id, holder, session_id, acquired_at, expires_at
		FROM entity_locks WHERE ref = ?`, ref)
	l, err := scanEntityLock(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EntityLock{}, ErrNotFound
	}
	return l, err
}

// List returns every recorded lock, ordered by ref, expired rows included —
// same reasoning as Get.
func (r *EntityLockRepo) List(ctx context.Context) ([]EntityLock, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ref, changeset_id, holder, session_id, acquired_at, expires_at
		FROM entity_locks ORDER BY ref`)
	if err != nil {
		return nil, fmt.Errorf("store: listing entity locks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EntityLock
	for rows.Next() {
		l, scanErr := scanEntityLock(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating entity locks: %w", err)
	}
	return out, nil
}

// Upsert takes (or takes over) the lock on l.Ref. The PRIMARY KEY on `ref`
// is what makes "one holder per entity" a constraint rather than a race:
// two concurrent takeovers serialise, and the last writer is the holder.
//
// Deciding WHETHER a takeover is allowed is the caller's job
// (internal/presence.Service, which refuses to overwrite an unexpired lock
// held by someone else unless the caller explicitly overrode it and the
// override was audited). This method deliberately holds no such policy: a
// storage method that silently declined some writes would make the audited
// override path indistinguishable from a lost write.
func (r *EntityLockRepo) Upsert(ctx context.Context, l EntityLock) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO entity_locks (ref, changeset_id, holder, session_id, acquired_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (ref) DO UPDATE SET
			changeset_id = excluded.changeset_id,
			holder       = excluded.holder,
			session_id   = excluded.session_id,
			acquired_at  = excluded.acquired_at,
			expires_at   = excluded.expires_at`,
		l.Ref, l.ChangesetID, l.Holder, l.SessionID, l.AcquiredAt, l.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting entity lock %s: %w", l.Ref, err)
	}
	return nil
}

// DeleteRef releases the lock on ref, if any. Releasing an absent lock is
// not an error: every release path is idempotent by design (a session may
// disconnect twice, a draft may be discarded after its locks already
// expired).
func (r *EntityLockRepo) DeleteRef(ctx context.Context, ref string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM entity_locks WHERE ref = ?`, ref); err != nil {
		return fmt.Errorf("store: releasing entity lock %s: %w", ref, err)
	}
	return nil
}

// DeleteBySession releases every lock held by sessionID and reports how
// many rows it removed. This is the dropped-connection path (T-2805 AC3):
// the closed laptop, not a release call the operator made.
//
// An empty sessionID matches nothing rather than every session-less lock —
// "" is the sentinel for "not bound to a live connection", so treating it
// as a selector would let one disconnect free locks it never took.
func (r *EntityLockRepo) DeleteBySession(ctx context.Context, sessionID string) (int, error) {
	if sessionID == "" {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM entity_locks WHERE session_id = ?`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("store: releasing entity locks for session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting released entity locks for session: %w", err)
	}
	return int(n), nil
}

// DeleteByChangeset releases every lock taken for changesetID and reports
// how many rows it removed — the discarded/terminal-draft path.
func (r *EntityLockRepo) DeleteByChangeset(ctx context.Context, changesetID string) (int, error) {
	if changesetID == "" {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM entity_locks WHERE changeset_id = ?`, changesetID)
	if err != nil {
		return 0, fmt.Errorf("store: releasing entity locks for changeset %s: %w", changesetID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting released entity locks for changeset %s: %w", changesetID, err)
	}
	return int(n), nil
}

// DeleteExpired removes every lock whose expires_at is at or before now and
// reports how many rows it removed. It keeps the table bounded (the soak
// gate scores every table's row-count trend); it is never what makes expiry
// correct — reads already ignore an expired row, so a daemon that never got
// to sweep still behaves exactly as if it had.
func (r *EntityLockRepo) DeleteExpired(ctx context.Context, now int64) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM entity_locks WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, fmt.Errorf("store: sweeping expired entity locks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting swept entity locks: %w", err)
	}
	return int(n), nil
}

func scanEntityLock(s rowScanner) (EntityLock, error) {
	var l EntityLock
	if err := s.Scan(&l.Ref, &l.ChangesetID, &l.Holder, &l.SessionID, &l.AcquiredAt, &l.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EntityLock{}, err
		}
		return EntityLock{}, fmt.Errorf("store: scanning entity lock: %w", err)
	}
	return l, nil
}
