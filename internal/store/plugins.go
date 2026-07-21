package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// PluginRow is one row of the plugins table (migration 0029_plugins.sql): the
// registry's record of one installed extension plugin. ExtensionPoints and
// Capabilities are stored as JSON string lists (extension_points_json /
// capabilities_json) and decoded here into slices; the store neither validates
// their vocabulary nor interprets them — internal/plugin's registry does that
// on install, before a row is ever written. Transport is "in-process" or
// "grpc"; Endpoint is the out-of-process launch hint (empty for in-process).
//
// This is app-owned data (CLAUDE.md's storage rule): it records what was
// installed and with which capability scope, never a shadow of PVE config. The
// capability scope recorded here is a ceiling — a plugin can only ever reach the
// seams its capabilities cover, and never an apply/confirm/rollback path.
type PluginRow struct {
	ID              string
	Name            string
	Version         string
	APIVersion      string
	Transport       string
	Endpoint        string
	InstalledBy     string
	ExtensionPoints []string
	Capabilities    []string
	InstalledAt     int64
	Enabled         bool
}

// PluginRepo is the plugins table repository.
type PluginRepo struct {
	db *DB
}

// NewPluginRepo constructs a PluginRepo.
func NewPluginRepo(db *DB) *PluginRepo { return &PluginRepo{db: db} }

// Upsert installs (or re-installs) a plugin row, replacing any existing row with
// the same id. Re-install is how an operator changes a plugin's capability scope
// or transport; the caller (internal/plugin's registry) audits install/uninstall
// separately.
func (r *PluginRepo) Upsert(ctx context.Context, p PluginRow) error {
	eps, err := json.Marshal(nonNilStrings(p.ExtensionPoints))
	if err != nil {
		return fmt.Errorf("store: encoding plugin extension points: %w", err)
	}
	caps, err := json.Marshal(nonNilStrings(p.Capabilities))
	if err != nil {
		return fmt.Errorf("store: encoding plugin capabilities: %w", err)
	}
	_, err = r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO plugins
		  (id, name, version, api_version, extension_points_json, capabilities_json,
		   transport, endpoint, enabled, installed_by, installed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
		  name = excluded.name,
		  version = excluded.version,
		  api_version = excluded.api_version,
		  extension_points_json = excluded.extension_points_json,
		  capabilities_json = excluded.capabilities_json,
		  transport = excluded.transport,
		  endpoint = excluded.endpoint,
		  enabled = excluded.enabled,
		  installed_by = excluded.installed_by,
		  installed_at = excluded.installed_at`,
		p.ID, p.Name, p.Version, p.APIVersion, string(eps), string(caps),
		p.Transport, p.Endpoint, boolToInt(p.Enabled), p.InstalledBy, p.InstalledAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting plugin %q: %w", p.ID, err)
	}
	return nil
}

// SetEnabled flips a plugin's enabled state without otherwise rewriting the row.
// Returns ErrNotFound when no plugin with id exists.
func (r *PluginRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := r.db.sqlDB.ExecContext(ctx,
		`UPDATE plugins SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("store: setting plugin %q enabled=%v: %w", id, enabled, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: reading rows affected for plugin %q: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete uninstalls a plugin row. Returns ErrNotFound when no plugin with id
// exists so the caller can distinguish a no-op from a real uninstall.
func (r *PluginRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM plugins WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting plugin %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: reading rows affected for plugin %q: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one plugin row, or ErrNotFound.
func (r *PluginRepo) Get(ctx context.Context, id string) (PluginRow, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, name, version, api_version, extension_points_json, capabilities_json,
		       transport, endpoint, enabled, installed_by, installed_at
		FROM plugins WHERE id = ?`, id)
	p, err := scanPluginRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PluginRow{}, ErrNotFound
	}
	if err != nil {
		return PluginRow{}, fmt.Errorf("store: getting plugin %q: %w", id, err)
	}
	return p, nil
}

// List returns every installed plugin, newest-first.
func (r *PluginRepo) List(ctx context.Context) ([]PluginRow, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, name, version, api_version, extension_points_json, capabilities_json,
		       transport, endpoint, enabled, installed_by, installed_at
		FROM plugins ORDER BY installed_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing plugins: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PluginRow
	for rows.Next() {
		p, err := scanPluginRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning plugin row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating plugin rows: %w", err)
	}
	return out, nil
}

func scanPluginRow(s rowScanner) (PluginRow, error) {
	var (
		p        PluginRow
		epsJSON  string
		capsJSON string
		enabled  int
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Version, &p.APIVersion, &epsJSON, &capsJSON,
		&p.Transport, &p.Endpoint, &enabled, &p.InstalledBy, &p.InstalledAt); err != nil {
		return PluginRow{}, err
	}
	if err := json.Unmarshal([]byte(epsJSON), &p.ExtensionPoints); err != nil {
		return PluginRow{}, fmt.Errorf("decoding extension points for plugin %q: %w", p.ID, err)
	}
	if err := json.Unmarshal([]byte(capsJSON), &p.Capabilities); err != nil {
		return PluginRow{}, fmt.Errorf("decoding capabilities for plugin %q: %w", p.ID, err)
	}
	p.Enabled = enabled != 0
	return p, nil
}

// nonNilStrings normalizes a nil slice to an empty one so it JSON-encodes as
// "[]" rather than "null" — keeping the stored column round-trippable.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
