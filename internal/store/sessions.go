package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Session is a server-side session record (docs/data-model.md §2). Ticket
// and CSRF token are plaintext here; SessionRepo encrypts/decrypts them
// against the sessions table's *_enc BLOB columns transparently.
type Session struct {
	ID        string
	Username  string
	Realm     string
	PVETicket string
	CSRFToken string
	CapsJSON  string
	CreatedAt int64
	ExpiresAt int64
}

// SessionRepo is the sessions table repository. It encrypts pve_ticket_enc
// and csrf_token_enc with the supplied cipher before they reach SQLite, and
// decrypts them on the way out.
type SessionRepo struct {
	db     *DB
	cipher *SessionCipher
}

// NewSessionRepo constructs a SessionRepo. cipher must not be nil.
func NewSessionRepo(db *DB, cipher *SessionCipher) *SessionRepo {
	return &SessionRepo{db: db, cipher: cipher}
}

// Insert creates a new session row.
func (r *SessionRepo) Insert(ctx context.Context, s Session) error {
	ticketEnc, err := r.cipher.Encrypt([]byte(s.PVETicket))
	if err != nil {
		return fmt.Errorf("store: encrypting session %s pve ticket: %w", s.ID, err)
	}
	csrfEnc, err := r.cipher.Encrypt([]byte(s.CSRFToken))
	if err != nil {
		return fmt.Errorf("store: encrypting session %s csrf token: %w", s.ID, err)
	}

	_, err = r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO sessions (id, username, realm, pve_ticket_enc, csrf_token_enc, caps_json, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Username, s.Realm, ticketEnc, csrfEnc, s.CapsJSON, s.CreatedAt, s.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting session %s: %w", s.ID, err)
	}
	return nil
}

// Get returns the session with the given id, decrypting its secrets. It
// returns ErrNotFound if no such session exists.
func (r *SessionRepo) Get(ctx context.Context, id string) (Session, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, username, realm, pve_ticket_enc, csrf_token_enc, caps_json, created_at, expires_at
		FROM sessions WHERE id = ?`, id,
	)
	return r.scanRow(row)
}

// List returns all sessions ordered by created_at ascending.
func (r *SessionRepo) List(ctx context.Context) ([]Session, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, username, realm, pve_ticket_enc, csrf_token_enc, caps_json, created_at, expires_at
		FROM sessions ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Session
	for rows.Next() {
		s, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing sessions: %w", err)
	}
	return out, nil
}

// Update replaces the mutable fields of an existing session: the encrypted
// ticket/CSRF secrets (renewed ~1h30 into the PVE ticket's life per
// docs/security.md), capabilities, and expiry. It returns ErrNotFound if the
// session doesn't exist.
func (r *SessionRepo) Update(ctx context.Context, s Session) error {
	ticketEnc, err := r.cipher.Encrypt([]byte(s.PVETicket))
	if err != nil {
		return fmt.Errorf("store: encrypting session %s pve ticket: %w", s.ID, err)
	}
	csrfEnc, err := r.cipher.Encrypt([]byte(s.CSRFToken))
	if err != nil {
		return fmt.Errorf("store: encrypting session %s csrf token: %w", s.ID, err)
	}

	res, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE sessions SET pve_ticket_enc = ?, csrf_token_enc = ?, caps_json = ?, expires_at = ?
		WHERE id = ?`,
		ticketEnc, csrfEnc, s.CapsJSON, s.ExpiresAt, s.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating session %s: %w", s.ID, err)
	}
	return checkRowAffected(res, "store: updating session %s", s.ID)
}

// Delete removes a session (logout, expiry sweep). It is not an error to
// delete an already-absent session.
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting session %s: %w", id, err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (r *SessionRepo) scanRow(row *sql.Row) (Session, error) {
	s, err := r.scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

func (r *SessionRepo) scan(row rowScanner) (Session, error) {
	var (
		s                  Session
		ticketEnc, csrfEnc []byte
	)
	if err := row.Scan(&s.ID, &s.Username, &s.Realm, &ticketEnc, &csrfEnc, &s.CapsJSON, &s.CreatedAt, &s.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, err
		}
		return Session{}, fmt.Errorf("store: scanning session: %w", err)
	}

	ticket, err := r.cipher.Decrypt(ticketEnc)
	if err != nil {
		return Session{}, fmt.Errorf("store: decrypting session %s pve ticket: %w", s.ID, err)
	}
	csrf, err := r.cipher.Decrypt(csrfEnc)
	if err != nil {
		return Session{}, fmt.Errorf("store: decrypting session %s csrf token: %w", s.ID, err)
	}
	s.PVETicket = string(ticket)
	s.CSRFToken = string(csrf)
	return s, nil
}

// checkRowAffected returns ErrNotFound (wrapped with a formatted message) if
// res reports zero rows affected, which for our UPDATE-by-primary-key
// statements means the target row didn't exist.
func checkRowAffected(res sql.Result, format string, args ...any) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf(format+": checking rows affected: %w", append(args, err)...)
	}
	if n == 0 {
		return fmt.Errorf(format+": %w", append(args, ErrNotFound)...)
	}
	return nil
}
