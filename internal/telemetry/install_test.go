package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

func openTestStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestResetInstallIDProducesANewIDAndForgetsTheOld is AC4.
//
// Both halves are asserted, and the second one is asserted the hard way: the
// whole database is walked — every table, every column — looking for the old
// id. A test that only called Get and found the new value would pass for an
// implementation that kept the old one in an audit row, a "previous" column
// or a second key, which is exactly the failure an operator resetting their
// correlator would never find out about.
func TestResetInstallIDProducesANewIDAndForgetsTheOld(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)

	first, created, err := EnsureInstallID(ctx, db)
	if err != nil {
		t.Fatalf("EnsureInstallID: %v", err)
	}
	if !created {
		t.Fatal("the first EnsureInstallID reported that the id already existed")
	}
	if !ulidPattern.MatchString(first) {
		t.Fatalf("the generated install-id %q is not a ULID", first)
	}

	// Control: before the reset, the scan below MUST find the id. Without
	// this leg, "the old id is nowhere in the store" would also be true of a
	// scan that looks in the wrong place or a database that was never
	// written to.
	hits := scanStoreFor(t, db, first)
	if len(hits) == 0 {
		t.Fatalf("the scan cannot find the CURRENT install-id %q anywhere in the store, so it proves nothing about the old one", first)
	}

	second, err := ResetInstallID(ctx, db)
	if err != nil {
		t.Fatalf("ResetInstallID: %v", err)
	}
	if second == first {
		t.Fatal("the reset produced the same id")
	}
	if !ulidPattern.MatchString(second) {
		t.Fatalf("the new install-id %q is not a ULID", second)
	}

	if hits := scanStoreFor(t, db, first); len(hits) > 0 {
		t.Fatalf("the OLD install-id is still readable from the store at %s", strings.Join(hits, ", "))
	}
	if hits := scanStoreFor(t, db, second); len(hits) == 0 {
		t.Fatal("the NEW install-id is not in the store, so the scan is not looking where the id lives")
	}

	got, err := PeekInstallID(ctx, db)
	if err != nil {
		t.Fatalf("PeekInstallID: %v", err)
	}
	if got != second {
		t.Fatalf("PeekInstallID = %q, want the new id %q", got, second)
	}
}

// TestEnsureInstallIDIsStable: an id, once generated, does not change on its
// own. A correlator that rotated by itself would make every report look like
// a new install and quietly destroy the thing telemetry is for.
func TestEnsureInstallIDIsStable(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)

	first, created, err := EnsureInstallID(ctx, db)
	if err != nil || !created {
		t.Fatalf("EnsureInstallID: id=%q created=%v err=%v", first, created, err)
	}
	for i := range 3 {
		again, createdAgain, againErr := EnsureInstallID(ctx, db)
		if againErr != nil {
			t.Fatalf("EnsureInstallID #%d: %v", i+2, againErr)
		}
		if createdAgain {
			t.Fatalf("EnsureInstallID #%d generated a second id", i+2)
		}
		if again != first {
			t.Fatalf("EnsureInstallID #%d = %q, want %q", i+2, again, first)
		}
	}
}

// TestPeekDoesNotCreate: asking whether this install has a correlator must
// not be the act that gives it one.
func TestPeekDoesNotCreate(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)

	id, err := PeekInstallID(ctx, db)
	if err != nil {
		t.Fatalf("PeekInstallID: %v", err)
	}
	if id != "" {
		t.Fatalf("PeekInstallID on a fresh store returned %q", id)
	}
	if hits := scanStoreFor(t, db, InstallIDKey); len(hits) > 0 {
		t.Fatalf("peeking wrote the install-id key into the store at %s", strings.Join(hits, ", "))
	}
}

// scanStoreFor returns "table.column" for every place value appears as (part
// of) a text value in the database. Deliberately a whole-database sweep
// rather than a kv lookup: the claim under test is about the store, not
// about one table.
func scanStoreFor(t *testing.T, db *store.DB, value string) []string {
	t.Helper()
	conn := db.Conn()

	tableRows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	var tables []string
	for tableRows.Next() {
		var name string
		if scanErr := tableRows.Scan(&name); scanErr != nil {
			t.Fatalf("scanning table name: %v", scanErr)
		}
		tables = append(tables, name)
	}
	if err := tableRows.Err(); err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	_ = tableRows.Close()
	if len(tables) == 0 {
		t.Fatal("the store has no tables; this scan would find nothing whatever happened")
	}

	var hits []string
	for _, table := range tables {
		for _, column := range tableColumns(t, conn, table) {
			var count int
			//nolint:gosec // table/column names come from sqlite_master, not from input
			q := fmt.Sprintf(`SELECT COUNT(*) FROM %q WHERE CAST(%q AS TEXT) LIKE ?`, table, column)
			if err := conn.QueryRow(q, "%"+value+"%").Scan(&count); err != nil {
				// A column type that cannot be cast is not a place a string
				// hides; skip it rather than failing the sweep.
				continue
			}
			if count > 0 {
				hits = append(hits, table+"."+column)
			}
		}
	}
	return hits
}

func tableColumns(t *testing.T, conn *sql.DB, table string) []string {
	t.Helper()
	//nolint:gosec // table name comes from sqlite_master
	rows, err := conn.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		t.Fatalf("reading columns of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var cols []string
	for rows.Next() {
		var (
			cid         int
			name, ctype string
			notNull, pk int
			dflt        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scanning columns of %s: %v", table, err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading columns of %s: %v", table, err)
	}
	return cols
}
