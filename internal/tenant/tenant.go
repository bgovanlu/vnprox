// SPDX-License-Identifier: Apache-2.0

// Package tenant implements T-1703's multi-tenancy & self-service: delegated,
// server-side-scoped views over the federation-era permission model, and the
// request-changeset approval workflow.
//
// SECURITY (docs/security.md, Tenant authorization). Tenant scoping is enforced
// SERVER-SIDE at the data-access layer, never only in the UI. The authority for
// "what may this principal see" and "what is this principal's role" is the
// tenant store (internal/store's tenants/tenant_scopes/tenant_members tables),
// resolved fresh on every request from the authenticated identity — never from
// any client-supplied value. A Scope is the resolved, immutable set of visible
// resource Refs plus the caller's per-tenant role; every tenant-scoped read
// filters against Scope.Visible, and an out-of-scope direct Ref lookup returns
// not-found (existence is not confirmed), exactly the enforcement-point pattern
// internal/auth.forceReadOnly already uses for read_only mode.
//
// This package composes already-frozen primitives (the store, the change
// engine's lifecycle, T-1005's alert routing seam) rather than inventing a new
// core: multi-tenancy narrows WHAT a principal may touch; it never adds a
// mutation path around the change engine. A request-changeset is an ordinary
// changeset in StatusRequested that an approver converts to a draft, after
// which it flows staged->validated->diffed->applied->confirmed like any other.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// Store is the subset of *store.TenantRepo this package needs. Declared as an
// interface so the Service is unit-testable without a real SQLite file; the
// concrete *store.TenantRepo satisfies it directly.
type Store interface {
	ScopesForTenant(ctx context.Context, tenantID string) ([]string, error)
	TenantsForIdentity(ctx context.Context, identity string) ([]store.TenantMember, error)
	MemberRole(ctx context.Context, tenantID, identity string) (string, error)
	MembersForTenant(ctx context.Context, tenantID string) ([]store.TenantMember, error)
	GetTenant(ctx context.Context, id string) (store.Tenant, error)
	GetRequest(ctx context.Context, changesetID string) (store.ChangesetRequest, error)
	InsertRequest(ctx context.Context, req store.ChangesetRequest) error
	MarkRequestApproved(ctx context.Context, changesetID, approvedBy string, approvedAt int64) error
}

// Expander resolves a coarse tenant scope Ref (a VLAN/VNet the admin scoped the
// tenant to) into the concrete member Refs it implies (that VNet's guests and
// subnets), live against the inventory graph — so scoping a tenant to one VLAN
// makes exactly that VLAN's guests/subnets visible (T-1703 AC1) without the
// admin enumerating every member by hand, and without ever freezing the
// membership into the store. A nil Expander (Service.expander) means no
// expansion: the visible set is exactly the stored scope Refs. The returned set
// MUST include the input refs themselves.
type Expander interface {
	ExpandScopeRefs(ctx context.Context, scopeRefs []string) (map[string]bool, error)
}

// Scope is the resolved, server-derived visibility of one authenticated
// principal: the concrete set of Refs it may see (unioned across every tenant
// it is a member of) plus its role in each of those tenants. It is constructed
// only by Service.ScopeFor from server-side state; nothing a client sends ever
// widens it.
type Scope struct {
	visible   map[string]bool
	roles     map[string]string // tenantID -> role
	tenantIDs []string
}

// Visible reports whether ref is within this scope. The empty Ref and any Ref
// not in the resolved set are never visible — fail-closed.
func (s Scope) Visible(ref string) bool {
	if ref == "" {
		return false
	}
	return s.visible[ref]
}

// VisibleAny reports whether any of the candidate keys is within this scope.
// Used where a read surface can be keyed several equivalent ways (e.g. an IPAM
// subnet identifiable by bare CIDR or by its inventory Ref string) — a resource
// is visible if the tenant's scope names it under any of those forms.
func (s Scope) VisibleAny(keys ...string) bool {
	for _, k := range keys {
		if s.Visible(k) {
			return true
		}
	}
	return false
}

// TenantIDs returns the tenant ids this principal is a member of, sorted.
func (s Scope) TenantIDs() []string { return s.tenantIDs }

// Includes reports whether the principal is a member of tenantID.
func (s Scope) Includes(tenantID string) bool { _, ok := s.roles[tenantID]; return ok }

// RoleIn returns the principal's role in tenantID, or "" if not a member.
func (s Scope) RoleIn(tenantID string) string { return s.roles[tenantID] }

// VisibleRefs returns the resolved visible-Ref set, sorted — for callers that
// need to enumerate (e.g. the scoped dashboard's tile counts) rather than test
// membership. The returned slice is a copy.
func (s Scope) VisibleRefs() []string {
	out := make([]string, 0, len(s.visible))
	for ref := range s.visible {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

// Service is the tenant authorization + request-changeset workflow service.
type Service struct {
	store    Store
	expander Expander
	now      func() int64
}

// Config configures a Service. Store is required; Expander is optional (nil =
// no coarse-scope expansion); Now defaults to time.Now().Unix().
type Config struct {
	Store    Store
	Expander Expander
	Now      func() int64
}

// NewService constructs a Service.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("tenant: Config.Store is required")
	}
	now := cfg.Now
	if now == nil {
		now = defaultNow
	}
	return &Service{store: cfg.Store, expander: cfg.Expander, now: now}, nil
}

// ScopeFor resolves the tenant Scope of an authenticated principal. ok is false
// when the identity is not a member of any tenant — such a principal is an
// ordinary (admin/operator) user and its reads are NOT tenant-scoped (the
// scoping middleware then passes through unchanged, preserving every existing
// non-tenant caller's behavior). When ok is true the returned Scope is the
// union of every tenant the principal belongs to, with coarse scopes expanded
// to their concrete members.
func (s *Service) ScopeFor(ctx context.Context, identity string) (Scope, bool, error) {
	if identity == "" {
		return Scope{}, false, nil
	}
	memberships, err := s.store.TenantsForIdentity(ctx, identity)
	if err != nil {
		return Scope{}, false, fmt.Errorf("tenant: resolving memberships for %q: %w", identity, err)
	}
	if len(memberships) == 0 {
		return Scope{}, false, nil
	}

	scope := Scope{visible: map[string]bool{}, roles: map[string]string{}}
	for _, m := range memberships {
		scope.roles[m.TenantID] = m.Role
		scope.tenantIDs = append(scope.tenantIDs, m.TenantID)

		refs, err := s.store.ScopesForTenant(ctx, m.TenantID)
		if err != nil {
			return Scope{}, false, fmt.Errorf("tenant: resolving scopes for %s: %w", m.TenantID, err)
		}
		expanded := refsToSet(refs)
		if s.expander != nil && len(refs) > 0 {
			set, err := s.expander.ExpandScopeRefs(ctx, refs)
			if err != nil {
				return Scope{}, false, fmt.Errorf("tenant: expanding scope for %s: %w", m.TenantID, err)
			}
			expanded = set
		}
		for ref := range expanded {
			if ref != "" {
				scope.visible[ref] = true
			}
		}
	}
	sort.Strings(scope.tenantIDs)
	return scope, true, nil
}

// ScopeForTenant resolves the visible-Ref scope of a SINGLE tenant for a
// principal, verifying membership. ok is false when identity is not a member of
// tenantID. Unlike ScopeFor (which unions every tenant the caller belongs to),
// this is scoped to exactly one tenant — used to validate that a
// request-changeset a member raises against tenantID touches only that tenant's
// resources, never another tenant it happens to also belong to.
func (s *Service) ScopeForTenant(ctx context.Context, identity, tenantID string) (Scope, bool, error) {
	role, err := s.store.MemberRole(ctx, tenantID, identity)
	if err != nil {
		if isNotFound(err) {
			return Scope{}, false, nil
		}
		return Scope{}, false, fmt.Errorf("tenant: reading role for %q in %s: %w", identity, tenantID, err)
	}
	refs, err := s.store.ScopesForTenant(ctx, tenantID)
	if err != nil {
		return Scope{}, false, fmt.Errorf("tenant: resolving scopes for %s: %w", tenantID, err)
	}
	expanded := refsToSet(refs)
	if s.expander != nil && len(refs) > 0 {
		set, err := s.expander.ExpandScopeRefs(ctx, refs)
		if err != nil {
			return Scope{}, false, fmt.Errorf("tenant: expanding scope for %s: %w", tenantID, err)
		}
		expanded = set
	}
	scope := Scope{visible: expanded, roles: map[string]string{tenantID: role}, tenantIDs: []string{tenantID}}
	return scope, true, nil
}

// ApprovalDecision is the outcome of an approver-check on a request-changeset.
type ApprovalDecision struct {
	// TenantID is the tenant that owns the request-changeset (set on Allowed).
	TenantID string
	// RequestedBy is the member who raised the request.
	RequestedBy string
	// Allowed is true only when identity may approve: it is an approver of the
	// owning tenant AND not the member who raised the request (separation of
	// duties). Everything else is false.
	Allowed bool
	// NotARequest is true when changesetID is not a request-changeset at all
	// (no changeset_requests row) — the caller maps this to 404, not 403, so
	// approving a non-request changeset never confirms its existence to a
	// principal outside its tenant.
	NotARequest bool
}

// CanApprove decides whether identity may approve the request-changeset
// changesetID. This is the server-side role check T-1703 AC2 requires: a plain
// member (role "member") is always denied; an approver of the owning tenant is
// allowed UNLESS they themselves raised the request (a tenant member — even an
// approver — can never approve their own request). A caller outside the owning
// tenant (MemberRole ErrNotFound) is denied without revealing the request.
func (s *Service) CanApprove(ctx context.Context, changesetID, identity string) (ApprovalDecision, error) {
	req, err := s.store.GetRequest(ctx, changesetID)
	if err != nil {
		if isNotFound(err) {
			return ApprovalDecision{NotARequest: true}, nil
		}
		return ApprovalDecision{}, fmt.Errorf("tenant: loading request %s: %w", changesetID, err)
	}
	dec := ApprovalDecision{TenantID: req.TenantID, RequestedBy: req.RequestedBy}

	role, err := s.store.MemberRole(ctx, req.TenantID, identity)
	if err != nil {
		if isNotFound(err) {
			return dec, nil // not a member of the owning tenant: denied, but a real request
		}
		return ApprovalDecision{}, fmt.Errorf("tenant: reading role for %q in %s: %w", identity, req.TenantID, err)
	}
	if role != store.TenantRoleApprover {
		return dec, nil // a plain member may never approve
	}
	if identity == req.RequestedBy {
		return dec, nil // separation of duties: never approve your own request
	}
	dec.Allowed = true
	return dec, nil
}

// RecordRequest links a freshly-created request-changeset to its tenant. The
// caller (the tenant request route) has already created the changeset via
// change.Service.CreateRequest; this records the tenant/requester linkage the
// changesets table has no column for.
func (s *Service) RecordRequest(ctx context.Context, changesetID, tenantID, requestedBy string) error {
	return s.store.InsertRequest(ctx, store.ChangesetRequest{
		ChangesetID: changesetID, TenantID: tenantID, RequestedBy: requestedBy, CreatedAt: s.now(),
	})
}

// MarkApproved records who approved a request-changeset and when, after the
// change engine has converted it to a draft.
func (s *Service) MarkApproved(ctx context.Context, changesetID, approver string) error {
	return s.store.MarkRequestApproved(ctx, changesetID, approver, s.now())
}

// IsMemberOf reports whether identity is a member (any role) of tenantID.
func (s *Service) IsMemberOf(ctx context.Context, tenantID, identity string) (bool, error) {
	_, err := s.store.MemberRole(ctx, tenantID, identity)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("tenant: checking membership of %q in %s: %w", identity, tenantID, err)
	}
	return true, nil
}

// Approvers returns the identities that hold the approver role in tenantID —
// the routing target for a pending request-changeset's approval notification.
func (s *Service) Approvers(ctx context.Context, tenantID string) ([]string, error) {
	members, err := s.store.MembersForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenant: listing members of %s: %w", tenantID, err)
	}
	var out []string
	for _, m := range members {
		if m.Role == store.TenantRoleApprover {
			out = append(out, m.Identity)
		}
	}
	sort.Strings(out)
	return out, nil
}

func refsToSet(refs []string) map[string]bool {
	set := make(map[string]bool, len(refs))
	for _, r := range refs {
		if r != "" {
			set[r] = true
		}
	}
	return set
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

func defaultNow() int64 { return time.Now().Unix() }
