// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func seedTenant(t *testing.T, r *TenantRepo, id, name string) {
	t.Helper()
	if err := r.InsertTenant(context.Background(), Tenant{ID: id, Name: name, CreatedBy: "admin@pve", CreatedAt: 100}); err != nil {
		t.Fatalf("InsertTenant(%s): %v", id, err)
	}
}

func TestTenantRepo_CRUDAndScopes(t *testing.T) {
	ctx := context.Background()
	r := NewTenantRepo(openTestDB(t))

	seedTenant(t, r, "t1", "Team One")
	seedTenant(t, r, "t2", "Team Two")

	got, err := r.GetTenant(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if got.Name != "Team One" || got.CreatedBy != "admin@pve" {
		t.Errorf("GetTenant = %+v", got)
	}

	_, err = r.GetTenant(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTenant(missing) = %v, want ErrNotFound", err)
	}

	list, err := r.ListTenants(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListTenants = %v (len %d), err %v", list, len(list), err)
	}

	// Scopes are additive, idempotent, and never leak across tenants.
	for _, ref := range []string{"sdn-vnet::zone1/vnet10", "guest:pve1:100"} {
		if aerr := r.AddScope(ctx, "t1", ref); aerr != nil {
			t.Fatalf("AddScope: %v", aerr)
		}
	}
	if aerr := r.AddScope(ctx, "t1", "guest:pve1:100"); aerr != nil { // idempotent
		t.Fatalf("AddScope idempotent: %v", aerr)
	}
	if aerr := r.AddScope(ctx, "t2", "guest:pve2:200"); aerr != nil {
		t.Fatalf("AddScope t2: %v", aerr)
	}

	s1, err := r.ScopesForTenant(ctx, "t1")
	if err != nil {
		t.Fatalf("ScopesForTenant: %v", err)
	}
	if len(s1) != 2 {
		t.Fatalf("t1 scopes = %v, want 2", s1)
	}
	s2, _ := r.ScopesForTenant(ctx, "t2")
	if len(s2) != 1 || s2[0] != "guest:pve2:200" {
		t.Errorf("t2 scopes = %v, want [guest:pve2:200]", s2)
	}

	if err := r.RemoveScope(ctx, "t1", "guest:pve1:100"); err != nil {
		t.Fatalf("RemoveScope: %v", err)
	}
	s1, _ = r.ScopesForTenant(ctx, "t1")
	if len(s1) != 1 || s1[0] != "sdn-vnet::zone1/vnet10" {
		t.Errorf("t1 scopes after remove = %v", s1)
	}
}

func TestTenantRepo_MembersAndRoles(t *testing.T) {
	ctx := context.Background()
	r := NewTenantRepo(openTestDB(t))
	seedTenant(t, r, "t1", "Team One")

	if err := r.PutMember(ctx, TenantMember{TenantID: "t1", Identity: "alice@pve", Role: TenantRoleMember}); err != nil {
		t.Fatalf("PutMember alice: %v", err)
	}
	if err := r.PutMember(ctx, TenantMember{TenantID: "t1", Identity: "boss@pve", Role: TenantRoleApprover}); err != nil {
		t.Fatalf("PutMember boss: %v", err)
	}

	if err := r.PutMember(ctx, TenantMember{TenantID: "t1", Identity: "x@pve", Role: "root"}); err == nil {
		t.Error("PutMember with invalid role should fail")
	}

	role, err := r.MemberRole(ctx, "t1", "alice@pve")
	if err != nil || role != TenantRoleMember {
		t.Errorf("MemberRole(alice) = %q, %v", role, err)
	}
	_, err = r.MemberRole(ctx, "t1", "stranger@pve")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("MemberRole(stranger) = %v, want ErrNotFound", err)
	}

	// Upsert promotes.
	if perr := r.PutMember(ctx, TenantMember{TenantID: "t1", Identity: "alice@pve", Role: TenantRoleApprover}); perr != nil {
		t.Fatalf("PutMember promote: %v", perr)
	}
	role, _ = r.MemberRole(ctx, "t1", "alice@pve")
	if role != TenantRoleApprover {
		t.Errorf("MemberRole(alice) after promote = %q", role)
	}

	tenants, err := r.TenantsForIdentity(ctx, "alice@pve")
	if err != nil || len(tenants) != 1 || tenants[0].TenantID != "t1" {
		t.Errorf("TenantsForIdentity(alice) = %v, %v", tenants, err)
	}

	members, _ := r.MembersForTenant(ctx, "t1")
	if len(members) != 2 {
		t.Errorf("MembersForTenant = %v, want 2", members)
	}

	if err := r.RemoveMember(ctx, "t1", "alice@pve"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := r.MemberRole(ctx, "t1", "alice@pve"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MemberRole after remove = %v, want ErrNotFound", err)
	}
}

func TestTenantRepo_ChangesetRequests(t *testing.T) {
	ctx := context.Background()
	r := NewTenantRepo(openTestDB(t))
	seedTenant(t, r, "t1", "Team One")

	req := ChangesetRequest{ChangesetID: "cs1", TenantID: "t1", RequestedBy: "alice@pve", CreatedAt: 200}
	if err := r.InsertRequest(ctx, req); err != nil {
		t.Fatalf("InsertRequest: %v", err)
	}
	got, err := r.GetRequest(ctx, "cs1")
	if err != nil || got.TenantID != "t1" || got.RequestedBy != "alice@pve" || got.ApprovedBy != "" {
		t.Fatalf("GetRequest = %+v, %v", got, err)
	}
	if _, err := r.GetRequest(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRequest(nope) = %v, want ErrNotFound", err)
	}

	if err := r.MarkRequestApproved(ctx, "cs1", "boss@pve", 300); err != nil {
		t.Fatalf("MarkRequestApproved: %v", err)
	}
	got, _ = r.GetRequest(ctx, "cs1")
	if got.ApprovedBy != "boss@pve" || got.ApprovedAt != 300 {
		t.Errorf("GetRequest after approve = %+v", got)
	}
	if err := r.MarkRequestApproved(ctx, "missing", "boss@pve", 300); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkRequestApproved(missing) = %v, want ErrNotFound", err)
	}
}

// TestTenantRepo_CascadeOnTenantDelete proves ON DELETE CASCADE removes a
// tenant's scopes/members/requests, so a deleted tenant leaves no orphaned
// visibility that could leak to a re-created tenant of the same id.
func TestTenantRepo_CascadeOnTenantDelete(t *testing.T) {
	ctx := context.Background()
	r := NewTenantRepo(openTestDB(t))
	seedTenant(t, r, "t1", "Team One")
	_ = r.AddScope(ctx, "t1", "guest:pve1:100")
	_ = r.PutMember(ctx, TenantMember{TenantID: "t1", Identity: "alice@pve", Role: TenantRoleMember})
	_ = r.InsertRequest(ctx, ChangesetRequest{ChangesetID: "cs1", TenantID: "t1", RequestedBy: "alice@pve", CreatedAt: 1})

	if err := r.DeleteTenant(ctx, "t1"); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	if s, _ := r.ScopesForTenant(ctx, "t1"); len(s) != 0 {
		t.Errorf("scopes survived tenant delete: %v", s)
	}
	if m, _ := r.MembersForTenant(ctx, "t1"); len(m) != 0 {
		t.Errorf("members survived tenant delete: %v", m)
	}
	if _, err := r.GetRequest(ctx, "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("request survived tenant delete: %v", err)
	}
}
