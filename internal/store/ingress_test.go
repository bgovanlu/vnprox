package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestIngressTargetRepo_InsertGetListDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewIngressTargetRepo(db)
	ctx := context.Background()

	a := IngressTarget{
		ID: "01A", Kind: "haproxy", Address: "http://10.0.0.5:8404",
		AddedBy: "root@pam", AddedAt: 100,
	}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, a) {
		t.Errorf("Get() = %+v, want %+v", got, a)
	}

	b := IngressTarget{
		ID: "01B", Kind: "traefik", Address: "http://10.0.0.6:8080",
		CredentialEnc: []byte("cipher"), AddedBy: "root@pam", AddedAt: 200,
	}
	if insertErr := repo.Insert(ctx, b); insertErr != nil {
		t.Fatalf("Insert second: %v", insertErr)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	if list[0].ID != a.ID || list[1].ID != b.ID {
		t.Errorf("List() order = [%s, %s], want [%s, %s]", list[0].ID, list[1].ID, a.ID, b.ID)
	}
	if !reflect.DeepEqual(list[1].CredentialEnc, b.CredentialEnc) {
		t.Errorf("List()[1].CredentialEnc = %v, want %v", list[1].CredentialEnc, b.CredentialEnc)
	}

	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
	// Deleting an already-absent row is not an error (rollback/idempotent
	// convergence, same convention every other repo in this package uses).
	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Errorf("Delete of already-absent row: %v", err)
	}
}

func TestIngressTargetRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewIngressTargetRepo(db)
	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() err = %v, want ErrNotFound", err)
	}
}
