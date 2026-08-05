// tenants.go implements T-1703's multi-tenancy tables (docs/data-model.md §2,
// migration 0030_tenants.sql): tenants, tenant_scopes, tenant_members, and the
// changeset_requests linkage for request-changesets.
//
// App-owned delegation model only per CLAUDE.md's storage rule: who may see
// (tenant_scopes) and request (tenant_members) what, never a shadow copy of any
// PVE-authoritative config. The scope refs are inventory Ref strings naming
// visible resources; the live entities are always read fresh from the graph.
//
// SECURITY: this repository is the server-side data-access layer T-1703's
// tenant scoping is enforced at. ScopesForTenant / TenantsForIdentity /
// MemberRole are the only source of a principal's visibility and role — no
// client-supplied value ever widens them.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Tenant roles (tenant_members.role).
const (
	TenantRoleMember   = "member"
	TenantRoleApprover = "approver"
)

// Tenant is one row of the tenants table.
type Tenant struct {
	ID        string
	Name      string
	CreatedBy string
	CreatedAt int64
}

// TenantMember is one row of the tenant_members table.
type TenantMember struct {
	TenantID string
	Identity string
	Role     string
}

// ChangesetRequest is one row of the changeset_requests table: the tenant
// linkage a request-changeset carries alongside its ordinary changesets row.
type ChangesetRequest struct {
	ChangesetID string
	TenantID    string
	RequestedBy string
	ApprovedBy  string
	CreatedAt   int64
	ApprovedAt  int64
}

// TenantRepo is the repository over every T-1703 table.
type TenantRepo struct {
	db *DB
}

// NewTenantRepo constructs a TenantRepo.
func NewTenantRepo(db *DB) *TenantRepo { return &TenantRepo{db: db} }

// InsertTenant creates a new tenants row (ID is caller-assigned, typically
// store.NewULID()).
func (r *TenantRepo) InsertTenant(ctx context.Context, t Tenant) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tenants (id, name, created_by, created_at)
		VALUES (?, ?, ?, ?)`,
		t.ID, t.Name, t.CreatedBy, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting tenant %s: %w", t.ID, err)
	}
	return nil
}

// GetTenant returns one tenant by id, or ErrNotFound.
func (r *TenantRepo) GetTenant(ctx context.Context, id string) (Tenant, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, created_by, created_at FROM tenants WHERE id = ?`, id)
	var t Tenant
	if err := row.Scan(&t.ID, &t.Name, &t.CreatedBy, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Tenant{}, ErrNotFound
		}
		return Tenant{}, fmt.Errorf("store: scanning tenant: %w", err)
	}
	return t, nil
}

// ListTenants returns every tenant, ordered by created_at then id.
func (r *TenantRepo) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, created_by, created_at FROM tenants ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing tenants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedBy, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning tenant: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing tenants: %w", err)
	}
	return out, nil
}

// DeleteTenant removes a tenant and (via ON DELETE CASCADE) its scopes,
// members, and request linkages. Not an error to delete an already-absent one.
func (r *TenantRepo) DeleteTenant(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM tenants WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting tenant %s: %w", id, err)
	}
	return nil
}

// AddScope adds a scope ref to a tenant. Idempotent (INSERT OR IGNORE): adding
// the same ref twice is a no-op, never an error.
func (r *TenantRepo) AddScope(ctx context.Context, tenantID, scopeRef string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO tenant_scopes (tenant_id, scope_ref) VALUES (?, ?)`,
		tenantID, scopeRef,
	)
	if err != nil {
		return fmt.Errorf("store: adding scope %q to tenant %s: %w", scopeRef, tenantID, err)
	}
	return nil
}

// RemoveScope removes a scope ref from a tenant. Not an error if absent.
func (r *TenantRepo) RemoveScope(ctx context.Context, tenantID, scopeRef string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM tenant_scopes WHERE tenant_id = ? AND scope_ref = ?`, tenantID, scopeRef); err != nil {
		return fmt.Errorf("store: removing scope %q from tenant %s: %w", scopeRef, tenantID, err)
	}
	return nil
}

// ScopesForTenant returns every scope ref for a tenant, sorted for a stable
// listing. This is the server-side source of a tenant's visible-resource set.
func (r *TenantRepo) ScopesForTenant(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT scope_ref FROM tenant_scopes WHERE tenant_id = ? ORDER BY scope_ref ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: listing scopes for tenant %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("store: scanning scope ref: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing scopes for tenant %s: %w", tenantID, err)
	}
	return out, nil
}

// PutMember inserts or updates a tenant membership (upsert on the
// (tenant_id, identity) primary key), so re-adding an identity with a new role
// promotes/demotes rather than erroring.
func (r *TenantRepo) PutMember(ctx context.Context, m TenantMember) error {
	if m.Role != TenantRoleMember && m.Role != TenantRoleApprover {
		return fmt.Errorf("store: invalid tenant role %q", m.Role)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tenant_members (tenant_id, identity, role) VALUES (?, ?, ?)
		ON CONFLICT (tenant_id, identity) DO UPDATE SET role = excluded.role`,
		m.TenantID, m.Identity, m.Role,
	)
	if err != nil {
		return fmt.Errorf("store: putting member %q in tenant %s: %w", m.Identity, m.TenantID, err)
	}
	return nil
}

// RemoveMember removes an identity from a tenant. Not an error if absent.
func (r *TenantRepo) RemoveMember(ctx context.Context, tenantID, identity string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM tenant_members WHERE tenant_id = ? AND identity = ?`, tenantID, identity); err != nil {
		return fmt.Errorf("store: removing member %q from tenant %s: %w", identity, tenantID, err)
	}
	return nil
}

// MembersForTenant returns every membership of a tenant, sorted by identity.
func (r *TenantRepo) MembersForTenant(ctx context.Context, tenantID string) ([]TenantMember, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT tenant_id, identity, role FROM tenant_members WHERE tenant_id = ? ORDER BY identity ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: listing members for tenant %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []TenantMember
	for rows.Next() {
		var m TenantMember
		if err := rows.Scan(&m.TenantID, &m.Identity, &m.Role); err != nil {
			return nil, fmt.Errorf("store: scanning member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing members for tenant %s: %w", tenantID, err)
	}
	return out, nil
}

// MemberRole returns identity's role in tenantID, or ErrNotFound if identity is
// not a member. This is the server-side authority for the approver check: a
// member can never approve, only an approver can.
func (r *TenantRepo) MemberRole(ctx context.Context, tenantID, identity string) (string, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT role FROM tenant_members WHERE tenant_id = ? AND identity = ?`, tenantID, identity)
	var role string
	if err := row.Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("store: reading member role: %w", err)
	}
	return role, nil
}

// TenantsForIdentity returns every tenant membership an identity holds, sorted
// by tenant id. The scoping middleware calls this on every request to resolve
// which tenant (if any) the caller acts within.
func (r *TenantRepo) TenantsForIdentity(ctx context.Context, identity string) ([]TenantMember, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT tenant_id, identity, role FROM tenant_members WHERE identity = ? ORDER BY tenant_id ASC`, identity)
	if err != nil {
		return nil, fmt.Errorf("store: listing tenants for identity %q: %w", identity, err)
	}
	defer func() { _ = rows.Close() }()
	var out []TenantMember
	for rows.Next() {
		var m TenantMember
		if err := rows.Scan(&m.TenantID, &m.Identity, &m.Role); err != nil {
			return nil, fmt.Errorf("store: scanning member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing tenants for identity %q: %w", identity, err)
	}
	return out, nil
}

// InsertRequest records the tenant linkage of a request-changeset.
func (r *TenantRepo) InsertRequest(ctx context.Context, req ChangesetRequest) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_requests (changeset_id, tenant_id, requested_by, created_at, approved_by, approved_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		req.ChangesetID, req.TenantID, req.RequestedBy, req.CreatedAt, req.ApprovedBy, req.ApprovedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting changeset request %s: %w", req.ChangesetID, err)
	}
	return nil
}

// GetRequest returns the tenant linkage for a changeset id, or ErrNotFound if
// the changeset is not a request-changeset.
func (r *TenantRepo) GetRequest(ctx context.Context, changesetID string) (ChangesetRequest, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT changeset_id, tenant_id, requested_by, created_at, approved_by, approved_at
		FROM changeset_requests WHERE changeset_id = ?`, changesetID)
	var req ChangesetRequest
	if err := row.Scan(&req.ChangesetID, &req.TenantID, &req.RequestedBy, &req.CreatedAt, &req.ApprovedBy, &req.ApprovedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChangesetRequest{}, ErrNotFound
		}
		return ChangesetRequest{}, fmt.Errorf("store: scanning changeset request: %w", err)
	}
	return req, nil
}

// MarkRequestApproved records who approved a request-changeset and when.
// Returns ErrNotFound if the changeset is not a request-changeset.
func (r *TenantRepo) MarkRequestApproved(ctx context.Context, changesetID, approvedBy string, approvedAt int64) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE changeset_requests SET approved_by = ?, approved_at = ? WHERE changeset_id = ?`,
		approvedBy, approvedAt, changesetID,
	)
	if err != nil {
		return fmt.Errorf("store: marking request %s approved: %w", changesetID, err)
	}
	return checkRowAffected(res, "store: marking request %s approved", changesetID)
}
