package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

// observedCall is one QueryObserver invocation, recorded for assertions.
type observedCall struct {
	op     string
	failed bool
}

type recordingObserver struct {
	calls []observedCall
	mu    sync.Mutex
}

func (r *recordingObserver) observe(op string, _ time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, observedCall{op: op, failed: err != nil})
}

func (r *recordingObserver) opsCalled() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c.op
	}
	return out
}

func TestDB_SetQueryObserver_RecordsExecAndQuery(t *testing.T) {
	db := openTestDB(t)
	rec := &recordingObserver{}
	db.SetQueryObserver(rec.observe)

	repo := NewKVRepo(db)
	ctx := context.Background()

	if err := repo.Insert(ctx, "k1", "v1"); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := repo.Get(ctx, "k1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := repo.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}

	ops := rec.opsCalled()
	var inserts, selects int
	for _, op := range ops {
		switch op {
		case "insert":
			inserts++
		case "select":
			// Both Get (QueryRowContext) and List (QueryContext) reduce to
			// the same "select" op label — that's queryOp's whole point
			// (a SQL verb, not a per-call-site distinction).
			selects++
		}
	}
	if inserts != 1 {
		t.Errorf("observer saw %d \"insert\" ops, want 1; got %v", inserts, ops)
	}
	if selects != 2 {
		t.Errorf("observer saw %d \"select\" ops (Get + List), want 2; got %v", selects, ops)
	}
}

func TestDB_SetQueryObserver_NilByDefaultIsNoOp(t *testing.T) {
	db := openTestDB(t)
	repo := NewKVRepo(db)
	// No SetQueryObserver call — must not panic or otherwise misbehave.
	if err := repo.Insert(context.Background(), "k1", "v1"); err != nil {
		t.Fatalf("Insert with no observer configured: %v", err)
	}
}

func TestQueryOp(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM kv":                "select",
		"  select v from kv where k = ?":  "select",
		"INSERT INTO kv (k, v) VALUES ()": "insert",
		"UPDATE kv SET v = ? WHERE k = ?": "update",
		"DELETE FROM kv WHERE k = ?":      "delete",
		"PRAGMA foreign_keys = ON":        "other",
		"":                                "other",
	}
	for query, want := range cases {
		if got := queryOp(query); got != want {
			t.Errorf("queryOp(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestDB_SizeBytes_ReportsPositiveSize(t *testing.T) {
	db := openTestDB(t)
	size, err := db.SizeBytes()
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size <= 0 {
		t.Errorf("SizeBytes = %d, want > 0 for a freshly-migrated database", size)
	}
}

func TestDB_SchemaVersion_CurrentMatchesLatestAfterOpen(t *testing.T) {
	db := openTestDB(t)
	current, latest, err := db.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if current != latest {
		t.Errorf("current = %d, latest = %d, want equal immediately after Open (Open always migrates to latest)", current, latest)
	}
	if current == 0 {
		t.Errorf("current schema version = 0, want > 0 (embedded migrations exist)")
	}
}
