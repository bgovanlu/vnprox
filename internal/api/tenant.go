// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/tenant"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// tenant.go implements T-1703's server-side multi-tenant scoping and the
// request-changeset approval workflow's HTTP surface.
//
// SECURITY — the enforcement point. Tenant scoping is enforced here,
// server-side, at the data-access layer: tenantScopeMiddleware resolves the
// authenticated principal's tenant Scope from the store (never from any
// client-supplied value) and attaches it to the request context; every
// tenant-scoped read handler (topology/findings/ipam/flows, and — since
// T-3002-followup-01 — the /tenants admin CRUD reads themselves) then
// filters its in-memory projection through that Scope BEFORE serialization,
// and an out-of-scope direct-Ref/tenant-id lookup returns 404 (existence is
// not confirmed), mirroring internal/auth.forceReadOnly's enforcement-point
// pattern. A caller who is not a tenant member gets no Scope attached and
// reads unscoped, exactly as before — multi-tenancy only ever NARROWS a
// member's view.

// TenantScoper resolves an authenticated principal's tenant Scope. The concrete
// *tenant.Service satisfies it. nil-safe at the router level (no scoping
// middleware is mounted, every caller reads unscoped).
type TenantScoper interface {
	ScopeFor(ctx context.Context, identity string) (tenant.Scope, bool, error)
	ScopeForTenant(ctx context.Context, identity, tenantID string) (tenant.Scope, bool, error)
	CanApprove(ctx context.Context, changesetID, identity string) (tenant.ApprovalDecision, error)
	RecordRequest(ctx context.Context, changesetID, tenantID, requestedBy string) error
	MarkApproved(ctx context.Context, changesetID, approver string) error
	Approvers(ctx context.Context, tenantID string) ([]string, error)
	IsMemberOf(ctx context.Context, tenantID, identity string) (bool, error)
}

// TenantAdminStore is the tenant CRUD backend (admin routes). *store.TenantRepo
// satisfies it.
type TenantAdminStore interface {
	InsertTenant(ctx context.Context, t store.Tenant) error
	GetTenant(ctx context.Context, id string) (store.Tenant, error)
	ListTenants(ctx context.Context) ([]store.Tenant, error)
	DeleteTenant(ctx context.Context, id string) error
	AddScope(ctx context.Context, tenantID, scopeRef string) error
	RemoveScope(ctx context.Context, tenantID, scopeRef string) error
	ScopesForTenant(ctx context.Context, tenantID string) ([]string, error)
	PutMember(ctx context.Context, m store.TenantMember) error
	RemoveMember(ctx context.Context, tenantID, identity string) error
	MembersForTenant(ctx context.Context, tenantID string) ([]store.TenantMember, error)
}

// ApprovalNotice is the routed notification raised when a request-changeset is
// created (T-1703's approval routing, reusing T-1005's alert plumbing).
type ApprovalNotice struct {
	TenantID    string
	TenantName  string
	ChangesetID string
	RequestedBy string
	Title       string
	Approvers   []string
}

// ApprovalNotifier routes a pending-approval notice to the tenant's approver
// group. The concrete adapter (cmd/vnproxd) bridges to T-1005's alert routing;
// nil-safe (a request-changeset is still created, just un-notified).
type ApprovalNotifier interface {
	NotifyApprovalPending(ctx context.Context, notice ApprovalNotice) error
}

// ---- scope context plumbing ---------------------------------------------

type scopeCtxKey int

const scopeKey scopeCtxKey = 0

func contextWithScope(ctx context.Context, s tenant.Scope) context.Context {
	return context.WithValue(ctx, scopeKey, s)
}

// scopeFromContext returns the tenant Scope attached by tenantScopeMiddleware,
// if the caller is a tenant member. ok is false for an unscoped (ordinary)
// caller — handlers then pass their full response through unchanged.
func scopeFromContext(ctx context.Context) (tenant.Scope, bool) {
	s, ok := ctx.Value(scopeKey).(tenant.Scope)
	return s, ok
}

// tenantScopeMiddleware resolves the caller's tenant Scope (server-side, from
// the store) and attaches it to the request context. It runs AFTER
// SessionMiddleware (it reads the authenticated identity via lookup). A caller
// who is not a tenant member gets no Scope attached — reads pass through
// unscoped, so every existing non-tenant caller is unaffected. A resolution
// error fails closed (the request proceeds with NO scope attached only when the
// caller is genuinely a non-member; a store error is surfaced as 500 rather
// than silently granting an unscoped — i.e. unfiltered — view).
func tenantScopeMiddleware(scoper TenantScoper, lookup UsernameLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, ok := lookup.Username(r.Context())
			if !ok || username == "" {
				next.ServeHTTP(w, r) // unauthenticated paths are already rejected upstream
				return
			}
			scope, isMember, err := scoper.ScopeFor(r.Context(), username)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not resolve tenant scope")
				return
			}
			if isMember {
				r = r.WithContext(contextWithScope(r.Context(), scope))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tenantWSGuard denies the /api/ws upgrade for a tenant-scoped principal
// (T-1703, fail-closed). The WS topology.delta feed is cluster-wide and NOT yet
// filtered per subscriber, so a tenant member subscribing would receive
// unscoped cross-tenant deltas — the exact leak this card must not ship. Any
// principal that resolves to a tenant scope is refused with 403; a
// non-tenant/admin principal passes through and gets the feed as before. This
// is the same server-side scope resolution the REST scoping middleware uses. A
// resolution error fails closed (500) rather than risk an unfiltered upgrade.
func tenantWSGuard(scoper TenantScoper, lookup UsernameLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, ok := lookup.Username(r.Context())
			if !ok || username == "" {
				next.ServeHTTP(w, r) // unauthenticated is already rejected upstream
				return
			}
			_, isMember, err := scoper.ScopeFor(r.Context(), username)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not resolve tenant scope")
				return
			}
			if isMember {
				writeJSONError(w, http.StatusForbidden, "forbidden",
					"the live topology feed is not yet tenant-scoped; use the REST read routes, which are")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- response filters (data-access-layer enforcement) -------------------

// filterTopologyForScope narrows a projected Topology to the tenant's visible
// Refs: only nodes whose id (a Ref string) is visible survive, and an edge
// survives only if BOTH endpoints do. This is applied to the in-memory
// projection before serialization — a client can never see a filtered-out
// node/edge in any form.
func filterTopologyForScope(t topology.Topology, scope tenant.Scope) topology.Topology {
	nodes := make([]topology.Node, 0, len(t.Nodes))
	visible := make(map[string]bool, len(t.Nodes))
	for _, n := range t.Nodes {
		if scope.Visible(n.ID) {
			nodes = append(nodes, n)
			visible[n.ID] = true
		}
	}
	edges := make([]topology.Edge, 0, len(t.Edges))
	for _, e := range t.Edges {
		if visible[e.From] && visible[e.To] {
			edges = append(edges, e)
		}
	}
	t.Nodes = nodes
	t.Edges = edges
	return t
}

// filterFindingsForScope keeps only findings that reference at least one
// visible Ref. A finding with no refs at all (a node/cluster-wide health
// finding) is NOT shown to a tenant — a tenant's view is strictly its own
// scoped resources, never cluster-wide state.
func filterFindingsForScope(items []findings.Finding, scope tenant.Scope) []findings.Finding {
	out := make([]findings.Finding, 0, len(items))
	for _, f := range items {
		if anyRefVisible(f.Refs, scope) {
			out = append(out, f)
		}
	}
	return out
}

func anyRefVisible(refs []string, scope tenant.Scope) bool {
	for _, ref := range refs {
		if scope.Visible(ref) {
			return true
		}
	}
	return false
}

// subnetVisible reports whether an IPAM subnet is within scope. An IPAM subnet
// carries no inventory Ref of its own, so it is matched under any of the
// equivalent keys a tenant scope might name it by: the bare CIDR, or the
// SdnSubnet Ref forms.
func subnetVisible(sub ipam.Subnet, scope tenant.Scope) bool {
	keys := []string{
		sub.CIDR,
		"sdn-subnet::" + sub.CIDR,
	}
	if sub.Vnet != "" {
		keys = append(keys, "sdn-subnet::"+sub.Vnet+"/"+sub.CIDR)
	}
	return scope.VisibleAny(keys...)
}

// filterSubnetsForScope narrows an IPAM subnets response to visible subnets.
func filterSubnetsForScope(resp ipam.SubnetsResponse, scope tenant.Scope) ipam.SubnetsResponse {
	items := make([]ipam.Subnet, 0, len(resp.Items))
	for _, s := range resp.Items {
		if subnetVisible(s, scope) {
			items = append(items, s)
		}
	}
	resp.Items = items
	return resp
}

// filterFlowsForScope keeps only flows whose source OR destination Ref is
// visible to the tenant. A flow with neither endpoint resolved to a visible
// Ref is never shown.
func filterFlowsForScope(items []flowRecordResponse, scope tenant.Scope) []flowRecordResponse {
	out := make([]flowRecordResponse, 0, len(items))
	for _, it := range items {
		if scope.Visible(it.SrcRef) || scope.Visible(it.DstRef) {
			out = append(out, it)
		}
	}
	return out
}

// ---- tenant CRUD + request/approve + dashboard routes -------------------

// mountTenantRoutes registers the tenant-management admin routes, the
// request-changeset approve route, and the scoped dashboard. Nil-safe like
// every other mount function: a nil store or auth skips the whole family.
func mountTenantRoutes(r chi.Router, adminStore TenantAdminStore, scoper TenantScoper, changesets ChangesetService, notifier ApprovalNotifier, auth AuthService) {
	if adminStore == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	// Admin CRUD: reads require netRead, mutations require netWrite (+ CSRF).
	//
	// SECURITY (T-3002-followup-01, 2026-08-19): reads are ALSO
	// tenant-scoped, the same as /topology, /findings, /ipam/*, /flows, and
	// inventory detail/search — netRead alone is not an admin capability
	// (every tenant member holds it, since it derives from ordinary PVE
	// network-read ACLs), so without tenantScopeMiddleware here any member
	// of any tenant could enumerate every tenant and read another tenant's
	// scopes/members. A caller who is not a tenant member (isMember=false
	// from ScopeFor) gets no Scope attached and reads unscoped, exactly
	// like every other route this middleware guards — multi-tenancy only
	// ever narrows a member's view.
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		if scoper != nil {
			r.Use(tenantScopeMiddleware(scoper, lookup))
		}
		r.Get("/tenants", handleListTenants(adminStore))
		r.Get("/tenants/{id}", handleGetTenant(adminStore))
	})
	// SECURITY (T-3002-followup-02, 2026-08-19): mutations are ALSO
	// tenant-scoped, closing the gap T-3002-followup-01 left open on purpose
	// (that decision covered reads only). Before this fix, every mutation
	// below was gated on netWrite alone — no membership check at all — so a
	// caller holding netWrite and membership in ONE tenant could rewrite ANY
	// tenant's scopes/members, not just their own. tenantScopeMiddleware here
	// gives handlers the same resolved Scope the read routes use, and each
	// handler below applies tenantMutationScope: a caller who is a member of
	// some OTHER tenant than the one named in the URL gets 404 (never 403 —
	// a 403 would still confirm the foreign tenant exists, same "not found,
	// not forbidden" stance the reads take). An unscoped caller (not a member
	// of any tenant — the ordinary fleet-admin persona netWrite was modelled
	// on) is unaffected and can still mutate any tenant, exactly as before.
	//
	// PUT/DELETE .../scopes are held to a stricter rule than the rest: they
	// are refused for ANY tenant-scoped caller, including a member mutating
	// their OWN tenant (404 for a foreign tenant, 403 for their own). This is
	// deliberate, not an oversight of the membership-scoping pattern above —
	// see this function's closing comment for the reasoning.
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		if scoper != nil {
			r.Use(tenantScopeMiddleware(scoper, lookup))
		}
		r.Post("/tenants", handleCreateTenant(adminStore, lookup))
		r.Delete("/tenants/{id}", handleDeleteTenant(adminStore))
		r.Put("/tenants/{id}/scopes", handlePutScope(adminStore))
		r.Delete("/tenants/{id}/scopes", handleDeleteScope(adminStore))
		r.Put("/tenants/{id}/members", handlePutMember(adminStore))
		r.Delete("/tenants/{id}/members/{identity}", handleDeleteMember(adminStore))
	})

	// Approve is a tenant-member-authenticated action (netWrite gates the
	// underlying draft the approval unlocks); the "an approver, not the
	// requester" check is enforced server-side by scoper.CanApprove.
	if scoper != nil && changesets != nil {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			if csrf, ok := auth.(CSRFEnforcer); ok {
				r.Use(csrf.CSRFMiddleware)
			}
			r.Use(auth.RequireCap(capNetWrite))
			r.Post("/changesets/{id}/approve", handleApproveChangeset(scoper, changesets, lookup))
		})

		// Scoped dashboard: netRead + the same scope middleware the read
		// routes use.
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			r.Use(auth.RequireCap(capNetRead))
			r.Use(tenantScopeMiddleware(scoper, lookup))
			r.Get("/dashboard", handleTenantDashboard(scoper, lookup))
		})
	}

	_ = notifier // notifier is consumed by the request-changeset create path (changesets.go)
}

// tenantMutationScope reports how a mutation targeting tenantID should be
// gated, given the caller's tenant Scope (or lack of one) already resolved
// onto the request context by tenantScopeMiddleware.
//
//   - scoped=false: the caller is not a member of any tenant — the ordinary
//     fleet-admin persona netWrite's mutation gate was modelled on before
//     T-3002-followup-02. Unaffected: the handler proceeds exactly as before
//     this fix.
//   - scoped=true, member=false: the caller IS a tenant member, but of some
//     tenant OTHER than tenantID. The handler must answer 404, never 403 —
//     a 403 would confirm tenantID exists, which is exactly what the read
//     side (T-3002-followup-01) already refuses to do.
//   - scoped=true, member=true: the caller is a member of tenantID itself.
func tenantMutationScope(ctx context.Context, tenantID string) (scoped, member bool) {
	scope, ok := scopeFromContext(ctx)
	if !ok {
		return false, false
	}
	return true, scope.Includes(tenantID)
}

type createTenantRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

type tenantResponse struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	CreatedBy string               `json:"createdBy"`
	Scopes    []string             `json:"scopes"`
	Members   []tenantMemberOutput `json:"members"`
	CreatedAt int64                `json:"createdAt"`
}

type tenantMemberOutput struct {
	Identity string `json:"identity"`
	Role     string `json:"role"`
}

// POST /tenants (create) has no existing tenant to be a member of, so
// membership cannot gate it the way tenantMutationScope gates the other five
// mutation routes — it needs its own rule.
//
// The rule: creation is refused for ANY tenant-scoped caller (403 — there is
// no tenant identity to hide behind here, so unlike the membership-scoped
// routes this is a plain capability check, not an existence check), and left
// to netWrite holders who are not a member of any tenant — the same unscoped
// "fleet admin" persona every other mutation route in this file already
// treats as the trusted, unaffected case. Reasoning:
//
//   - Tenant creation defines a brand-new boundary from nothing. Unlike
//     DELETE /tenants/{id} or PUT/DELETE .../members (self-service actions
//     confined to a boundary an admin already drew), there is no existing
//     scope for membership to narrow against — "may create tenants" reads as
//     a fleet-administration capability, not a per-tenant one, and
//     docs/api.md has always titled this whole route family "Tenant
//     management (admin)".
//   - handleCreateTenant does NOT add the creator as a member of the tenant
//     it creates (never did — verified by reading it before this change: no
//     PutMember call anywhere in this function). Combined with the
//     create-time restriction above, "create a tenant, then reach it as a
//     member" is closed categorically: a tenant-scoped caller can never
//     create a tenant to begin with, so there is never a tenant they just
//     made themselves a member of to widen.
//
// See handlePutScope/handleDeleteScope below for the closely related second
// half of this reasoning: even a genuine member of an EXISTING tenant may
// not widen that tenant's own scope refs, for the same "boundary-setting
// stays with the fleet admin" reason — otherwise a member could reach the
// same privilege escalation this rule closes off at creation, just one step
// later (join/be-added-to a tenant, then scope it onto resources they were
// never granted).
func handleCreateTenant(s TenantAdminStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		if _, isScoped := scopeFromContext(r.Context()); isScoped {
			writeJSONError(w, http.StatusForbidden, "forbidden",
				"tenant creation is a fleet-admin action; a tenant member may not create tenants")
			return
		}
		var req createTenantRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "tenant name is required")
			return
		}
		id := req.ID
		if id == "" {
			id = store.NewULID()
		}
		t := store.Tenant{ID: id, Name: req.Name, CreatedBy: username, CreatedAt: time.Now().Unix()}
		if err := s.InsertTenant(r.Context(), t); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create tenant")
			return
		}
		writeJSON(w, http.StatusCreated, tenantResponse{ID: t.ID, Name: t.Name, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, Scopes: []string{}, Members: []tenantMemberOutput{}})
	}
}

// tenantAdminRow loads one store.Tenant's scopes/members and shapes it into
// a tenantResponse. Unlike the pre-T-3002-followup-01 handleListTenants,
// this genuinely queries both tables rather than hard-coding empty arrays —
// docs/api.md documents the Tenant shape as carrying both, and a caller
// could not previously tell "this tenant has no scopes" from "this endpoint
// does not report scopes" (pinned, before the fix, by
// TestListTenantsReportsEmptyScopesWithoutReadingThem).
func tenantAdminRow(ctx context.Context, s TenantAdminStore, t store.Tenant) (tenantResponse, error) {
	scopes, err := s.ScopesForTenant(ctx, t.ID)
	if err != nil {
		return tenantResponse{}, fmt.Errorf("loading scopes for tenant %s: %w", t.ID, err)
	}
	if scopes == nil {
		scopes = []string{}
	}
	members, err := s.MembersForTenant(ctx, t.ID)
	if err != nil {
		return tenantResponse{}, fmt.Errorf("loading members for tenant %s: %w", t.ID, err)
	}
	mo := make([]tenantMemberOutput, 0, len(members))
	for _, m := range members {
		mo = append(mo, tenantMemberOutput{Identity: m.Identity, Role: m.Role})
	}
	return tenantResponse{ID: t.ID, Name: t.Name, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, Scopes: scopes, Members: mo}, nil
}

// handleListTenants lists tenants, scoped to the caller's own membership
// (T-3002-followup-01): a caller who is a tenant member sees only the
// tenants named by their resolved Scope; an unscoped (non-member) caller
// sees every tenant, unchanged from before this fix.
func handleListTenants(s TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenants, err := s.ListTenants(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list tenants")
			return
		}
		if scope, ok := scopeFromContext(r.Context()); ok {
			allowed := make(map[string]bool, len(scope.TenantIDs()))
			for _, id := range scope.TenantIDs() {
				allowed[id] = true
			}
			filtered := make([]store.Tenant, 0, len(tenants))
			for _, t := range tenants {
				if allowed[t.ID] {
					filtered = append(filtered, t)
				}
			}
			tenants = filtered
		}
		out := make([]tenantResponse, 0, len(tenants))
		for _, t := range tenants {
			row, err := tenantAdminRow(r.Context(), s, t)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load tenant scopes/members")
				return
			}
			out = append(out, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// handleGetTenant returns one tenant, scoped to the caller's own membership
// (T-3002-followup-01): a caller who is a tenant member but NOT of this
// tenant gets 404 — never 403, so the response never confirms a tenant they
// don't belong to exists (docs/user-guide.md:156's promise). An unscoped
// (non-member) caller can look up any tenant by id, unchanged from before
// this fix.
func handleGetTenant(s TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if scope, ok := scopeFromContext(r.Context()); ok && !scope.Includes(id) {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
			return
		}
		t, err := s.GetTenant(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load tenant")
			return
		}
		row, err := tenantAdminRow(r.Context(), s, t)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load tenant scopes/members")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

// handleDeleteTenant deletes one tenant, scoped to the caller's own
// membership (T-3002-followup-02, mirroring handleGetTenant): a caller who
// is a member of some OTHER tenant gets 404, never 403 — a 403 would confirm
// the foreign tenant exists. A member of THIS tenant may delete it (the same
// self-service latitude PUT/DELETE .../members below carries — deleting your
// own tenant narrows nobody else's boundary, it just retires your own). An
// unscoped caller is unaffected.
func handleDeleteTenant(s TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if scoped, member := tenantMutationScope(r.Context(), id); scoped && !member {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
			return
		}
		if err := s.DeleteTenant(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete tenant")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type scopeRequest struct {
	ScopeRef string `json:"scopeRef"`
}

// handlePutScope adds a visible-resource ref to a tenant's boundary.
//
// SECURITY (T-3002-followup-02): unlike the other mutation routes, this one
// is refused for EVERY tenant-scoped caller, including a member acting on
// their OWN tenant — 404 for a foreign tenant (existence not confirmed, same
// as every other route in this family), 403 for their own (there is nothing
// left to hide; the caller already knows their own tenant exists). Only an
// unscoped (fleet-admin) netWrite holder may add a scope ref.
//
// Why stricter than membership scoping: scopeRef is never validated against
// anything — AddScope stores whatever ref the caller sends, verbatim, with
// no check that the ref names a resource the caller (or anyone) is otherwise
// entitled to see. If a plain member could call this on their own tenant,
// they could hand their OWN tenant visibility into any resource on the
// cluster, including another tenant's exclusive scope — reaching resources
// through their tenant's Scope that they were never granted any other way.
// That is a privilege escalation the membership-scoping pattern used
// elsewhere in this file does not close by itself, so scope-boundary writes
// stay a fleet-admin action instead. See handleCreateTenant's comment for
// the related reasoning on tenant creation.
func handlePutScope(s TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if scoped, member := tenantMutationScope(r.Context(), id); scoped {
			if !member {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
				return
			}
			writeJSONError(w, http.StatusForbidden, "forbidden",
				"only a fleet admin, not a tenant member, may change a tenant's scope boundary")
			return
		}
		if err := ensureTenantExists(r.Context(), s, id, w); err != nil {
			return
		}
		var req scopeRequest
		if err := decodeJSONBody(w, r, &req); err != nil || strings.TrimSpace(req.ScopeRef) == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "scopeRef is required")
			return
		}
		if err := s.AddScope(r.Context(), id, req.ScopeRef); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not add scope")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteScope removes a scope ref. Held to the same fleet-admin-only
// rule as handlePutScope above, for symmetry — a member able to remove a
// scope ref from any tenant they belong to is a smaller concern than adding
// one (it only narrows), but gating add without gating remove would be an
// inconsistent, easily-misread boundary in the same route family.
func handleDeleteScope(s TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if scoped, member := tenantMutationScope(r.Context(), id); scoped {
			if !member {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
				return
			}
			writeJSONError(w, http.StatusForbidden, "forbidden",
				"only a fleet admin, not a tenant member, may change a tenant's scope boundary")
			return
		}
		ref := r.URL.Query().Get("scopeRef")
		if ref == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "scopeRef query param is required")
			return
		}
		if err := s.RemoveScope(r.Context(), id, ref); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not remove scope")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type memberRequest struct {
	Identity string `json:"identity"`
	Role     string `json:"role"`
}

// handlePutMember adds or promotes a member of a tenant, scoped to the
// caller's own membership (T-3002-followup-02, mirroring handleDeleteTenant):
// 404 for a foreign tenant, unaffected for an unscoped caller. A member of
// THIS tenant may manage its membership — self-service, and bounded: the
// scope refs a new member inherits are exactly the tenant's already-fixed
// boundary (handlePutScope above is fleet-admin-only precisely so this stays
// bounded), so adding or promoting a member here never reaches a resource
// the tenant could not already see.
func handlePutMember(s TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if scoped, member := tenantMutationScope(r.Context(), id); scoped && !member {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
			return
		}
		if err := ensureTenantExists(r.Context(), s, id, w); err != nil {
			return
		}
		var req memberRequest
		if err := decodeJSONBody(w, r, &req); err != nil || strings.TrimSpace(req.Identity) == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "identity is required")
			return
		}
		if req.Role != store.TenantRoleMember && req.Role != store.TenantRoleApprover {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "role must be member or approver")
			return
		}
		if err := s.PutMember(r.Context(), store.TenantMember{TenantID: id, Identity: req.Identity, Role: req.Role}); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not add member")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteMember removes a member, held to the same own-tenant
// self-service rule as handlePutMember above.
func handleDeleteMember(s TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if scoped, member := tenantMutationScope(r.Context(), id); scoped && !member {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
			return
		}
		identity := chi.URLParam(r, "identity")
		if unescaped, err := url.PathUnescape(identity); err == nil {
			identity = unescaped
		}
		if err := s.RemoveMember(r.Context(), id, identity); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not remove member")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleApproveChangeset converts a request-changeset to an ordinary draft
// (T-1703 AC2/AC3). The server-side authz — an approver of the owning tenant,
// never a plain member and never the requester — is enforced by
// scoper.CanApprove; an out-of-scope or non-request changeset is a 404, never a
// 403 that would confirm its existence.
func handleApproveChangeset(scoper TenantScoper, changesets ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		dec, err := scoper.CanApprove(r.Context(), id, username)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not evaluate approval")
			return
		}
		if dec.NotARequest {
			// Not a request-changeset (or nonexistent): 404, never revealing it.
			writeJSONError(w, http.StatusNotFound, "not_found", "no such request-changeset")
			return
		}
		if !dec.Allowed {
			writeJSONError(w, http.StatusForbidden, "forbidden", "only an approver of this tenant may approve, and never their own request")
			return
		}

		c, err := changesets.Approve(r.Context(), id, username)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		if markErr := scoper.MarkApproved(r.Context(), id, username); markErr != nil {
			// Non-fatal: the changeset is already a draft; the linkage bookkeeping
			// is best-effort audit metadata.
			_ = markErr
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

// tenantDashboardResponse is GET /dashboard?tenantId='s scoped tile counts.
type tenantDashboardResponse struct {
	TenantID    string `json:"tenantId"`
	VisibleRefs int    `json:"visibleRefs"`
	Guests      int    `json:"guests"`
	Subnets     int    `json:"subnets"`
	Vnets       int    `json:"vnets"`
}

// handleTenantDashboard returns the caller's scoped dashboard tile counts,
// computed purely from the tenant Scope already resolved by the middleware — so
// the counts reflect only the tenant's own scope, never cluster-wide totals
// (T-1703 AC6).
func handleTenantDashboard(scoper TenantScoper, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, ok := scopeFromContext(r.Context())
		if !ok {
			// A non-tenant caller has no scoped dashboard.
			writeJSONError(w, http.StatusForbidden, "forbidden", "not a tenant member")
			return
		}
		wantTenant := r.URL.Query().Get("tenantId")
		if wantTenant != "" && !scope.Includes(wantTenant) {
			// Asking for a tenant you don't belong to is a 404 (existence not
			// confirmed), not a 403.
			writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
			return
		}

		refs := scope.VisibleRefs()
		resp := tenantDashboardResponse{VisibleRefs: len(refs)}
		if len(scope.TenantIDs()) > 0 {
			resp.TenantID = scope.TenantIDs()[0]
		}
		if wantTenant != "" {
			resp.TenantID = wantTenant
		}
		for _, ref := range refs {
			switch {
			case strings.HasPrefix(ref, "guest:"):
				resp.Guests++
			case strings.HasPrefix(ref, "sdn-subnet:"):
				resp.Subnets++
			case strings.HasPrefix(ref, "sdn-vnet:"):
				resp.Vnets++
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// ---- request-changeset creation (called from handleCreateChangeset) -----

// createRequestChangeset handles the request-changeset branch of POST
// /changesets (a body carrying tenantId). It enforces, server-side: the caller
// is a member of tenantId, and EVERY op targets a Ref within that tenant's
// scope (an op with a zero/out-of-scope target is rejected — a tenant may only
// request changes to resources it can see). It then creates the changeset in
// StatusRequested via the change engine, records the tenant linkage, and routes
// the approval notification to the tenant's approvers.
func createRequestChangeset(w http.ResponseWriter, r *http.Request, svc ChangesetService, scoper TenantScoper, notifier ApprovalNotifier, adminStore TenantAdminStore, username, tenantID, title string, ops []change.Op) {
	scope, isMember, err := scoper.ScopeForTenant(r.Context(), username, tenantID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not resolve tenant scope")
		return
	}
	if !isMember {
		// Not a member of the named tenant: 404 (never confirm the tenant to a
		// non-member).
		writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
		return
	}
	for _, op := range ops {
		ref := op.Target.String()
		if op.Target.IsZero() || !scope.Visible(ref) {
			writeJSONError(w, http.StatusForbidden, "forbidden", "request touches a resource outside this tenant's scope")
			return
		}
	}

	c, err := svc.CreateRequest(r.Context(), username, title, ops)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create request-changeset")
		return
	}
	if err := scoper.RecordRequest(r.Context(), c.ID, tenantID, username); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not record tenant request")
		return
	}
	routeApprovalNotice(r.Context(), scoper, notifier, adminStore, tenantID, c.ID, username, title)
	writeJSON(w, http.StatusCreated, toChangesetResponse(c))
}

// routeApprovalNotice raises the pending-approval notification to the tenant's
// approver group (T-1005 alert routing). Best-effort: a delivery failure never
// fails the request-changeset creation.
func routeApprovalNotice(ctx context.Context, scoper TenantScoper, notifier ApprovalNotifier, adminStore TenantAdminStore, tenantID, changesetID, requestedBy, title string) {
	if notifier == nil {
		return
	}
	approvers, err := scoper.Approvers(ctx, tenantID)
	if err != nil {
		return
	}
	name := tenantID
	if adminStore != nil {
		if t, gerr := adminStore.GetTenant(ctx, tenantID); gerr == nil {
			name = t.Name
		}
	}
	_ = notifier.NotifyApprovalPending(ctx, ApprovalNotice{
		TenantID: tenantID, TenantName: name, ChangesetID: changesetID,
		RequestedBy: requestedBy, Title: title, Approvers: approvers,
	})
}

// ---- helpers ------------------------------------------------------------

func ensureTenantExists(ctx context.Context, s TenantAdminStore, id string, w http.ResponseWriter) error {
	if _, err := s.GetTenant(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such tenant")
			return err
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load tenant")
		return err
	}
	return nil
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
