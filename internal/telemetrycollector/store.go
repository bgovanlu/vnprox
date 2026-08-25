package telemetrycollector

// store.go is the collector's entire persistence layer: one table, no
// migration framework. This is a deliberate, small independent decision
// from internal/store (vnproxd's own app-owned-data database) — this
// package ships in a different binary (cmd/vnproxtelemetryd), with a
// different lifecycle and its own database file, and a single-table schema
// does not need a migration engine sized for vnproxd's fifty-plus tables.
// If this schema ever needs to change in place against data already
// collected, add one the way internal/store's migrate.go does; until then
// this stays a plain CREATE TABLE IF NOT EXISTS.
//
// Every column here traces back either to a field of internal/telemetry.
// Payload (unchanged — this package does not reshape what it stores) or to
// ReceivedAt, the one field the collector itself adds. docs/security.md's
// collector section states that pairing explicitly, and
// TestStoreColumnsMatchPayloadAndReceivedAt (store_test.go) checks it by
// reflection the same way internal/telemetry.docs.go checks the payload
// against its own doc table — so a column added here without being
// documented fails the build the same way an undocumented payload field
// does.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/bgovanlu/vnprox/internal/telemetry"
)

// dbFilePerm mirrors internal/store's own dbFilePerm: docs/security.md's
// "Host footprint" expectation ("SQLite DB and key files are root:root
// 0600") applies here too — this file holds every compatibility report
// this collector has ever received.
const dbFilePerm = 0o600

// Store is the collector's opened, schema-ready database. *sql.DB is
// already safe for concurrent use, so Store adds no locking of its own.
type Store struct {
	sqlDB *sql.DB
	path  string
}

// Open opens (creating if necessary) the SQLite database at path, applies
// the schema, and enforces file permissions.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("telemetrycollector: opening database %s: %w", path, err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("telemetrycollector: connecting to database %s: %w", path, err)
	}
	if _, err := sqlDB.ExecContext(ctx, schemaSQL); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("telemetrycollector: applying schema to %s: %w", path, err)
	}
	if err := enforceDBFilePerms(path); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &Store{sqlDB: sqlDB, path: path}, nil
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS submissions (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	install_id      TEXT    NOT NULL,
	received_at     INTEGER NOT NULL,
	payload_version INTEGER NOT NULL,
	vnprox_version  TEXT    NOT NULL,
	pve_version     TEXT    NOT NULL,
	kernel          TEXT    NOT NULL,
	nic_pci_ids     TEXT    NOT NULL,
	node_count      INTEGER NOT NULL,
	suite           TEXT    NOT NULL,
	checks          TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS submissions_install_id_idx ON submissions(install_id);
CREATE INDEX IF NOT EXISTS submissions_received_at_idx ON submissions(received_at);
`

func enforceDBFilePerms(path string) error {
	if err := os.Chmod(path, dbFilePerm); err != nil {
		return fmt.Errorf("telemetrycollector: setting permissions on %s: %w", path, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if _, statErr := os.Stat(sidecar); statErr != nil {
			continue
		}
		if err := os.Chmod(sidecar, dbFilePerm); err != nil {
			return fmt.Errorf("telemetrycollector: setting permissions on %s: %w", sidecar, err)
		}
	}
	return nil
}

// Path returns the database file path Store was opened with.
func (s *Store) Path() string { return s.path }

// Close closes the underlying database connection pool.
func (s *Store) Close() error {
	if err := s.sqlDB.Close(); err != nil {
		return fmt.Errorf("telemetrycollector: closing database: %w", err)
	}
	return nil
}

// Insert stores one submission. receivedAt is the collector's own clock —
// the payload itself carries no timestamp (internal/telemetry's package
// doc: "a local clock is a fingerprint").
func (s *Store) Insert(ctx context.Context, p telemetry.Payload, receivedAt time.Time) error {
	nicIDs, err := json.Marshal(p.NICPCIIDs)
	if err != nil {
		return fmt.Errorf("telemetrycollector: encoding nicPciIds: %w", err)
	}
	checks, err := json.Marshal(p.Checks)
	if err != nil {
		return fmt.Errorf("telemetrycollector: encoding checks: %w", err)
	}

	_, err = s.sqlDB.ExecContext(ctx, `
		INSERT INTO submissions
			(install_id, received_at, payload_version, vnprox_version, pve_version, kernel, nic_pci_ids, node_count, suite, checks)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.InstallID, receivedAt.Unix(), p.PayloadVersion, p.VnproxVersion, p.PVEVersion, p.Kernel,
		string(nicIDs), p.NodeCount, p.Suite, string(checks),
	)
	if err != nil {
		return fmt.Errorf("telemetrycollector: storing submission: %w", err)
	}
	return nil
}

// DeleteByInstallID deletes every submission for installID and returns how
// many rows were removed. It is the revocation mechanism: InstallID is the
// payload's only correlator, so deleting every row that carries it is
// deleting everything this collector holds that traces back to one
// install. Idempotent — deleting an id with no rows returns (0, nil), not
// an error, so a caller cannot use the response to probe whether an id was
// ever seen.
func (s *Store) DeleteByInstallID(ctx context.Context, installID string) (int64, error) {
	res, err := s.sqlDB.ExecContext(ctx, `DELETE FROM submissions WHERE install_id = ?`, installID)
	if err != nil {
		return 0, fmt.Errorf("telemetrycollector: deleting submissions for install-id %s: %w", installID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("telemetrycollector: counting deleted submissions for install-id %s: %w", installID, err)
	}
	return n, nil
}

// Prune deletes every submission received before cutoff and returns how
// many rows were removed. This is the retention mechanism (T-3710 AC4): a
// real DELETE against a configurable window, not a documented intention.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.sqlDB.ExecContext(ctx, `DELETE FROM submissions WHERE received_at < ?`, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("telemetrycollector: pruning submissions older than %s: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("telemetrycollector: counting pruned submissions: %w", err)
	}
	return n, nil
}

// Count returns the total number of stored submissions, for tests and for
// the retention CLI's before/after report.
func (s *Store) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := s.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM submissions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("telemetrycollector: counting submissions: %w", err)
	}
	return n, nil
}
