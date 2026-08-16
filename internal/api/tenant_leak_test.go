package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// TestTenantAdminRoutesAreNotTenantScoped documents a cross-tenant
// information disclosure found on 2026-08-16 by the T-3002 agent, which was
// reading these routes in order to build a tenants screen.
//
// docs/user-guide.md:156 promises: "A tenant member sees only their own slice
// of the topology, findings, and IPAM — everything outside their scope is not
// just hidden but genuinely invisible (a lookup of something out of scope
// returns 'not found,' never confirming it exists)." docs/datasheet.md makes
// the same promise more tersely.
//
// GET /tenants and GET /tenants/{id} do not hold it. mountTenantRoutes
// (tenant.go:265-270) puts both behind auth.RequireCap(capNetRead) and NOTHING
// else — no tenantScopeMiddleware, no filter, no membership check — under a
// comment reading "Admin CRUD: reads require netRead". netRead is not an admin
// capability; every tenant member has it, because it is derived from ordinary
// PVE network-read ACLs.
//
// So a member of tenant t1 can enumerate every tenant and read t2's scope
// refs — which ARE guest and subnet identifiers belonging to t2 — plus t2's
// member identities.
//
// TestTenantScoping_NoCrossTenantLeakage (tenant_test.go:241) is the test that
// would be expected to catch this. It exercises /topology and /flows. It never
// calls /tenants.
//
// This test asserts TODAY'S behaviour on purpose, so the gap cannot be closed
// by accident and cannot quietly persist either. When someone fixes it this
// test goes red — that is the intent, and the failure message says what to do.
// Tracked as T-3002-followup-01 in planning/tasks/phase-30.md.
func TestTenantAdminRoutesAreNotTenantScoped(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember},
		"guest:pve1:100", "sdn-subnet::10.0.0.0/24")
	env.seedTenant(t, "t2", map[string]string{"bob@pve": store.TenantRoleMember},
		"guest:pve2:200")

	// Alice is a member of t1 only. She is not an administrator.
	aliceR := env.router("alice@pve")

	t.Run("she can enumerate every tenant", func(t *testing.T) {
		rec := httptest.NewRecorder()
		aliceR.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /tenants as a plain tenant member: status = %d, want 200 (today's behaviour).\n"+
				"If this is now 403/404, the leak has been FIXED — delete this test, and update "+
				"docs/user-guide.md:156 and T-3002-followup-01 to say so.", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"t2"`) {
			t.Errorf("GET /tenants did not list t2; the disclosure this test documents may already be fixed — re-read it before assuming otherwise")
		}
	})

	t.Run("she can read another tenant's scope refs and members", func(t *testing.T) {
		rec := httptest.NewRecorder()
		aliceR.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants/t2", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /tenants/t2 as a member of t1 only: status = %d, want 200 (today's behaviour).\n"+
				"If this is now 403/404, the leak has been FIXED — delete this test and update the docs.", rec.Code)
		}
		var got struct {
			Scopes  []string `json:"scopes"`
			Members []struct {
				Identity string `json:"identity"`
			} `json:"members"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		// These two assertions ARE the finding: a guest ref and an identity
		// belonging to a tenant Alice is not a member of.
		if len(got.Scopes) == 0 || got.Scopes[0] != "guest:pve2:200" {
			t.Errorf("t2's scopes as read by a t1 member = %v; expected the leak to expose guest:pve2:200", got.Scopes)
		}
		if len(got.Members) == 0 || got.Members[0].Identity != "bob@pve" {
			t.Errorf("t2's members as read by a t1 member = %+v; expected the leak to expose bob@pve", got.Members)
		}
	})
}

// TestListTenantsReportsEmptyScopesWithoutReadingThem documents a second,
// smaller defect in the same handler family, and it is this arc's recurring
// bug once more: handleListTenants (tenant.go:366-371) hard-codes
// `Scopes: []string{}, Members: []tenantMemberOutput{}` into every item
// without ever querying either table.
//
// A caller cannot distinguish "this tenant has no scopes" from "this endpoint
// does not report scopes". docs/api.md documents the Tenant shape as carrying
// both and does not say the list omits them.
func TestListTenantsReportsEmptyScopesWithoutReadingThem(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember},
		"guest:pve1:100", "sdn-subnet::10.0.0.0/24")

	rec := httptest.NewRecorder()
	env.router("alice@pve").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tenants: status = %d, want 200", rec.Code)
	}
	var got struct {
		Items []struct {
			ID     string   `json:"id"`
			Scopes []string `json:"scopes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, it := range got.Items {
		if it.ID != "t1" {
			continue
		}
		if len(it.Scopes) != 0 {
			t.Fatalf("GET /tenants now reports t1's scopes as %v.\n"+
				"That is an IMPROVEMENT over the hard-coded empty list this test documents — "+
				"delete this test and update T-3002-followup-01.", it.Scopes)
		}
		// t1 genuinely has two scopes; the list says zero.
		return
	}
	t.Fatalf("t1 missing from GET /tenants")
}
