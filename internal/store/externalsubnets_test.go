// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func TestExternalSubnetRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewExternalSubnetRepo(db)
	ctx := context.Background()

	e := ExternalSubnet{
		ID: NewULID(), CIDR: "192.0.2.0/24", Label: "office-lan",
		Description: "physical office LAN", CreatedBy: "root@pam",
		CreatedAt: 1700000000, UpdatedAt: 1700000000,
	}
	if err := repo.Insert(ctx, e); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CIDR != e.CIDR || got.Label != "office-lan" || got.CreatedBy != "root@pam" {
		t.Errorf("Get() = %+v, want cidr/label/createdBy to match", got)
	}
	// Insert defaulted the empty Source to "manual".
	if got.Source != ExternalSubnetSourceManual {
		t.Errorf("Get().Source = %q, want default %q", got.Source, ExternalSubnetSourceManual)
	}

	// A second subnet, netbox-sourced; List is stable by cidr.
	e2 := ExternalSubnet{
		ID: NewULID(), CIDR: "10.0.0.0/8", Label: "transit", Source: ExternalSubnetSourceNetbox,
		CreatedBy: "root@pam", CreatedAt: 1700000100, UpdatedAt: 1700000100,
	}
	if err = repo.Insert(ctx, e2); err != nil {
		t.Fatalf("Insert #2: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].CIDR != "10.0.0.0/8" || list[1].CIDR != "192.0.2.0/24" {
		t.Fatalf("List order = %+v, want [10.0.0.0/8, 192.0.2.0/24]", list)
	}

	// Update the first row.
	e.Label = "office-renamed"
	e.Source = ExternalSubnetSourcePhpIPAM
	e.UpdatedAt = 1700000200
	if err = repo.Update(ctx, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Label != "office-renamed" || got.Source != ExternalSubnetSourcePhpIPAM || got.UpdatedAt != 1700000200 {
		t.Errorf("Update not reflected: %+v", got)
	}

	// Delete is idempotent.
	if err = repo.Delete(ctx, e.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err = repo.Delete(ctx, e.ID); err != nil {
		t.Fatalf("Delete (idempotent) second call: %v", err)
	}
	if _, err = repo.Get(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestExternalSubnetRepo_DuplicateCIDRRejected(t *testing.T) {
	db := openTestDB(t)
	repo := NewExternalSubnetRepo(db)
	ctx := context.Background()

	first := ExternalSubnet{ID: NewULID(), CIDR: "203.0.113.0/24", CreatedBy: "root@pam", CreatedAt: 1, UpdatedAt: 1}
	if err := repo.Insert(ctx, first); err != nil {
		t.Fatalf("Insert first: %v", err)
	}
	dup := ExternalSubnet{ID: NewULID(), CIDR: "203.0.113.0/24", CreatedBy: "root@pam", CreatedAt: 2, UpdatedAt: 2}
	if err := repo.Insert(ctx, dup); err == nil {
		t.Fatal("Insert of duplicate CIDR succeeded, want unique-index violation")
	}
}

func TestExternalSubnetRepo_UpdateMissing(t *testing.T) {
	db := openTestDB(t)
	repo := NewExternalSubnetRepo(db)
	ctx := context.Background()

	err := repo.Update(ctx, ExternalSubnet{ID: NewULID(), CIDR: "198.51.100.0/24", CreatedBy: "x", CreatedAt: 1, UpdatedAt: 1})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update of missing row: err = %v, want ErrNotFound", err)
	}
}
