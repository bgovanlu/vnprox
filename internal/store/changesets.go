// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Changeset is one row of the changesets table (docs/data-model.md §2). Its
// id is a ULID (see NewULID) so changesets sort lexicographically by
// creation order.
type Changeset struct {
	ID        string
	Title     string
	Author    string
	Status    string // draft|validated|applying|awaiting_confirm|committed|rolled_back|failed|discarded
	ClusterID string // T-1201: the cluster this changeset is scoped to; '' = implicit default/local cluster
	// Origin (T-1701) is 'ui'|'mcp'|'cli' — who staged this changeset;
	// OriginTokenID is the staging bearer token's api_tokens.id ('' when not
	// token-staged). Read via COALESCE so pre-0028 rows (defaulted to 'ui' by
	// the migration) and NULL token ids scan as their empty/'ui' forms.
	Origin        string
	OriginTokenID string
	// OriginTool (T-2705) names the MCP tool that staged this changeset
	// ('changesets.stage.bridge', …), '' for anything not staged by one of
	// them. Written once by Insert and never by Update, like Origin/
	// OriginTokenID: provenance is set at creation and cannot be rewritten by
	// a later edit of the draft.
	OriginTool      string
	OpsJSON         string
	FindingsJSON    sql.NullString
	PlanJSON        sql.NullString
	ApplyLogJSON    sql.NullString
	ConfirmDeadline sql.NullInt64
	// RevertTicketExpiresAt is T-1805's sealed-revert-ticket expiry (unix
	// seconds), or invalid when no ticket is sealed. It is deliberately the
	// ONLY half of the revert-ticket pair that lives on this struct: the
	// expiry is not a secret (it is what the apply response's "unattended
	// revert is available until X" report is computed from), whereas the
	// ciphertext itself (changesets.revert_ticket_enc) is reachable only
	// through RevertTicket below and never enters the read model that
	// internal/change and internal/api hand to a response. It is read-only
	// here: Insert/Update/Upsert never write it — SealRevertTicket and
	// WipeRevertTicket are the only writers, so an ordinary changeset
	// persist can never clobber (or resurrect) a ticket.
	RevertTicketExpiresAt sql.NullInt64
	CreatedAt             int64
	UpdatedAt             int64
}

// ChangesetRepo is the changesets table repository.
type ChangesetRepo struct {
	db *DB
}

// NewChangesetRepo constructs a ChangesetRepo.
func NewChangesetRepo(db *DB) *ChangesetRepo { return &ChangesetRepo{db: db} }

// Insert creates a new changeset row, typically in "draft" status.
func (r *ChangesetRepo) Insert(ctx context.Context, c Changeset) error {
	origin := c.Origin
	if origin == "" {
		origin = "ui" // never write an unlabelled row (mirrors change.OriginUI)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changesets (id, title, author, status, cluster_id, origin, origin_token_id, origin_tool, ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.Author, c.Status, c.ClusterID, origin,
		sql.NullString{String: c.OriginTokenID, Valid: c.OriginTokenID != ""},
		sql.NullString{String: c.OriginTool, Valid: c.OriginTool != ""},
		c.OpsJSON, c.FindingsJSON, c.PlanJSON, c.ApplyLogJSON, c.ConfirmDeadline, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting changeset %s: %w", c.ID, err)
	}
	return nil
}

// Get returns the changeset with the given id, or ErrNotFound.
func (r *ChangesetRepo) Get(ctx context.Context, id string) (Changeset, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, title, author, status, cluster_id, origin, COALESCE(origin_token_id, ''), COALESCE(origin_tool, ''), ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, revert_ticket_expires_at, created_at, updated_at
		FROM changesets WHERE id = ?`, id,
	)
	c, err := scanChangeset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Changeset{}, ErrNotFound
	}
	return c, err
}

// List returns changesets ordered by created_at descending (newest first),
// optionally filtered to a single status. Pass an empty status to list all.
func (r *ChangesetRepo) List(ctx context.Context, status string) ([]Changeset, error) {
	query := `SELECT id, title, author, status, cluster_id, origin, COALESCE(origin_token_id, ''), COALESCE(origin_tool, ''), ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, revert_ticket_expires_at, created_at, updated_at
		FROM changesets`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing changesets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Changeset
	for rows.Next() {
		c, err := scanChangeset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing changesets: %w", err)
	}
	return out, nil
}

// Update rewrites the mutable fields of an existing changeset (status, ops,
// findings, plan, apply log, confirm deadline, updated_at) as it moves
// through stage -> validate -> diff -> apply -> confirm/rollback. It returns
// ErrNotFound if the changeset doesn't exist.
func (r *ChangesetRepo) Update(ctx context.Context, c Changeset) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE changesets
		SET title = ?, status = ?, ops_json = ?, findings_json = ?, plan_json = ?, apply_log_json = ?, confirm_deadline = ?, updated_at = ?
		WHERE id = ?`,
		c.Title, c.Status, c.OpsJSON, c.FindingsJSON, c.PlanJSON, c.ApplyLogJSON, c.ConfirmDeadline, c.UpdatedAt, c.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating changeset %s: %w", c.ID, err)
	}
	return checkRowAffected(res, "store: updating changeset %s", c.ID)
}

// Upsert inserts c, or fully overwrites the existing row with the same id —
// the id-preserving write T-1704's HA replication uses to mirror the active's
// changesets onto the standby verbatim (a normal Insert would collide on a
// row the standby already has from an earlier replication pass, and Update
// would miss a row it has never seen). Every column including created_at is
// replicated so the standby's copy is byte-identical to the active's, never a
// re-timestamped local variant.
func (r *ChangesetRepo) Upsert(ctx context.Context, c Changeset) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changesets (id, title, author, status, cluster_id, ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			title            = excluded.title,
			author           = excluded.author,
			status           = excluded.status,
			cluster_id       = excluded.cluster_id,
			ops_json         = excluded.ops_json,
			findings_json    = excluded.findings_json,
			plan_json        = excluded.plan_json,
			apply_log_json   = excluded.apply_log_json,
			confirm_deadline = excluded.confirm_deadline,
			created_at       = excluded.created_at,
			updated_at       = excluded.updated_at`,
		c.ID, c.Title, c.Author, c.Status, c.ClusterID, c.OpsJSON, c.FindingsJSON, c.PlanJSON, c.ApplyLogJSON, c.ConfirmDeadline, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting changeset %s: %w", c.ID, err)
	}
	return nil
}

// SealRevertTicket stores T-1805's sealed revert ticket (AES-256-GCM
// ciphertext from the shared SessionCipher) and its expiry on an existing
// changeset row, replacing whatever was there. It is the only writer of
// changesets.revert_ticket_enc besides WipeRevertTicket, deliberately
// separate from Update so an ordinary changeset persist can neither clobber
// a live ticket nor resurrect a wiped one.
func (r *ChangesetRepo) SealRevertTicket(ctx context.Context, id string, sealed []byte, expiresAt int64) error {
	if len(sealed) == 0 {
		return fmt.Errorf("store: sealing revert ticket for changeset %s: empty ciphertext", id)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE changesets SET revert_ticket_enc = ?, revert_ticket_expires_at = ? WHERE id = ?`,
		sealed, sql.NullInt64{Int64: expiresAt, Valid: expiresAt > 0}, id,
	)
	if err != nil {
		return fmt.Errorf("store: sealing revert ticket for changeset %s: %w", id, err)
	}
	return checkRowAffected(res, "store: sealing revert ticket for changeset %s", id)
}

// RevertTicket returns the sealed revert-ticket ciphertext and its expiry for
// id. sealed is nil (with a nil error) when the changeset carries no ticket —
// the steady state for every changeset that is not mid-apply or awaiting
// confirmation. It returns ErrNotFound if the changeset itself does not exist.
//
// This is the ONLY read path to changesets.revert_ticket_enc anywhere in the
// codebase; nothing that builds an API/MCP/plugin response calls it (asserted
// by T-1805's registry-enumeration tests).
func (r *ChangesetRepo) RevertTicket(ctx context.Context, id string) (sealed []byte, expiresAt int64, err error) {
	var enc []byte
	var exp sql.NullInt64
	row := r.db.QueryRowContext(ctx,
		`SELECT revert_ticket_enc, revert_ticket_expires_at FROM changesets WHERE id = ?`, id)
	if scanErr := row.Scan(&enc, &exp); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("store: reading revert ticket for changeset %s: %w", id, scanErr)
	}
	return enc, exp.Int64, nil
}

// WipeRevertTicket clears both revert-ticket columns for id. It is
// idempotent, and deliberately does NOT report a missing changeset as an
// error: the wipe runs on every terminal transition (confirm, rollback,
// failed apply, discard, expiry sweep) and must never be the reason one of
// those fails — "the ticket is gone" is the only outcome that matters.
func (r *ChangesetRepo) WipeRevertTicket(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE changesets SET revert_ticket_enc = NULL, revert_ticket_expires_at = NULL WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: wiping revert ticket for changeset %s: %w", id, err)
	}
	return nil
}

// WipeExpiredRevertTickets clears every sealed revert ticket whose expiry is
// at or before now, returning how many rows it cleared. A PVE ticket that has
// expired is dead weight — it can no longer authorize the revert it was
// sealed for — so it is removed rather than left at rest until the changeset
// happens to reach a terminal state (T-1805's "wiped on expiry").
func (r *ChangesetRepo) WipeExpiredRevertTickets(ctx context.Context, now int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE changesets SET revert_ticket_enc = NULL, revert_ticket_expires_at = NULL
		WHERE revert_ticket_enc IS NOT NULL AND revert_ticket_expires_at IS NOT NULL AND revert_ticket_expires_at <= ?`, now)
	if err != nil {
		return 0, fmt.Errorf("store: wiping expired revert tickets: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: wiping expired revert tickets: %w", err)
	}
	return n, nil
}

func scanChangeset(row rowScanner) (Changeset, error) {
	var c Changeset
	err := row.Scan(&c.ID, &c.Title, &c.Author, &c.Status, &c.ClusterID, &c.Origin, &c.OriginTokenID, &c.OriginTool, &c.OpsJSON, &c.FindingsJSON, &c.PlanJSON, &c.ApplyLogJSON, &c.ConfirmDeadline, &c.RevertTicketExpiresAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Changeset{}, err
		}
		return Changeset{}, fmt.Errorf("store: scanning changeset: %w", err)
	}
	return c, nil
}
