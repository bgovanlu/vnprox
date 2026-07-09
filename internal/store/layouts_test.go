package store

import (
	"context"
	"errors"
	"testing"
)

func TestLayoutRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewLayoutRepo(db)
	ctx := context.Background()

	l := Layout{Username: "root@pam", Name: "topology", LayoutJSON: `{"nodes":[]}`, UpdatedAt: 100}
	if err := repo.Insert(ctx, l); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, l.Username, l.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != l {
		t.Errorf("Get() = %+v, want %+v", got, l)
	}

	if insertErr := repo.Insert(ctx, Layout{Username: "root@pam", Name: "sdn", LayoutJSON: `{}`, UpdatedAt: 100}); insertErr != nil {
		t.Fatalf("Insert second layout: %v", insertErr)
	}

	list, err := repo.List(ctx, "root@pam")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("List() len = %d, want 2", len(list))
	}

	l.LayoutJSON = `{"nodes":[{"id":"pve1","x":10,"y":20}]}`
	l.UpdatedAt = 200
	if updateErr := repo.Update(ctx, l); updateErr != nil {
		t.Fatalf("Update: %v", updateErr)
	}
	got, err = repo.Get(ctx, l.Username, l.Name)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got != l {
		t.Errorf("Get() after Update = %+v, want %+v", got, l)
	}
}

func TestLayoutRepo_Put_UpsertsAndUpdates(t *testing.T) {
	db := openTestDB(t)
	repo := NewLayoutRepo(db)
	ctx := context.Background()

	l := Layout{Username: "bob@pve", Name: "topology", LayoutJSON: `{}`, UpdatedAt: 1}
	if err := repo.Put(ctx, l); err != nil {
		t.Fatalf("Put (insert): %v", err)
	}
	l.LayoutJSON = `{"a":1}`
	l.UpdatedAt = 2
	if err := repo.Put(ctx, l); err != nil {
		t.Fatalf("Put (update): %v", err)
	}

	got, err := repo.Get(ctx, l.Username, l.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != l {
		t.Errorf("Get() = %+v, want %+v", got, l)
	}

	if err := repo.Delete(ctx, l.Username, l.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, l.Username, l.Name); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestLayoutRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewLayoutRepo(db)
	if _, err := repo.Get(context.Background(), "nobody", "nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestLayoutRepo_UpdateNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewLayoutRepo(db)
	err := repo.Update(context.Background(), Layout{Username: "nobody", Name: "nothing", LayoutJSON: "{}"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing): got %v, want ErrNotFound", err)
	}
}
