// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// TestTenantAdminRoutes_ScopedToMembership replaces
// TestTenantAdminRoutesAreNotTenantScoped, which documented a cross-tenant
// information disclosure found 2026-08-16 by the T-3002 agent: GET /tenants
// and GET /tenants/{id} were gated on plain netRead (which every tenant
// member holds, since it derives from ordinary PVE network-read ACLs) with
// no tenant-membership scoping at all, so a member of t1 could enumerate
// every tenant and read t2's scope refs and member identities outright.
//
// docs/user-guide.md:156 promises: "A tenant member sees only their own
// slice of the topology, findings, and IPAM — everything outside their
// scope is not just hidden but genuinely invisible (a lookup of something
// out of scope returns 'not found,' never confirming it exists)."
// docs/datasheet.md makes the same promise more tersely.
//
// Fixed 2026-08-19 (T-3002-followup-01, owner decision: "scope reads to own
// tenants"; a non-member's GET /tenants/{id} is 404, not 403, so it never
// confirms the tenant exists — a 403 would still leak that much).
// tenant.go's admin CRUD read group now runs tenantScopeMiddleware, and
// handleListTenants/handleGetTenant filter through the resolved Scope
// exactly like the topology/findings/ipam/flows routes already did.
func TestTenantAdminRoutes_ScopedToMembership(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember},
		"guest:pve1:100", "sdn-subnet::10.0.0.0/24")
	env.seedTenant(t, "t2", map[string]string{"bob@pve": store.TenantRoleMember},
		"guest:pve2:200")

	// Alice is a member of t1 only.
	aliceR := env.router("alice@pve")

	t.Run("she cannot enumerate t2 via GET /tenants", func(t *testing.T) {
		rec := httptest.NewRecorder()
		aliceR.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /tenants as a tenant member: status = %d, want 200", rec.Code)
		}
		if strings.Contains(rec.Body.String(), `"t2"`) {
			t.Errorf("LEAK: GET /tenants as a t1-only member listed t2: %s", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"t1"`) {
			t.Errorf("GET /tenants as a t1 member did not list t1 (her own tenant): %s", rec.Body.String())
		}
	})

	t.Run("GET /tenants/t2 is 404, not 403 — existence is not confirmed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		aliceR.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants/t2", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /tenants/t2 as a member of t1 only: status = %d, want 404 (never 403 — that would "+
				"still confirm t2 exists, against docs/user-guide.md:156's promise)", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "guest:pve2:200") || strings.Contains(rec.Body.String(), "bob@pve") {
			t.Errorf("LEAK: 404 body still names t2's scope/member data: %s", rec.Body.String())
		}
	})

	t.Run("she can still read her own tenant in full", func(t *testing.T) {
		rec := httptest.NewRecorder()
		aliceR.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants/t1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /tenants/t1 as a t1 member: status = %d, want 200, body %s", rec.Code, rec.Body.String())
		}
		var got struct {
			Scopes []string `json:"scopes"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if len(got.Scopes) != 2 {
			t.Errorf("t1's own scopes as read by a t1 member = %v, want 2", got.Scopes)
		}
	})

	t.Run("an unscoped (non-member) caller is unaffected", func(t *testing.T) {
		// admin@pve is not a member of any tenant — unscoped, same as every
		// other tenant-scoped route: multi-tenancy only ever narrows a
		// MEMBER's view.
		adminR := env.router("admin@pve")
		rec := httptest.NewRecorder()
		adminR.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /tenants as a non-member: status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"t1"`) || !strings.Contains(rec.Body.String(), `"t2"`) {
			t.Errorf("GET /tenants as a non-member should still list every tenant: %s", rec.Body.String())
		}

		rec = httptest.NewRecorder()
		adminR.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants/t2", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /tenants/t2 as a non-member: status = %d, want 200", rec.Code)
		}
	})
}

// TestListTenantsReportsRealScopesAndMembers replaces
// TestListTenantsReportsEmptyScopesWithoutReadingThem, which documented a
// second defect in the same handler family: handleListTenants hard-coded
// `Scopes: []string{}, Members: []tenantMemberOutput{}` into every item
// without ever querying either table, so a caller could not distinguish
// "this tenant has no scopes" from "this endpoint does not report scopes".
//
// Fixed 2026-08-19 in the same change as the scoping fix above:
// handleListTenants now calls the same tenantAdminRow helper
// handleGetTenant uses, which genuinely reads tenant_scopes/tenant_members.
func TestListTenantsReportsRealScopesAndMembers(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember},
		"guest:pve1:100", "sdn-subnet::10.0.0.0/24")

	rec := httptest.NewRecorder()
	env.router("admin@pve").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tenants: status = %d, want 200", rec.Code)
	}
	var got struct {
		Items []struct {
			ID      string   `json:"id"`
			Scopes  []string `json:"scopes"`
			Members []struct {
				Identity string `json:"identity"`
			} `json:"members"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, it := range got.Items {
		if it.ID != "t1" {
			continue
		}
		if len(it.Scopes) != 2 {
			t.Errorf("GET /tenants reports t1's scopes as %v, want the 2 real scope refs it was seeded with", it.Scopes)
		}
		if len(it.Members) != 1 || it.Members[0].Identity != "alice@pve" {
			t.Errorf("GET /tenants reports t1's members as %+v, want [alice@pve]", it.Members)
		}
		return
	}
	t.Fatalf("t1 missing from GET /tenants")
}
