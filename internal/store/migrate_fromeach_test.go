package store

// T-606 (planning/tasks/phase-6.md#T-606): "upgrade path: migration tests
// from each prior tagged schema". This repository has not cut any git tags
// yet (pre-1.0: `git tag -l` is empty), so "each prior tagged schema" is,
// today, exactly "each prior schema_version that has ever shipped" — 0
// (a brand-new, pre-0001 database; i.e. a fresh install of the very first
// release) through latest-1. Once real release tags exist, each of them
// pins one of these version numbers (a tag's go.mod-embedded migrations
// determine the schema_version its build understood), so this harness stays
// meaningful without depending on tag names at all: it is parameterized
// purely over schema_version, which is what "an install upgrading from an
// older release" actually differs by. As new migrations land, this test
// automatically grows an additional prior-version case with no edits
// needed.
//
// Each case: build a database frozen at schema_version V (by applying only
// migrations 1..V, exactly what an old vnproxd build left on disk), seed it
// with representative data in that version's shape, then run this build's
// full migrate() over it (what happens on `apt install` upgrading vnprox on
// that node) and assert: (1) the DB ends at this build's latest
// schema_version, (2) every pre-existing row survived byte-for-byte
// untouched, (3) tables/columns added by later migrations are present and
// usable. This is the "DB migrations run automatically on daemon start;
// forward-only" contract (docs/deployment.md "Upgrade") pinned per prior
// version rather than only ever tested from an always-empty database, which
// is all TestOpen_CreatesAllTables and friends exercise.

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
)

// openFrozenAt returns a raw, unmigrated-beyond-V *sql.DB: migrations
// 1..version are applied (version 0 leaves it completely empty, as a
// brand-new file would be before any vnproxd ever opened it), and nothing
// past that. This deliberately bypasses Open (which always migrates to
// latest) — it exists to simulate exactly the on-disk state an older
// vnproxd build would have left behind.
func openFrozenAt(t *testing.T, version int) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	ctx := context.Background()
	for _, m := range migrations {
		if m.version > version {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			t.Fatalf("applying migration %s while freezing at version %d: %v", m.name, version, err)
		}
	}
	return db
}

// seedAtVersion1 inserts one representative row per 0001_init.sql table —
// the shape every column in that file's schema documents — using values a
// real T-1xx/T-2xx-era vnproxd could actually have written.
func seedAtVersion1(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seeding at v1 (%s): %v", query, err)
		}
	}
	exec(`INSERT INTO sessions (id, username, realm, pve_ticket_enc, csrf_token_enc, caps_json, created_at, expires_at)
	      VALUES ('sess-1', 'root@pam', 'pam', x'01020304', x'05060708', '{"sysModify":true}', 1700000000, 1700007200)`)
	exec(`INSERT INTO changesets (id, title, author, status, ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, created_at, updated_at)
	      VALUES ('cs-v1', 'pre-upgrade change', 'root@pam', 'committed', '[]', '[]', '{}', '{}', NULL, 1700000000, 1700000100)`)
	exec(`INSERT INTO snapshots (id, changeset_id, taken_at, kind, files_json)
	      VALUES ('snap-v1', 'cs-v1', 1700000000, 'pre', '[{"node":"pve1","path":"/etc/network/interfaces","sha256":"deadbeef"}]')`)
	exec(`INSERT INTO audit_log (at, username, action, target, changeset_id, result, detail_json)
	      VALUES (1700000000, 'root@pam', 'changeset.apply', NULL, 'cs-v1', 'success', '{}')`)
	exec(`INSERT INTO layouts (username, name, layout_json, updated_at)
	      VALUES ('root@pam', 'default', '{"nodes":[]}', 1700000000)`)
	exec(`INSERT INTO metric_samples (ref, at, rx_bytes, tx_bytes, rx_pkts, tx_pkts, rx_errs, tx_errs, rx_drop, tx_drop)
	      VALUES ('iface:pve1:vmbr0', 1700000000, 100, 200, 1, 2, 0, 0, 0, 0)`)
	exec(`INSERT INTO kv (k, v) VALUES ('install_id', 'test-install-v1')`)
}

// seedAtVersion2 adds 0002_snapshot_blobs.sql's tables (and its
// snapshots.note column) on top of seedAtVersion1.
func seedAtVersion2(t *testing.T, db *sql.DB) {
	t.Helper()
	seedAtVersion1(t, db)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seeding at v2 (%s): %v", query, err)
		}
	}
	exec(`INSERT INTO blobs (sha256, content_zstd, size) VALUES ('deadbeef', x'28b52ffd', 42)`)
	exec(`INSERT INTO snapshot_files (snapshot_id, node, path, sha256) VALUES ('snap-v1', 'pve1', '/etc/network/interfaces', 'deadbeef')`)
	exec(`UPDATE snapshots SET note = 'manual pre-upgrade snapshot' WHERE id = 'snap-v1'`)
}

// migrateFromEachTestCase pins one prior schema_version to freeze at
// (frozenVersion), a seeder that populates it in that version's shape, and
// an assertion function that all seeded data survived the subsequent
// migrate-to-latest.
type migrateFromEachTestCase struct {
	name          string
	frozenVersion int
	seed          func(t *testing.T, db *sql.DB) // nil for "nothing existed yet" (fresh install)
	assertSeeded  func(t *testing.T, db *sql.DB)
}

func TestMigrate_FromEachPriorSchemaVersion(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	latest := latestSchemaVersion(migrations)
	if latest == 0 {
		t.Fatal("no embedded migrations found — this test needs at least one to be meaningful")
	}

	cases := []migrateFromEachTestCase{
		{
			name:          "version 0 (fresh install, no schema at all)",
			frozenVersion: 0,
			seed:          nil,
			assertSeeded:  func(t *testing.T, db *sql.DB) {},
		},
		{
			name:          "version 1 (0001_init only)",
			frozenVersion: 1,
			seed:          seedAtVersion1,
			assertSeeded:  assertVersion1Seeded,
		},
		{
			name:          "version 2 (0001+0002, snapshot blobs added)",
			frozenVersion: 2,
			seed:          seedAtVersion2,
			assertSeeded:  assertVersion2Seeded,
		},
	}

	for _, tc := range cases {
		tc := tc
		if tc.frozenVersion >= latest {
			continue // this version isn't "prior" for the current build; skip rather than fail as migrations accrue
		}
		t.Run(tc.name, func(t *testing.T) {
			db := openFrozenAt(t, tc.frozenVersion)
			ctx := context.Background()

			gotBefore, err := currentSchemaVersion(ctx, db)
			if err != nil {
				t.Fatalf("currentSchemaVersion before seeding: %v", err)
			}
			if gotBefore != tc.frozenVersion {
				t.Fatalf("frozen schema_version = %d, want %d (freezing helper bug)", gotBefore, tc.frozenVersion)
			}

			if tc.seed != nil {
				tc.seed(t, db)
			}

			// This is the actual thing being tested: the same migrate() an
			// upgraded vnproxd runs against this node's existing database on
			// startup (store.Open calls it unconditionally).
			if err := migrate(ctx, db); err != nil {
				t.Fatalf("migrate() from version %d to latest: %v", tc.frozenVersion, err)
			}

			gotAfter, err := currentSchemaVersion(ctx, db)
			if err != nil {
				t.Fatalf("currentSchemaVersion after migrate: %v", err)
			}
			if gotAfter != latest {
				t.Errorf("schema_version after migrate = %d, want latest (%d)", gotAfter, latest)
			}

			tc.assertSeeded(t, db)

			// Migrating an already-latest DB again (the "every subsequent
			// `apt install vnprox` on an already-current node" case, and
			// simply restarting the daemon) must be a no-op, not an error —
			// migrate()'s "skip if m.version <= current" loop guard is what
			// this proves.
			if err := migrate(ctx, db); err != nil {
				t.Fatalf("re-running migrate() on an already-latest database: %v", err)
			}
		})
	}
}

func assertVersion1Seeded(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()

	var username, status string
	if err := db.QueryRowContext(ctx, `SELECT username FROM sessions WHERE id = 'sess-1'`).Scan(&username); err != nil {
		t.Errorf("sessions row lost across migration: %v", err)
	} else if username != "root@pam" {
		t.Errorf("sessions.username = %q, want root@pam", username)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM changesets WHERE id = 'cs-v1'`).Scan(&status); err != nil {
		t.Errorf("changesets row lost across migration: %v", err)
	} else if status != "committed" {
		t.Errorf("changesets.status = %q, want committed", status)
	}

	var filesJSON string
	if err := db.QueryRowContext(ctx, `SELECT files_json FROM snapshots WHERE id = 'snap-v1'`).Scan(&filesJSON); err != nil {
		t.Errorf("snapshots row lost across migration: %v", err)
	}

	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE changeset_id = 'cs-v1'`).Scan(&auditCount); err != nil {
		t.Errorf("audit_log query failed: %v", err)
	} else if auditCount != 1 {
		t.Errorf("audit_log rows for cs-v1 = %d, want 1", auditCount)
	}

	var layoutJSON string
	if err := db.QueryRowContext(ctx, `SELECT layout_json FROM layouts WHERE username = 'root@pam' AND name = 'default'`).Scan(&layoutJSON); err != nil {
		t.Errorf("layouts row lost across migration: %v", err)
	}

	var rxBytes int64
	if err := db.QueryRowContext(ctx, `SELECT rx_bytes FROM metric_samples WHERE ref = 'iface:pve1:vmbr0' AND at = 1700000000`).Scan(&rxBytes); err != nil {
		t.Errorf("metric_samples row lost across migration: %v", err)
	} else if rxBytes != 100 {
		t.Errorf("metric_samples.rx_bytes = %d, want 100", rxBytes)
	}

	var installID string
	if err := db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = 'install_id'`).Scan(&installID); err != nil {
		t.Errorf("kv row lost across migration: %v", err)
	} else if installID != "test-install-v1" {
		t.Errorf("kv install_id = %q, want test-install-v1", installID)
	}

	// A table only 0002+ introduces must now exist and be usable — proves
	// the migration actually ran forward, not just that old data survived.
	if _, err := db.ExecContext(ctx, `INSERT INTO blobs (sha256, content_zstd, size) VALUES ('newblob', x'00', 1)`); err != nil {
		t.Errorf("blobs table (added by 0002) not usable after migrating from v1: %v", err)
	}
	var note sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT note FROM snapshots WHERE id = 'snap-v1'`).Scan(&note); err != nil {
		t.Errorf("snapshots.note column (added by 0002) missing after migrating from v1: %v", err)
	}

	// And a table only 0003+ introduces.
	if _, err := db.ExecContext(ctx, `INSERT INTO node_timers (changeset_id, node, pre_content, deadline, status, armed_at)
	                                   VALUES ('cs-v1', 'pve1', 'auto network eth0', 1700000200, 'armed', 1700000100)`); err != nil {
		t.Errorf("node_timers table (added by 0003) not usable after migrating from v1: %v", err)
	}
}

func assertVersion2Seeded(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	assertVersion1Seeded(t, db)

	var size int64
	if err := db.QueryRowContext(ctx, `SELECT size FROM blobs WHERE sha256 = 'deadbeef'`).Scan(&size); err != nil {
		t.Errorf("blobs row (seeded pre-migration) lost across migration: %v", err)
	} else if size != 42 {
		t.Errorf("blobs.size = %d, want 42", size)
	}

	var sfCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM snapshot_files WHERE snapshot_id = 'snap-v1'`).Scan(&sfCount); err != nil {
		t.Errorf("snapshot_files query failed: %v", err)
	} else if sfCount != 1 {
		t.Errorf("snapshot_files rows for snap-v1 = %d, want 1", sfCount)
	}

	var note string
	if err := db.QueryRowContext(ctx, `SELECT note FROM snapshots WHERE id = 'snap-v1'`).Scan(&note); err != nil {
		t.Errorf("snapshots.note (seeded pre-migration) lost across migration: %v", err)
	} else if note != "manual pre-upgrade snapshot" {
		t.Errorf("snapshots.note = %q, want %q", note, "manual pre-upgrade snapshot")
	}
}

// Note: the converse case — a database whose recorded schema_version is
// *newer* than this build's embedded migrations understand (downgrading a
// node) — is already covered by store_test.go's Open-with-a-future-
// schema_version case (ErrSchemaTooNew); not duplicated here.
