package store

import (
	"context"
	"errors"
	"testing"
)

func TestIncidentRepo_InsertGetList(t *testing.T) {
	db := openTestDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unknown): err = %v, want ErrNotFound", err)
	}

	live := Incident{
		ID: "inc-live", Title: "vmbr0 down on pve2", Status: IncidentStatusOpen,
		OpenedBy: "brian@pam", OpenedAt: 1000, StartedAt: 1000,
	}
	// Retroactive: opened long after the window it describes.
	retro := Incident{
		ID: "inc-retro", Title: "last Tuesday", Status: IncidentStatusOpen,
		OpenedBy: "brian@pam", OpenedAt: 5000, StartedAt: 900, EndedAt: 950,
	}
	for _, i := range []Incident{live, retro} {
		if err := repo.Insert(ctx, i); err != nil {
			t.Fatalf("Insert(%s): %v", i.ID, err)
		}
	}

	got, err := repo.Get(ctx, "inc-retro")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != retro {
		t.Fatalf("Get = %+v, want %+v", got, retro)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != "inc-live" || list[1].ID != "inc-retro" {
		t.Fatalf("List = %+v, want inc-live then inc-retro (most recent window first)", list)
	}
}

func TestIncidentRepo_SetStatus(t *testing.T) {
	db := openTestDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	if err := repo.SetStatus(ctx, "nope", IncidentStatusClosed, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetStatus(unknown): err = %v, want ErrNotFound", err)
	}

	in := Incident{ID: "inc-1", Title: "t", Status: IncidentStatusOpen, OpenedBy: "u", OpenedAt: 10, StartedAt: 10}
	if err := repo.Insert(ctx, in); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.SetStatus(ctx, "inc-1", IncidentStatusClosed, 90, 95); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, err := repo.Get(ctx, "inc-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Incident{ID: "inc-1", Title: "t", Status: IncidentStatusClosed, OpenedBy: "u", OpenedAt: 10, StartedAt: 10, EndedAt: 90, ClosedAt: 95}
	if got != want {
		t.Fatalf("after SetStatus, Get = %+v, want %+v", got, want)
	}

	// Reopening puts the window back to "runs to now" and clears the close
	// instant; the title, author and window start are untouched.
	if reopenErr := repo.SetStatus(ctx, "inc-1", IncidentStatusOpen, 0, 0); reopenErr != nil {
		t.Fatalf("SetStatus(reopen): %v", reopenErr)
	}
	got, err = repo.Get(ctx, "inc-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != in {
		t.Fatalf("after reopen, Get = %+v, want the original %+v", got, in)
	}
}

func TestIncidentRepo_Annotations(t *testing.T) {
	db := openTestDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	for _, id := range []string{"inc-1", "other"} {
		if err := repo.Insert(ctx, Incident{
			ID: id, Title: "t", Status: IncidentStatusOpen, OpenedBy: "u", OpenedAt: 10, StartedAt: 10,
		}); err != nil {
			t.Fatalf("Insert(%s): %v", id, err)
		}
	}

	empty, err := repo.ListAnnotations(ctx, "inc-1")
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListAnnotations on a fresh incident = %+v, want none", empty)
	}

	// Inserted out of order, returned in time order.
	for _, a := range []IncidentAnnotation{
		{ID: "an-2", IncidentID: "inc-1", At: 40, Author: "brian@pam", Body: "second"},
		{ID: "an-1", IncidentID: "inc-1", At: 20, Author: "brian@pam", Body: "first"},
		{ID: "an-3", IncidentID: "other", At: 30, Author: "brian@pam", Body: "another incident's note"},
	} {
		if insErr := repo.InsertAnnotation(ctx, a); insErr != nil {
			t.Fatalf("InsertAnnotation(%s): %v", a.ID, insErr)
		}
	}

	got, err := repo.ListAnnotations(ctx, "inc-1")
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(got) != 2 || got[0].ID != "an-1" || got[1].ID != "an-2" {
		t.Fatalf("ListAnnotations = %+v, want an-1 then an-2 and nothing from another incident", got)
	}
	if got[0].Body != "first" || got[0].Author != "brian@pam" {
		t.Fatalf("annotation round-trip lost content: %+v", got[0])
	}
}
