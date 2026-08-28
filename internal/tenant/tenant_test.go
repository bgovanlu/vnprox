// SPDX-License-Identifier: Apache-2.0

package tenant_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/tenant"
)

func newRepo(t *testing.T) *store.TenantRepo {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.NewTenantRepo(db)
}

func newService(t *testing.T, repo *store.TenantRepo, exp tenant.Expander) *tenant.Service {
	t.Helper()
	svc, err := tenant.NewService(tenant.Config{Store: repo, Expander: exp, Now: func() int64 { return 1000 }})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestScopeFor_NonMemberIsUnscoped(t *testing.T) {
	repo := newRepo(t)
	svc := newService(t, repo, nil)

	_, ok, err := svc.ScopeFor(context.Background(), "operator@pve")
	if err != nil {
		t.Fatalf("ScopeFor: %v", err)
	}
	if ok {
		t.Error("a non-member principal must be unscoped (ok=false), got ok=true")
	}
}

func TestScopeFor_ResolvesAndFiltersServerSide(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newService(t, repo, nil)

	seed(t, repo, "t1", "alice@pve", store.TenantRoleMember, "guest:pve1:100", "sdn-subnet::10.0.0.0/24")
	seed(t, repo, "t2", "bob@pve", store.TenantRoleMember, "guest:pve2:200")

	scope, ok, err := svc.ScopeFor(ctx, "alice@pve")
	if err != nil || !ok {
		t.Fatalf("ScopeFor(alice) ok=%v err=%v", ok, err)
	}
	if !scope.Visible("guest:pve1:100") || !scope.Visible("sdn-subnet::10.0.0.0/24") {
		t.Error("alice must see her own tenant's resources")
	}
	// Cross-tenant isolation: alice must never see t2's resources.
	if scope.Visible("guest:pve2:200") {
		t.Error("LEAK: alice sees t2's guest")
	}
	if scope.Visible("") || scope.Visible("guest:pve1:999") {
		t.Error("scope must fail closed for empty/unknown refs")
	}
}

func TestScopeFor_UnionsMultipleMemberships(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newService(t, repo, nil)

	seed(t, repo, "t1", "carol@pve", store.TenantRoleMember, "guest:pve1:100")
	seed(t, repo, "t2", "carol@pve", store.TenantRoleApprover, "guest:pve2:200")

	scope, ok, _ := svc.ScopeFor(ctx, "carol@pve")
	if !ok {
		t.Fatal("carol should be scoped")
	}
	if !scope.Visible("guest:pve1:100") || !scope.Visible("guest:pve2:200") {
		t.Error("a multi-tenant member sees the union of her own tenants")
	}
	if scope.RoleIn("t2") != store.TenantRoleApprover || scope.RoleIn("t1") != store.TenantRoleMember {
		t.Errorf("roles: t1=%q t2=%q", scope.RoleIn("t1"), scope.RoleIn("t2"))
	}
}

func TestCanApprove(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newService(t, repo, nil)

	_ = repo.InsertTenant(ctx, store.Tenant{ID: "t1", Name: "T1", CreatedBy: "admin", CreatedAt: 1})
	_ = repo.PutMember(ctx, store.TenantMember{TenantID: "t1", Identity: "alice@pve", Role: store.TenantRoleMember})
	_ = repo.PutMember(ctx, store.TenantMember{TenantID: "t1", Identity: "boss@pve", Role: store.TenantRoleApprover})
	_ = repo.PutMember(ctx, store.TenantMember{TenantID: "t1", Identity: "boss2@pve", Role: store.TenantRoleApprover})
	if err := svc.RecordRequest(ctx, "cs1", "t1", "alice@pve"); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	// boss2 raised their own request.
	if err := svc.RecordRequest(ctx, "cs2", "t1", "boss2@pve"); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}

	cases := []struct {
		name        string
		changeset   string
		identity    string
		wantAllowed bool
		wantNoReq   bool
	}{
		{"member cannot approve", "cs1", "alice@pve", false, false},
		{"approver can approve", "cs1", "boss@pve", true, false},
		{"non-member cannot approve", "cs1", "stranger@pve", false, false},
		{"approver cannot approve own request", "cs2", "boss2@pve", false, false},
		{"other approver can approve boss2's request", "cs2", "boss@pve", true, false},
		{"non-request changeset is not-found", "unknown", "boss@pve", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := svc.CanApprove(ctx, tc.changeset, tc.identity)
			if err != nil {
				t.Fatalf("CanApprove: %v", err)
			}
			if dec.Allowed != tc.wantAllowed {
				t.Errorf("Allowed = %v, want %v", dec.Allowed, tc.wantAllowed)
			}
			if dec.NotARequest != tc.wantNoReq {
				t.Errorf("NotARequest = %v, want %v", dec.NotARequest, tc.wantNoReq)
			}
		})
	}
}

func TestApprovers(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newService(t, repo, nil)
	_ = repo.InsertTenant(ctx, store.Tenant{ID: "t1", Name: "T1", CreatedBy: "a", CreatedAt: 1})
	_ = repo.PutMember(ctx, store.TenantMember{TenantID: "t1", Identity: "m@pve", Role: store.TenantRoleMember})
	_ = repo.PutMember(ctx, store.TenantMember{TenantID: "t1", Identity: "b@pve", Role: store.TenantRoleApprover})
	_ = repo.PutMember(ctx, store.TenantMember{TenantID: "t1", Identity: "a@pve", Role: store.TenantRoleApprover})

	got, err := svc.Approvers(ctx, "t1")
	if err != nil {
		t.Fatalf("Approvers: %v", err)
	}
	if len(got) != 2 || got[0] != "a@pve" || got[1] != "b@pve" {
		t.Errorf("Approvers = %v, want [a@pve b@pve]", got)
	}
}

func TestMapGroupsToTenants(t *testing.T) {
	mappings := []tenant.GroupTenantMapping{
		{Group: "vnprox-t1-members", TenantID: "t1", Role: tenant.TenantRoleMember},
		{Group: "vnprox-t1-approvers", TenantID: "t1", Role: tenant.TenantRoleApprover},
		{Group: "vnprox-t2-members", TenantID: "t2", Role: tenant.TenantRoleMember},
	}

	// A user in both t1 groups gets the more-privileged approver role for t1.
	got := tenant.MapGroupsToTenants([]string{"vnprox-t1-members", "vnprox-t1-approvers", "unmapped"}, mappings)
	if len(got) != 1 || got[0].TenantID != "t1" || got[0].Role != tenant.TenantRoleApprover {
		t.Errorf("union role: got %+v", got)
	}

	// A user in two tenants gets both memberships.
	got = tenant.MapGroupsToTenants([]string{"vnprox-t1-members", "vnprox-t2-members"}, mappings)
	if len(got) != 2 {
		t.Fatalf("two-tenant: got %+v", got)
	}

	// No mapped group => no memberships (an ordinary non-tenant operator).
	if got := tenant.MapGroupsToTenants([]string{"random"}, mappings); len(got) != 0 {
		t.Errorf("unmapped groups should yield no membership, got %+v", got)
	}
}

// fakeGraph implements GraphSource over a hand-built snapshot.
type fakeGraph struct{ snap inventory.Snapshot }

func (f fakeGraph) Snapshot() inventory.Snapshot { return f.snap }

func TestGraphExpander_VnetExpandsToGuestsAndSubnets(t *testing.T) {
	g := inventory.NewGraph()
	vnet := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "zone1/vnet10"}
	guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}
	nicRef := inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"}
	subRef := inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "zone1/vnet10/10.0.0.0-24"}
	otherGuest := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "999"}
	otherNic := inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "999/net0"}

	g.ApplyPoll(inventory.Source("test"), inventory.Scope{}, []inventory.Entity{
		&inventory.SdnVnet{Ref: vnet, ID: "zone1/vnet10", Zone: "zone1"},
		&inventory.Guest{Ref: guestRef, VMID: 100, Node: "pve1"},
		&inventory.GuestNic{Ref: nicRef, Guest: guestRef, Key: "net0", BridgeOrVnet: vnet},
		&inventory.SdnSubnet{Ref: subRef, ID: "10.0.0.0/24", Vnet: "zone1/vnet10"},
		// An unrelated guest attached elsewhere must NOT be pulled in.
		&inventory.Guest{Ref: otherGuest, VMID: 999, Node: "pve1"},
		&inventory.GuestNic{Ref: otherNic, Guest: otherGuest, Key: "net0",
			BridgeOrVnet: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}},
	})

	exp := tenant.NewGraphExpander(fakeGraph{snap: g.Snapshot()})
	set, err := exp.ExpandScopeRefs(context.Background(), []string{vnet.String()})
	if err != nil {
		t.Fatalf("ExpandScopeRefs: %v", err)
	}

	for _, want := range []string{vnet.String(), guestRef.String(), nicRef.String(), subRef.String()} {
		if !set[want] {
			t.Errorf("expanded set missing %q", want)
		}
	}
	if set[otherGuest.String()] || set[otherNic.String()] {
		t.Error("LEAK: expansion pulled in an unrelated guest/nic")
	}
}

func seed(t *testing.T, repo *store.TenantRepo, tenantID, identity, role string, refs ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := repo.GetTenant(ctx, tenantID); err != nil {
		if err := repo.InsertTenant(ctx, store.Tenant{ID: tenantID, Name: tenantID, CreatedBy: "admin", CreatedAt: 1}); err != nil {
			t.Fatalf("InsertTenant: %v", err)
		}
	}
	if err := repo.PutMember(ctx, store.TenantMember{TenantID: tenantID, Identity: identity, Role: role}); err != nil {
		t.Fatalf("PutMember: %v", err)
	}
	for _, ref := range refs {
		if err := repo.AddScope(ctx, tenantID, ref); err != nil {
			t.Fatalf("AddScope: %v", err)
		}
	}
}
