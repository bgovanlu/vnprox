// SPDX-License-Identifier: Apache-2.0

package tenant

import "sort"

// oidc.go maps OIDC group claims to tenant memberships (T-1703 AC5), the same
// "group claim -> vnprox concept" pattern internal/auth's MapGroupsToBundle
// (T-1207) uses for capability bundles — here the target is tenant membership
// rather than a capability set. This is authorization data resolved from the
// IdP's group claims into vnprox's own tenant model; it never grants any
// capability by itself (OIDC caps are still intersected with the linked PVE
// identity's ACLs by T-1207), it only says which tenant's scoped view an
// authenticated user lands in.

// GroupTenantMapping maps one OIDC group-claim value to a tenant membership.
type GroupTenantMapping struct {
	// Group is the exact OIDC group-claim value, matched verbatim.
	Group string
	// TenantID is the tenant the group grants membership in.
	TenantID string
	// Role is the role that membership carries ("member" or "approver").
	Role string
}

// Membership is one resolved (tenant, role) pair for a user.
type Membership struct {
	TenantID string
	Role     string
}

// MapGroupsToTenants resolves a user's OIDC group claims to the set of tenant
// memberships they imply. When two matched groups map to the same tenant with
// different roles, the more privileged role wins (approver > member) — the
// natural "most-privileged-of-my-roles" semantics, mirroring T-1207's union of
// capability bundles. Groups with no mapping contribute nothing; a user with no
// mapped group gets no tenant membership at all (an authenticated but
// non-tenant principal, i.e. an ordinary operator).
func MapGroupsToTenants(groups []string, mappings []GroupTenantMapping) []Membership {
	byGroup := make(map[string][]GroupTenantMapping, len(mappings))
	for _, m := range mappings {
		byGroup[m.Group] = append(byGroup[m.Group], m)
	}

	role := map[string]string{} // tenantID -> best role
	for _, g := range groups {
		for _, m := range byGroup[g] {
			if roleRank(m.Role) > roleRank(role[m.TenantID]) {
				role[m.TenantID] = m.Role
			}
		}
	}

	out := make([]Membership, 0, len(role))
	for tenantID, r := range role {
		out = append(out, Membership{TenantID: tenantID, Role: r})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out
}

// roleRank orders roles so the more privileged wins a union. An unknown/empty
// role ranks lowest so a valid role always beats it.
func roleRank(role string) int {
	switch role {
	case TenantRoleApprover:
		return 2
	case TenantRoleMember:
		return 1
	default:
		return 0
	}
}

// Role constants re-exported at the tenant-package level so callers mapping
// OIDC groups don't have to reach into internal/store for the vocabulary.
const (
	TenantRoleMember   = "member"
	TenantRoleApprover = "approver"
)
