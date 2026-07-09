// Package store implements vnproxd's SQLite-backed app store: schema
// migrations and typed repositories for every table documented in
// docs/data-model.md §2 ("App store (SQLite)").
//
// Proxmox VE remains the source of truth for network configuration; this
// package only ever holds app-owned data — sessions, changesets, snapshots,
// audit log entries, saved UI layouts, short-horizon metric samples, and a
// small kv table used for schema bookkeeping. It never persists a shadow
// copy of PVE config as authoritative state.
//
// # Opening a database
//
// Open applies embedded, forward-only migrations (internal/store/migrations)
// and enables WAL journaling, foreign key enforcement, and a busy_timeout so
// concurrent writers block-and-retry inside SQLite rather than surfacing
// SQLITE_BUSY to callers. The current schema version is tracked in the kv
// table; Open refuses (ErrSchemaTooNew) to open a database whose stored
// version is newer than this build's embedded migrations understand, so an
// old binary pointed at a newer database fails fast instead of
// misinterpreting it.
//
// # Repositories
//
// Each table has a small repository type (SessionRepo, ChangesetRepo,
// SnapshotRepo, AuditRepo, LayoutRepo, MetricSampleRepo, KVRepo) constructed
// from an open *DB. All methods are context-first and wrap errors with
// operation context, per repo convention.
//
// # Session secrets
//
// sessions.pve_ticket_enc and sessions.csrf_token_enc are encrypted at rest
// with AES-256-GCM (see cipher.go); SessionRepo takes a *SessionCipher
// constructed from a 256-bit key so callers (and tests) can supply the key
// however they like rather than this package hardcoding a key file path.
// The production key file convention (/etc/vnprox/keys/session.key,
// root:root 0600, generated at install) lives in docs/security.md
// "Authentication"; LoadKeyFile/GenerateKeyFile in cipher.go are the helpers
// the daemon and the packaging postinst script are expected to use.
package store
