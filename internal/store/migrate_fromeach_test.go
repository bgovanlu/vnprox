package store

// T-606 (planning/tasks/phase-6.md#T-606) established this harness: "upgrade
// path: migration tests from each prior tagged schema". This repository has
// cut no release tags (`git tag -l` is empty as of T-1807), so "each prior
// tagged schema" is, in practice, exactly "each prior schema_version that
// has ever shipped" — 0 (a brand-new, pre-0001 database; i.e. a fresh
// install of the very first release) through latest-1. Once real release
// tags exist, each of them pins one of these version numbers (a tag's
// go.mod-embedded migrations determine the schema_version its build
// understood), so this harness stays meaningful without depending on tag
// names at all: it is parameterized purely over schema_version, which is
// what "an install upgrading from an older release" actually differs by.
//
// T-1807 ("Migration upgrade-chain testing") extended this from the original
// two hand-written cases (versions 1 and 2) to every shipped version (1
// through the current latest-1, 32 as of schema 33): a v1.0-era database had
// never actually been walked all the way up to current. versionSeeds below
// is the fixture corpus — generated REPRODUCIBLY (freeze at version V by
// replaying migrations/*.sql 1..V exactly as an old vnproxd build left them,
// then execute this file's own seed function for that version), never a
// committed binary blob. Each entry seeds ONLY the rows/columns that
// version's own migration introduced; freezeAndSeed composes every
// registered version 1..V in order to build any version's full cumulative
// on-disk shape, exactly what a real node upgrading from that version would
// have accumulated release over release.
//
// Each test case: build a database frozen at schema_version V (openFrozenAt,
// migrations 1..V only — what an old vnproxd build left on disk), seed it
// with representative data in that version's shape (freezeAndSeed), then run
// this build's full migrate() over it (what happens on `apt install`
// upgrading vnprox on that node) and assert every table's data survived
// (assertSeededThrough) — per table, with real column values checked, not
// just "migrate() returned nil". TestMigrate_FromEachPriorSchemaVersion below
// fails loudly at setup if any version in 1..latest-1 has no versionSeeds
// entry, so a new migration landing without a matching seed/assert pair is a
// build failure, not a silent coverage gap.
//
// TestMigrate_DestructiveMigrationIsCaught (below) proves these assertions
// have teeth (AC3): it injects a synthetic, deliberately data-dropping extra
// migration on top of the real chain and confirms the SAME assertion
// (assertV1) used throughout this file actually fails when rows are lost —
// i.e. that a green TestMigrate_FromEachPriorSchemaVersion is real evidence
// of data preservation, not a tautology.

import (
	"context"
	"database/sql"
	"fmt"
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
	return openFrozenAtPath(t, filepath.Join(t.TempDir(), "vnprox.db"), version)
}

// openFrozenAtPath is openFrozenAt with a caller-chosen file path. Split out
// by T-1901, whose backup/restore-across-a-schema-upgrade test needs the
// frozen database to live at a path it can hand to internal/backup rather
// than in an anonymous t.TempDir(). openFrozenAt's own behaviour is
// unchanged — it is now a one-line wrapper.
func openFrozenAtPath(t *testing.T, path string, version int) *sql.DB {
	t.Helper()
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
	if pingErr := db.PingContext(context.Background()); pingErr != nil {
		t.Fatalf("ping: %v", pingErr)
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

// mustExec runs a seed INSERT/UPDATE and fails the test immediately if it
// errors — every versionSeeds seed function below is built from these, one
// statement per row, so a constraint violation (a bad fixture) points
// straight at the offending statement.
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("seed exec failed (%s): %v", query, err)
	}
}

// versionSeed pairs one schema version's own seed step (insert representative
// rows into whatever that version's migration introduced — a new table, or a
// value in a column an ALTER TABLE added) with the assertion that those rows
// (and every earlier version's) survived a subsequent migrate-to-latest.
type versionSeed struct {
	seed   func(t *testing.T, db *sql.DB)
	assert func(t *testing.T, db *sql.DB)
}

// versionSeeds is the fixture corpus: one entry per shipped schema version.
// TestMigrate_FromEachPriorSchemaVersion requires every version from 1 to
// latest-1 to have an entry here (T-1807 AC1) — a new migration landing
// without a matching entry fails that test at setup, loudly, rather than
// silently narrowing coverage.
var versionSeeds = map[int]versionSeed{
	1:  {seedV1, assertV1},
	2:  {seedV2, assertV2},
	3:  {seedV3, assertV3},
	4:  {seedV4, assertV4},
	5:  {seedV5, assertV5},
	6:  {seedV6, assertV6},
	7:  {seedV7, assertV7},
	8:  {seedV8, assertV8},
	9:  {seedV9, assertV9},
	10: {seedV10, assertV10},
	11: {seedV11, assertV11},
	12: {seedV12, assertV12},
	13: {seedV13, assertV13},
	14: {seedV14, assertV14},
	15: {seedV15, assertV15},
	16: {seedV16, assertV16},
	17: {seedV17, assertV17},
	18: {seedV18, assertV18},
	19: {seedV19, assertV19},
	20: {seedV20, assertV20},
	21: {seedV21, assertV21},
	22: {seedV22, assertV22},
	23: {seedV23, assertV23},
	24: {seedV24, assertV24},
	25: {seedV25, assertV25},
	26: {seedV26, assertV26},
	27: {seedV27, assertV27},
	28: {seedV28, assertV28},
	29: {seedV29, assertV29},
	30: {seedV30, assertV30},
	31: {seedV31, assertV31},
	32: {seedV32, assertV32},
	33: {seedV33, assertV33},
	34: {seedV34, assertV34},
	35: {seedV35, assertV35},
	36: {seedV36, assertV36},
	37: {seedV37, assertV37},
	38: {seedV38, assertV38},
	39: {seedV39, assertV39},
	40: {seedV40, assertV40},
	41: {seedV41, assertV41},
}

// freezeAndSeed populates db (already frozen at schema_version upto via
// openFrozenAt) with every registered version 1..upto's own representative
// rows, in order — exactly the cumulative shape a real node would have
// accumulated release over release.
func freezeAndSeed(t *testing.T, db *sql.DB, upto int) {
	t.Helper()
	for v := 1; v <= upto; v++ {
		vs, ok := versionSeeds[v]
		if !ok || vs.seed == nil {
			continue
		}
		vs.seed(t, db)
	}
}

// assertSeededThrough asserts every registered version 1..upto's own rows
// (per freezeAndSeed) are still present and correct.
func assertSeededThrough(t *testing.T, db *sql.DB, upto int) {
	t.Helper()
	for v := 1; v <= upto; v++ {
		vs, ok := versionSeeds[v]
		if !ok || vs.assert == nil {
			continue
		}
		vs.assert(t, db)
	}
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

	// AC1: every schema version from 1 to current must have a fixture. Fail
	// setup loudly, for the whole test, if a version in range is missing an
	// entry rather than quietly skipping it — this is what keeps the corpus
	// honest as migrations accrue.
	//
	// This gate was briefly relaxed to "every version that ships a migration
	// file", during a wave where three branches each held a pre-assigned
	// migration number and numbering was temporarily sparse. Numbering is
	// dense again (0040, 0041, 0042 all landed), so the stronger form is
	// restored: a gap here means a version some database really could sit at
	// has no upgrade fixture, and that is exactly what this test exists to
	// catch.
	for v := 1; v < latest; v++ {
		if _, ok := versionSeeds[v]; !ok {
			t.Fatalf("no versionSeeds fixture registered for schema version %d — add seedV%d/assertV%d "+
				"(see this file's package doc comment) and register it in versionSeeds; T-1807 AC1 requires "+
				"every version 1..%d to have one", v, v, v, latest-1)
		}
	}

	t.Run("version 0 (fresh install, no schema at all)", func(t *testing.T) {
		db := openFrozenAt(t, 0)
		ctx := context.Background()

		gotBefore, err := currentSchemaVersion(ctx, db)
		if err != nil {
			t.Fatalf("currentSchemaVersion before migrate: %v", err)
		}
		if gotBefore != 0 {
			t.Fatalf("frozen schema_version = %d, want 0 (freezing helper bug)", gotBefore)
		}

		if migrateErr := migrate(ctx, db); migrateErr != nil {
			t.Fatalf("migrate() from a brand-new file to latest: %v", migrateErr)
		}

		gotAfter, err := currentSchemaVersion(ctx, db)
		if err != nil {
			t.Fatalf("currentSchemaVersion after migrate: %v", err)
		}
		if gotAfter != latest {
			t.Errorf("schema_version after migrate = %d, want latest (%d)", gotAfter, latest)
		}

		if migrateErr := migrate(ctx, db); migrateErr != nil {
			t.Fatalf("re-running migrate() on an already-latest database: %v", migrateErr)
		}
	})

	for v := 1; v < latest; v++ {
		v := v
		t.Run(fmt.Sprintf("version %d", v), func(t *testing.T) {
			db := openFrozenAt(t, v)
			ctx := context.Background()

			gotBefore, err := currentSchemaVersion(ctx, db)
			if err != nil {
				t.Fatalf("currentSchemaVersion before seeding: %v", err)
			}
			if gotBefore != v {
				t.Fatalf("frozen schema_version = %d, want %d (freezing helper bug)", gotBefore, v)
			}

			freezeAndSeed(t, db, v)

			// This is the actual thing being tested: the same migrate() an
			// upgraded vnproxd runs against this node's existing database on
			// startup (store.Open calls it unconditionally).
			if migrateErr := migrate(ctx, db); migrateErr != nil {
				t.Fatalf("migrate() from version %d to latest: %v", v, migrateErr)
			}

			gotAfter, err := currentSchemaVersion(ctx, db)
			if err != nil {
				t.Fatalf("currentSchemaVersion after migrate: %v", err)
			}
			if gotAfter != latest {
				t.Errorf("schema_version after migrate = %d, want latest (%d)", gotAfter, latest)
			}

			// AC2: every row seeded at this version (and every version
			// before it) must still be present and correct — per table.
			assertSeededThrough(t, db, v)

			// Migrating an already-latest DB again (the "every subsequent
			// `apt install vnprox` on an already-current node" case, and
			// simply restarting the daemon) must be a no-op, not an error —
			// migrate()'s "skip if m.version <= current" loop guard is what
			// this proves.
			if migrateErr := migrate(ctx, db); migrateErr != nil {
				t.Fatalf("re-running migrate() on an already-latest database: %v", migrateErr)
			}
		})
	}
}

// TestMigrate_DestructiveMigrationIsCaught is T-1807 AC3: a deliberately
// destructive migration, added on top of the real chain, must be caught by
// this file's own data-preservation assertions — proving those assertions
// have teeth rather than being decorative "migrate() returned nil" checks.
//
// It works by seeding v1's data (seedV1), applying a synthetic extra
// migration on top of the real chain that DELETEs some of those rows —
// simulating a future release whose migration ships a bug that drops data
// instead of only adding schema — and then running checkV1, the EXACT SAME
// data-preservation logic assertV1 (used by every other case in this file)
// wraps in t.Errorf calls. checkV1 returns problem strings instead of
// calling t.Errorf itself specifically so this test can assert "the check
// found problems" as an ordinary value, rather than needing the destructive
// scenario to run inside a sub-test whose expected failure would otherwise
// unconditionally fail this outer test too (t.Run's parent-fails-if-any-
// subtest-fails behavior applies regardless of what the parent does with
// the returned bool — there is no clean way to run a testing.T callback
// that is SUPPOSED to fail without the enclosing test failing alongside
// it). If checkV1 reports nothing wrong here, the destructive migration
// went undetected — that is the actual failure this test exists to catch.
func TestMigrate_DestructiveMigrationIsCaught(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	latest := latestSchemaVersion(migrations)

	destructive := migration{
		version: latest + 1,
		name:    "9999_destructive_probe_TEST_ONLY.sql",
		// A real destructive migration bug would rarely be this on-the-nose
		// (more often a lossy CREATE-copy-DROP-rename table rebuild that
		// forgets a column), but a bare DELETE against tables checkV1
		// checks is the simplest thing that proves the point: if this goes
		// unnoticed, nothing subtler would be caught either. sessions/kv
		// only (not changesets): snapshots.changeset_id REFERENCES
		// changesets(id) with foreign_keys=1 enforced (openFrozenAt's DSN),
		// so deleting changesets while snap-v1 still references cs-v1 would
		// fail on a FOREIGN KEY constraint rather than silently succeed —
		// that would test SQLite's own FK enforcement, not this file's
		// data-preservation assertions.
		sql: `DELETE FROM sessions; DELETE FROM kv WHERE k = 'install_id';`,
	}

	db := openFrozenAt(t, 1)
	ctx := context.Background()
	seedV1(t, db)

	if err := migrate(ctx, db); err != nil {
		t.Fatalf("migrate() to latest: %v", err)
	}
	if err := applyMigration(ctx, db, destructive); err != nil {
		t.Fatalf("applying synthetic destructive migration: %v", err)
	}

	problems := checkV1(db)
	if len(problems) == 0 {
		t.Fatal("TestMigrate_DestructiveMigrationIsCaught: checkV1 found NO problems after the synthetic " +
			"destructive migration deleted sessions/kv rows — the data-preservation assertions failed to " +
			"notice data loss. That means this file's assertions are decorative, not real (T-1807 AC3 failed).")
	}
	t.Logf("destructive migration correctly detected — data-preservation checks caught %d problem(s): %v", len(problems), problems)
}

// ---------------------------------------------------------------------
// Version 1 — 0001_init.sql: sessions, changesets, snapshots, audit_log,
// layouts, metric_samples, kv.
// ---------------------------------------------------------------------

func seedV1(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO sessions (id, username, realm, pve_ticket_enc, csrf_token_enc, caps_json, created_at, expires_at)
	      VALUES ('sess-1', 'root@pam', 'pam', x'01020304', x'05060708', '{"sysModify":true}', 1700000000, 1700007200)`)
	mustExec(t, db, `INSERT INTO changesets (id, title, author, status, ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, created_at, updated_at)
	      VALUES ('cs-v1', 'pre-upgrade change', 'root@pam', 'committed', '[]', '[]', '{}', '{}', NULL, 1700000000, 1700000100)`)
	mustExec(t, db, `INSERT INTO snapshots (id, changeset_id, taken_at, kind, files_json)
	      VALUES ('snap-v1', 'cs-v1', 1700000000, 'pre', '[{"node":"pve1","path":"/etc/network/interfaces","sha256":"deadbeef"}]')`)
	mustExec(t, db, `INSERT INTO audit_log (at, username, action, target, changeset_id, result, detail_json)
	      VALUES (1700000000, 'root@pam', 'changeset.apply', NULL, 'cs-v1', 'success', '{}')`)
	mustExec(t, db, `INSERT INTO layouts (username, name, layout_json, updated_at)
	      VALUES ('root@pam', 'default', '{"nodes":[]}', 1700000000)`)
	mustExec(t, db, `INSERT INTO metric_samples (ref, at, rx_bytes, tx_bytes, rx_pkts, tx_pkts, rx_errs, tx_errs, rx_drop, tx_drop)
	      VALUES ('iface:pve1:vmbr0', 1700000000, 100, 200, 1, 2, 0, 0, 0, 0)`)
	mustExec(t, db, `INSERT INTO kv (k, v) VALUES ('install_id', 'test-install-v1')`)
}

func assertV1(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, problem := range checkV1(db) {
		t.Error(problem)
	}
}

// checkV1 is assertV1's assertion logic pulled out into a plain function
// that reports problems as returned strings instead of calling t.Errorf
// directly. It exists so TestMigrate_DestructiveMigrationIsCaught can reuse
// the EXACT SAME data-preservation checks every other case in this file
// relies on, without tripping over a Go testing.T quirk: a subtest's
// failure (t.Errorf/t.Fatalf inside a t.Run callback) always marks the
// PARENT test as failed too, regardless of what the parent does with
// t.Run's returned bool afterward — there is no way to run a testing.T
// callback that is "expected to fail" without the outer test failing right
// alongside it. Routing through a plain function sidesteps that entirely:
// the destructive-migration test calls checkV1 directly and asserts
// len(problems) > 0 itself, so the outer test's own pass/fail reflects only
// its own explicit check, not a fight against the testing package's
// subtest-propagation behavior.
func checkV1(db *sql.DB) []string {
	ctx := context.Background()
	var problems []string

	var username, status string
	if err := db.QueryRowContext(ctx, `SELECT username FROM sessions WHERE id = 'sess-1'`).Scan(&username); err != nil {
		problems = append(problems, fmt.Sprintf("sessions row lost across migration: %v", err))
	} else if username != "root@pam" {
		problems = append(problems, fmt.Sprintf("sessions.username = %q, want root@pam", username))
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM changesets WHERE id = 'cs-v1'`).Scan(&status); err != nil {
		problems = append(problems, fmt.Sprintf("changesets row lost across migration: %v", err))
	} else if status != "committed" {
		problems = append(problems, fmt.Sprintf("changesets.status = %q, want committed", status))
	}

	var filesJSON string
	if err := db.QueryRowContext(ctx, `SELECT files_json FROM snapshots WHERE id = 'snap-v1'`).Scan(&filesJSON); err != nil {
		problems = append(problems, fmt.Sprintf("snapshots row lost across migration: %v", err))
	}

	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE changeset_id = 'cs-v1'`).Scan(&auditCount); err != nil {
		problems = append(problems, fmt.Sprintf("audit_log query failed: %v", err))
	} else if auditCount != 1 {
		problems = append(problems, fmt.Sprintf("audit_log rows for cs-v1 = %d, want 1", auditCount))
	}

	var layoutJSON string
	if err := db.QueryRowContext(ctx, `SELECT layout_json FROM layouts WHERE username = 'root@pam' AND name = 'default'`).Scan(&layoutJSON); err != nil {
		problems = append(problems, fmt.Sprintf("layouts row lost across migration: %v", err))
	}

	var rxBytes int64
	if err := db.QueryRowContext(ctx, `SELECT rx_bytes FROM metric_samples WHERE ref = 'iface:pve1:vmbr0' AND at = 1700000000`).Scan(&rxBytes); err != nil {
		problems = append(problems, fmt.Sprintf("metric_samples row lost across migration: %v", err))
	} else if rxBytes != 100 {
		problems = append(problems, fmt.Sprintf("metric_samples.rx_bytes = %d, want 100", rxBytes))
	}

	var installID string
	if err := db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = 'install_id'`).Scan(&installID); err != nil {
		problems = append(problems, fmt.Sprintf("kv row lost across migration: %v", err))
	} else if installID != "test-install-v1" {
		problems = append(problems, fmt.Sprintf("kv install_id = %q, want test-install-v1", installID))
	}

	return problems
}

// ---------------------------------------------------------------------
// Version 2 — 0002_snapshot_blobs.sql: blobs, snapshot_files, snapshots.note.
// ---------------------------------------------------------------------

func seedV2(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO blobs (sha256, content_zstd, size) VALUES ('deadbeef', x'28b52ffd', 42)`)
	mustExec(t, db, `INSERT INTO snapshot_files (snapshot_id, node, path, sha256) VALUES ('snap-v1', 'pve1', '/etc/network/interfaces', 'deadbeef')`)
	mustExec(t, db, `UPDATE snapshots SET note = 'manual pre-upgrade snapshot' WHERE id = 'snap-v1'`)
}

func assertV2(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()

	var size int64
	if err := db.QueryRowContext(ctx, `SELECT size FROM blobs WHERE sha256 = 'deadbeef'`).Scan(&size); err != nil {
		t.Errorf("blobs row lost across migration: %v", err)
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
		t.Errorf("snapshots.note lost across migration: %v", err)
	} else if note != "manual pre-upgrade snapshot" {
		t.Errorf("snapshots.note = %q, want %q", note, "manual pre-upgrade snapshot")
	}
}

// ---------------------------------------------------------------------
// Version 3 — 0003_node_timers.sql
// ---------------------------------------------------------------------

func seedV3(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO node_timers (changeset_id, node, pre_content, deadline, status, armed_at, resolved_at, error)
	      VALUES ('cs-v1', 'pve1', 'auto vmbr0
iface vmbr0 inet static
	address 10.0.0.1/24', 1700000300, 'armed', 1700000100, NULL, NULL)`)
}

func assertV3(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM node_timers WHERE changeset_id = 'cs-v1' AND node = 'pve1'`).Scan(&status); err != nil {
		t.Errorf("node_timers row lost across migration: %v", err)
	} else if status != "armed" {
		t.Errorf("node_timers.status = %q, want armed", status)
	}
}

// ---------------------------------------------------------------------
// Version 4 — 0004_blueprints.sql
// ---------------------------------------------------------------------

func seedV4(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO blueprints (id, name, blueprint_json, created_by, created_at, updated_at)
	      VALUES ('bp-v4', 'Two-tier LAN', '{"id":"bp-v4","name":"Two-tier LAN"}', 'root@pam', 1700000400, 1700000400)`)
}

func assertV4(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM blueprints WHERE id = 'bp-v4'`).Scan(&name); err != nil {
		t.Errorf("blueprints row lost across migration: %v", err)
	} else if name != "Two-tier LAN" {
		t.Errorf("blueprints.name = %q, want Two-tier LAN", name)
	}
}

// ---------------------------------------------------------------------
// Version 5 — 0005_sim_divergence_findings.sql
// ---------------------------------------------------------------------

func seedV5(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO sim_divergence_findings (id, src_ref, dst_kind, dst_ref, dst_ip, proto, port, simulated_verdict, observed_outcome, detail, created_at, updated_at)
	      VALUES ('probe:sim_divergence|v5', 'guest:pve1:100', 'ip', '', '10.0.0.5', 'tcp', 443, 'allow', 'blocked', 'unexpected block', 1700000500, 1700000500)`)
}

func assertV5(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var outcome string
	if err := db.QueryRowContext(ctx, `SELECT observed_outcome FROM sim_divergence_findings WHERE id = 'probe:sim_divergence|v5'`).Scan(&outcome); err != nil {
		t.Errorf("sim_divergence_findings row lost across migration: %v", err)
	} else if outcome != "blocked" {
		t.Errorf("sim_divergence_findings.observed_outcome = %q, want blocked", outcome)
	}
}

// ---------------------------------------------------------------------
// Version 6 — 0006_annotations.sql
// ---------------------------------------------------------------------

func seedV6(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO annotations (id, ref, content, created_by, created_at, updated_at)
	      VALUES ('ann-v6', 'guest:pve1:100', 'needs review before next maintenance window', 'root@pam', 1700000600, 1700000600)`)
}

func assertV6(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var content string
	if err := db.QueryRowContext(ctx, `SELECT content FROM annotations WHERE id = 'ann-v6'`).Scan(&content); err != nil {
		t.Errorf("annotations row lost across migration: %v", err)
	} else if content != "needs review before next maintenance window" {
		t.Errorf("annotations.content = %q, unexpected", content)
	}
}

// ---------------------------------------------------------------------
// Version 7 — 0007_flows.sql
// ---------------------------------------------------------------------

func seedV7(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO flow_samples (at, node, src_ip, dst_ip, src_port, dst_port, proto, bytes, packets, vlan, src_ref, dst_ref, ingress_if, egress_if, source)
	      VALUES (1700000700, 'pve1', '10.0.0.5', '10.0.0.6', 51000, 443, 6, 1500, 10, 0, '', '', 0, 0, 'sflow')`)
}

func assertV7(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var bytesCount int64
	if err := db.QueryRowContext(ctx, `SELECT bytes FROM flow_samples WHERE at = 1700000700 AND node = 'pve1'`).Scan(&bytesCount); err != nil {
		t.Errorf("flow_samples row lost across migration: %v", err)
	} else if bytesCount != 1500 {
		t.Errorf("flow_samples.bytes = %d, want 1500", bytesCount)
	}
}

// ---------------------------------------------------------------------
// Version 8 — 0008_alert_rules.sql
// ---------------------------------------------------------------------

func seedV8(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO alert_rules (id, name, enabled, source_filter_json, severity_filter_json, target_kind, target_url, target_secret_enc, created_at, updated_at)
	      VALUES ('ar-v8', 'webhook rule', 1, NULL, NULL, 'generic', 'https://example.invalid/hook', NULL, 1700000800, 1700000800)`)
	mustExec(t, db, `INSERT INTO alert_deliveries (id, rule_id, finding_id, at, attempt, status, error)
	      VALUES ('ad-v8', 'ar-v8', 'finding-v8', 1700000800, 1, 'delivered', NULL)`)
}

func assertV8(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var enabled int
	if err := db.QueryRowContext(ctx, `SELECT enabled FROM alert_rules WHERE id = 'ar-v8'`).Scan(&enabled); err != nil {
		t.Errorf("alert_rules row lost across migration: %v", err)
	} else if enabled != 1 {
		t.Errorf("alert_rules.enabled = %d, want 1", enabled)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM alert_deliveries WHERE id = 'ad-v8'`).Scan(&status); err != nil {
		t.Errorf("alert_deliveries row lost across migration: %v", err)
	} else if status != "delivered" {
		t.Errorf("alert_deliveries.status = %q, want delivered", status)
	}
}

// ---------------------------------------------------------------------
// Version 9 — 0009_finding_events.sql
// ---------------------------------------------------------------------

func seedV9(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO finding_events (finding_id, at, transition) VALUES ('finding-v9', 1700000900, 'new')`)
}

func assertV9(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var transition string
	if err := db.QueryRowContext(ctx, `SELECT transition FROM finding_events WHERE finding_id = 'finding-v9' AND at = 1700000900`).Scan(&transition); err != nil {
		t.Errorf("finding_events row lost across migration: %v", err)
	} else if transition != "new" {
		t.Errorf("finding_events.transition = %q, want new", transition)
	}
}

// ---------------------------------------------------------------------
// Version 10 — 0010_changeset_schedules.sql
// ---------------------------------------------------------------------

func seedV10(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO changeset_schedules (changeset_id, window_start, window_end, confirm_timeout_sec, missed_window_policy, callback_token_hash, status, created_by, created_at, fired_at, cancelled_at)
	      VALUES ('cs-v10', 1700001000, 1700001600, 120, 'skip', 'deadbeefcafe', 'pending', 'root@pam', 1700001000, NULL, NULL)`)
}

func assertV10(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM changeset_schedules WHERE changeset_id = 'cs-v10'`).Scan(&status); err != nil {
		t.Errorf("changeset_schedules row lost across migration: %v", err)
	} else if status != "pending" {
		t.Errorf("changeset_schedules.status = %q, want pending", status)
	}
}

// ---------------------------------------------------------------------
// Version 11 — 0011_api_tokens.sql
// ---------------------------------------------------------------------

func seedV11(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO api_tokens (id, name, token_hash, scopes_json, created_by, created_at, last_used_at, revoked_at)
	      VALUES ('tok-v11', 'automation', 'abc123hash', '["automation"]', 'root@pam', 1700001100, NULL, NULL)`)
	mustExec(t, db, `INSERT INTO webhooks (id, url, events_json, secret_enc, created_by, created_at, consecutive_failures, last_attempt_at, last_success_at, last_error)
	      VALUES ('wh-v11', 'https://example.invalid/webhook', NULL, x'01', 'root@pam', 1700001100, 0, NULL, NULL, NULL)`)
}

func assertV11(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM api_tokens WHERE id = 'tok-v11'`).Scan(&name); err != nil {
		t.Errorf("api_tokens row lost across migration: %v", err)
	} else if name != "automation" {
		t.Errorf("api_tokens.name = %q, want automation", name)
	}
	var wURL string
	if err := db.QueryRowContext(ctx, `SELECT url FROM webhooks WHERE id = 'wh-v11'`).Scan(&wURL); err != nil {
		t.Errorf("webhooks row lost across migration: %v", err)
	} else if wURL != "https://example.invalid/webhook" {
		t.Errorf("webhooks.url = %q, unexpected", wURL)
	}
}

// ---------------------------------------------------------------------
// Version 12 — 0012_pinned_spec.sql
// ---------------------------------------------------------------------

func seedV12(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO pinned_spec (id, content, pinned_by, pinned_at)
	      VALUES (1, 'specVersion: 1
nodes: []', 'root@pam', 1700001200)`)
}

func assertV12(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var pinnedBy string
	if err := db.QueryRowContext(ctx, `SELECT pinned_by FROM pinned_spec WHERE id = 1`).Scan(&pinnedBy); err != nil {
		t.Errorf("pinned_spec row lost across migration: %v", err)
	} else if pinnedBy != "root@pam" {
		t.Errorf("pinned_spec.pinned_by = %q, want root@pam", pinnedBy)
	}
}

// ---------------------------------------------------------------------
// Version 13 — 0013_latency_samples.sql
// ---------------------------------------------------------------------

func seedV13(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO latency_samples (link_id, fabric, from_node, to_node, at, rtt_ms, loss_pct)
	      VALUES ('corosync:ring0|pve1->pve2', 'corosync', 'pve1', 'pve2', 1700001300, 1.5, 0)`)
}

func assertV13(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var rtt float64
	if err := db.QueryRowContext(ctx, `SELECT rtt_ms FROM latency_samples WHERE link_id = 'corosync:ring0|pve1->pve2' AND at = 1700001300`).Scan(&rtt); err != nil {
		t.Errorf("latency_samples row lost across migration: %v", err)
	} else if rtt != 1.5 {
		t.Errorf("latency_samples.rtt_ms = %v, want 1.5", rtt)
	}
}

// ---------------------------------------------------------------------
// Version 14 — 0014_capture_sessions.sql
// ---------------------------------------------------------------------

func seedV14(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO capture_sessions (id, group_id, target_ref, node, nodes_json, filter, caps_json, status, started_by, started_at, stopped_at, file_path, file_bytes, packets)
	      VALUES ('cap-v14', 'grp-v14', 'guest:pve1:100', 'pve1', '["pve1"]', 'tcp port 443', '{"maxBytes":1000000}', 'completed', 'root@pam', 1700001400, 1700001500, '/var/lib/vnprox/captures/cap-v14.pcap', 2048, 20)`)
}

func assertV14(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM capture_sessions WHERE id = 'cap-v14'`).Scan(&status); err != nil {
		t.Errorf("capture_sessions row lost across migration: %v", err)
	} else if status != "completed" {
		t.Errorf("capture_sessions.status = %q, want completed", status)
	}
}

// ---------------------------------------------------------------------
// Version 15 — 0015_guest_interior_toggles.sql
// ---------------------------------------------------------------------

func seedV15(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO guest_interior_toggles (ref, enabled, updated_by, updated_at)
	      VALUES ('guest:pve1:100', 1, 'root@pam', 1700001500)`)
}

func assertV15(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var enabled int
	if err := db.QueryRowContext(ctx, `SELECT enabled FROM guest_interior_toggles WHERE ref = 'guest:pve1:100'`).Scan(&enabled); err != nil {
		t.Errorf("guest_interior_toggles row lost across migration: %v", err)
	} else if enabled != 1 {
		t.Errorf("guest_interior_toggles.enabled = %d, want 1", enabled)
	}
}

// ---------------------------------------------------------------------
// Version 16 — 0016_wireguard.sql
// ---------------------------------------------------------------------

func seedV16(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO wireguard_tunnels (id, node, if_name, private_key_enc, public_key, listen_port, addresses_json, mtu, carrier, created_by, created_at)
	      VALUES ('wg-v16', 'pve1', 'wg0', x'aa', 'pubkey-v16', 51820, '["10.10.0.1/24"]', 1420, 'vmbr0', 'root@pam', 1700001600)`)
	mustExec(t, db, `INSERT INTO wireguard_peers (tunnel_id, public_key, endpoint, allowed_ips_json, preshared_key_enc, keepalive_sec, external, cluster_id)
	      VALUES ('wg-v16', 'peerpub-v16', '203.0.113.5:51820', '["10.10.0.2/32"]', NULL, 25, 0, '')`)
}

func assertV16(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var pub string
	if err := db.QueryRowContext(ctx, `SELECT public_key FROM wireguard_tunnels WHERE id = 'wg-v16'`).Scan(&pub); err != nil {
		t.Errorf("wireguard_tunnels row lost across migration: %v", err)
	} else if pub != "pubkey-v16" {
		t.Errorf("wireguard_tunnels.public_key = %q, unexpected", pub)
	}
	var endpoint string
	if err := db.QueryRowContext(ctx, `SELECT endpoint FROM wireguard_peers WHERE tunnel_id = 'wg-v16' AND public_key = 'peerpub-v16'`).Scan(&endpoint); err != nil {
		t.Errorf("wireguard_peers row lost across migration: %v", err)
	} else if endpoint != "203.0.113.5:51820" {
		t.Errorf("wireguard_peers.endpoint = %q, unexpected", endpoint)
	}
}

// ---------------------------------------------------------------------
// Version 17 — 0017_ingress_targets.sql
// ---------------------------------------------------------------------

func seedV17(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO ingress_targets (id, kind, address, credential_enc, added_by, added_at)
	      VALUES ('ing-v17', 'haproxy', 'https://10.0.0.10:8404', NULL, 'root@pam', 1700001700)`)
}

func assertV17(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var kind string
	if err := db.QueryRowContext(ctx, `SELECT kind FROM ingress_targets WHERE id = 'ing-v17'`).Scan(&kind); err != nil {
		t.Errorf("ingress_targets row lost across migration: %v", err)
	} else if kind != "haproxy" {
		t.Errorf("ingress_targets.kind = %q, want haproxy", kind)
	}
}

// ---------------------------------------------------------------------
// Version 18 — 0018_wan.sql
// ---------------------------------------------------------------------

func seedV18(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO wan_targets (node, uplink, host, created_at) VALUES ('pve1', 'vmbr0', '1.1.1.1', 1700001800)`)
	mustExec(t, db, `INSERT INTO wan_probe_samples (link_id, from_node, uplink, to_node, at, rtt_ms, loss_pct)
	      VALUES ('wan:vmbr0|pve1->1.1.1.1', 'pve1', 'vmbr0', '1.1.1.1', 1700001800, 8.2, 0)`)
}

func assertV18(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var host string
	if err := db.QueryRowContext(ctx, `SELECT host FROM wan_targets WHERE node = 'pve1' AND uplink = 'vmbr0'`).Scan(&host); err != nil {
		t.Errorf("wan_targets row lost across migration: %v", err)
	} else if host != "1.1.1.1" {
		t.Errorf("wan_targets.host = %q, want 1.1.1.1", host)
	}
	var rtt float64
	if err := db.QueryRowContext(ctx, `SELECT rtt_ms FROM wan_probe_samples WHERE link_id = 'wan:vmbr0|pve1->1.1.1.1' AND at = 1700001800`).Scan(&rtt); err != nil {
		t.Errorf("wan_probe_samples row lost across migration: %v", err)
	} else if rtt != 8.2 {
		t.Errorf("wan_probe_samples.rtt_ms = %v, want 8.2", rtt)
	}
}

// ---------------------------------------------------------------------
// Version 19 — 0019_k8s_clusters.sql
// ---------------------------------------------------------------------

func seedV19(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO k8s_clusters (id, name, kubeconfig_enc, added_by, added_at, cni_detected, status)
	      VALUES ('k8s-v19', 'prod-cluster', x'bb', 'root@pam', 1700001900, 'cilium', 'ok')`)
}

func assertV19(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM k8s_clusters WHERE id = 'k8s-v19'`).Scan(&name); err != nil {
		t.Errorf("k8s_clusters row lost across migration: %v", err)
	} else if name != "prod-cluster" {
		t.Errorf("k8s_clusters.name = %q, want prod-cluster", name)
	}
}

// ---------------------------------------------------------------------
// Version 20 — 0020_qos.sql
// ---------------------------------------------------------------------

func seedV20(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO qos_shapes (id, node, bridge, match_cidr, match_vlan, rate_mbit, ceil_mbit, priority, created_by, created_at, updated_at)
	      VALUES ('qos-v20', 'pve1', 'vmbr0', '', NULL, 100, 200, 1, 'root@pam', 1700002000, 1700002000)`)
}

func assertV20(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var rate int64
	if err := db.QueryRowContext(ctx, `SELECT rate_mbit FROM qos_shapes WHERE id = 'qos-v20'`).Scan(&rate); err != nil {
		t.Errorf("qos_shapes row lost across migration: %v", err)
	} else if rate != 100 {
		t.Errorf("qos_shapes.rate_mbit = %d, want 100", rate)
	}
}

// ---------------------------------------------------------------------
// Version 21 — 0021_clusters.sql: clusters table, plus changesets.cluster_id
// and audit_log.cluster_id (ALTER TABLE ADD COLUMN, applied to the existing
// v1 rows).
// ---------------------------------------------------------------------

func seedV21(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO clusters (id, name, api_url, credential_enc, status, added_by, added_at)
	      VALUES ('clus-v21', 'remote-dc', 'https://10.1.0.1:8006', x'cc', 'ok', 'root@pam', 1700002100)`)
	mustExec(t, db, `UPDATE changesets SET cluster_id = 'clus-v21' WHERE id = 'cs-v1'`)
	mustExec(t, db, `UPDATE audit_log SET cluster_id = 'clus-v21' WHERE changeset_id = 'cs-v1'`)
}

func assertV21(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM clusters WHERE id = 'clus-v21'`).Scan(&name); err != nil {
		t.Errorf("clusters row lost across migration: %v", err)
	} else if name != "remote-dc" {
		t.Errorf("clusters.name = %q, want remote-dc", name)
	}
	var csClusterID string
	if err := db.QueryRowContext(ctx, `SELECT cluster_id FROM changesets WHERE id = 'cs-v1'`).Scan(&csClusterID); err != nil {
		t.Errorf("changesets.cluster_id lost across migration: %v", err)
	} else if csClusterID != "clus-v21" {
		t.Errorf("changesets.cluster_id = %q, want clus-v21", csClusterID)
	}
	var auditClusterID string
	if err := db.QueryRowContext(ctx, `SELECT cluster_id FROM audit_log WHERE changeset_id = 'cs-v1'`).Scan(&auditClusterID); err != nil {
		t.Errorf("audit_log.cluster_id lost across migration: %v", err)
	} else if auditClusterID != "clus-v21" {
		t.Errorf("audit_log.cluster_id = %q, want clus-v21", auditClusterID)
	}
}

// ---------------------------------------------------------------------
// Version 22 — 0022_switches.sql
// ---------------------------------------------------------------------

func seedV22(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO switches (id, name, mgmt_addr, driver_type, credentials_enc, enabled, added_by, added_at)
	      VALUES ('sw-v22', 'core-switch-1', '10.2.0.1', 'openconfig', x'dd', 1, 'root@pam', 1700002200)`)
}

func assertV22(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var addr string
	if err := db.QueryRowContext(ctx, `SELECT mgmt_addr FROM switches WHERE id = 'sw-v22'`).Scan(&addr); err != nil {
		t.Errorf("switches row lost across migration: %v", err)
	} else if addr != "10.2.0.1" {
		t.Errorf("switches.mgmt_addr = %q, want 10.2.0.1", addr)
	}
}

// ---------------------------------------------------------------------
// Version 23 — 0023_external_subnets.sql
// ---------------------------------------------------------------------

func seedV23(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO external_subnets (id, cidr, label, source, description, created_by, created_at, updated_at)
	      VALUES ('ext-v23', '192.0.2.0/24', 'transit', 'manual', 'ISP transit block', 'root@pam', 1700002300, 1700002300)`)
}

func assertV23(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var label string
	if err := db.QueryRowContext(ctx, `SELECT label FROM external_subnets WHERE id = 'ext-v23'`).Scan(&label); err != nil {
		t.Errorf("external_subnets row lost across migration: %v", err)
	} else if label != "transit" {
		t.Errorf("external_subnets.label = %q, want transit", label)
	}
}

// ---------------------------------------------------------------------
// Version 24 — 0024_oidc.sql
// ---------------------------------------------------------------------

func seedV24(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO oidc_pve_links (id, cluster_id, oidc_group, pve_username, credential_enc, created_by, created_at)
	      VALUES ('oidc-v24', '', 'network-admins', 'automation@pve', x'ee', 'root@pam', 1700002400)`)
}

func assertV24(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var username string
	if err := db.QueryRowContext(ctx, `SELECT pve_username FROM oidc_pve_links WHERE id = 'oidc-v24'`).Scan(&username); err != nil {
		t.Errorf("oidc_pve_links row lost across migration: %v", err)
	} else if username != "automation@pve" {
		t.Errorf("oidc_pve_links.pve_username = %q, want automation@pve", username)
	}
}

// ---------------------------------------------------------------------
// Version 25 — 0025_flow_baselines.sql
// ---------------------------------------------------------------------

func seedV25(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO baseline_profiles (ref, profile_json, window_start, window_end, updated_at)
	      VALUES ('guest:pve1:100', '{"topTalkers":[]}', 1700000000, 1700002500, 1700002500)`)
}

func assertV25(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var profileJSON string
	if err := db.QueryRowContext(ctx, `SELECT profile_json FROM baseline_profiles WHERE ref = 'guest:pve1:100'`).Scan(&profileJSON); err != nil {
		t.Errorf("baseline_profiles row lost across migration: %v", err)
	} else if profileJSON != `{"topTalkers":[]}` {
		t.Errorf("baseline_profiles.profile_json = %q, unexpected", profileJSON)
	}
}

// ---------------------------------------------------------------------
// Version 26 — 0026_capacity_samples.sql
// ---------------------------------------------------------------------

func seedV26(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO capacity_aggregates (ref, kind, bucket_at, avg_utilization, max_utilization, created_at)
	      VALUES ('iface:pve1:vmbr0', 'link', 1700002600, 12.5, 45.0, 1700002600)`)
}

func assertV26(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var avg float64
	if err := db.QueryRowContext(ctx, `SELECT avg_utilization FROM capacity_aggregates WHERE ref = 'iface:pve1:vmbr0' AND kind = 'link' AND bucket_at = 1700002600`).Scan(&avg); err != nil {
		t.Errorf("capacity_aggregates row lost across migration: %v", err)
	} else if avg != 12.5 {
		t.Errorf("capacity_aggregates.avg_utilization = %v, want 12.5", avg)
	}
}

// ---------------------------------------------------------------------
// Version 27 — 0027_posture_scores.sql
// ---------------------------------------------------------------------

func seedV27(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO posture_scores (computed_at, overall, qualified, factors_json)
	      VALUES (1700002700, 82, 0, '[{"name":"segmentation","weight":1,"value":82}]')`)
}

func assertV27(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var overall int
	if err := db.QueryRowContext(ctx, `SELECT overall FROM posture_scores WHERE computed_at = 1700002700`).Scan(&overall); err != nil {
		t.Errorf("posture_scores row lost across migration: %v", err)
	} else if overall != 82 {
		t.Errorf("posture_scores.overall = %d, want 82", overall)
	}
}

// ---------------------------------------------------------------------
// Version 28 — 0028_changeset_origin.sql: changesets.origin/origin_token_id
// (ALTER TABLE ADD COLUMN, applied to the existing v1 row).
// ---------------------------------------------------------------------

func seedV28(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `UPDATE changesets SET origin = 'mcp', origin_token_id = 'tok-v28' WHERE id = 'cs-v1'`)
}

func assertV28(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var origin, tokenID string
	if err := db.QueryRowContext(ctx, `SELECT origin, origin_token_id FROM changesets WHERE id = 'cs-v1'`).Scan(&origin, &tokenID); err != nil {
		t.Errorf("changesets.origin/origin_token_id lost across migration: %v", err)
	} else if origin != "mcp" || tokenID != "tok-v28" {
		t.Errorf("changesets.origin/origin_token_id = %q/%q, want mcp/tok-v28", origin, tokenID)
	}
}

// ---------------------------------------------------------------------
// Version 29 — 0029_plugins.sql
// ---------------------------------------------------------------------

func seedV29(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO plugins (id, name, version, api_version, extension_points_json, capabilities_json, transport, endpoint, enabled, installed_by, installed_at)
	      VALUES ('com.acme.sonic-driver', 'Sonic Driver', '1.0.0', 'v1', '["switchDriver"]', '["netWrite"]', 'in-process', '', 1, 'root@pam', 1700002900)`)
}

func assertV29(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM plugins WHERE id = 'com.acme.sonic-driver'`).Scan(&name); err != nil {
		t.Errorf("plugins row lost across migration: %v", err)
	} else if name != "Sonic Driver" {
		t.Errorf("plugins.name = %q, want Sonic Driver", name)
	}
}

// ---------------------------------------------------------------------
// Version 30 — 0030_tenants.sql
// ---------------------------------------------------------------------

func seedV30(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO tenants (id, name, created_by, created_at) VALUES ('ten-v30', 'Team A', 'root@pam', 1700003000)`)
	mustExec(t, db, `INSERT INTO tenant_scopes (tenant_id, scope_ref) VALUES ('ten-v30', 'guest:pve1:100')`)
	mustExec(t, db, `INSERT INTO tenant_members (tenant_id, identity, role) VALUES ('ten-v30', 'alice@pve', 'member')`)
	mustExec(t, db, `INSERT INTO changeset_requests (changeset_id, tenant_id, requested_by, created_at, approved_by, approved_at)
	      VALUES ('cs-v30', 'ten-v30', 'alice@pve', 1700003000, '', 0)`)
}

func assertV30(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id = 'ten-v30'`).Scan(&name); err != nil {
		t.Errorf("tenants row lost across migration: %v", err)
	} else if name != "Team A" {
		t.Errorf("tenants.name = %q, want Team A", name)
	}
	var scopeCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tenant_scopes WHERE tenant_id = 'ten-v30'`).Scan(&scopeCount); err != nil {
		t.Errorf("tenant_scopes query failed: %v", err)
	} else if scopeCount != 1 {
		t.Errorf("tenant_scopes rows for ten-v30 = %d, want 1", scopeCount)
	}
	var role string
	if err := db.QueryRowContext(ctx, `SELECT role FROM tenant_members WHERE tenant_id = 'ten-v30' AND identity = 'alice@pve'`).Scan(&role); err != nil {
		t.Errorf("tenant_members row lost across migration: %v", err)
	} else if role != "member" {
		t.Errorf("tenant_members.role = %q, want member", role)
	}
	var tenantID string
	if err := db.QueryRowContext(ctx, `SELECT tenant_id FROM changeset_requests WHERE changeset_id = 'cs-v30'`).Scan(&tenantID); err != nil {
		t.Errorf("changeset_requests row lost across migration: %v", err)
	} else if tenantID != "ten-v30" {
		t.Errorf("changeset_requests.tenant_id = %q, want ten-v30", tenantID)
	}
}

// ---------------------------------------------------------------------
// Version 31 — 0031_ha.sql
// ---------------------------------------------------------------------

func seedV31(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO ha_lease (id, holder, term, expires_at, acquired_at, updated_at)
	      VALUES ('singleton', 'node-a', 1, 1700003100, 1700003000, 1700003050)`)
}

func assertV31(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var holder string
	if err := db.QueryRowContext(ctx, `SELECT holder FROM ha_lease WHERE id = 'singleton'`).Scan(&holder); err != nil {
		t.Errorf("ha_lease row lost across migration: %v", err)
	} else if holder != "node-a" {
		t.Errorf("ha_lease.holder = %q, want node-a", holder)
	}
}

// ---------------------------------------------------------------------
// Version 32 — 0032_cluster_wg_tunnel.sql: clusters.wg_tunnel_id (ALTER
// TABLE ADD COLUMN, applied to the v21 cluster row; needs both v16's
// wireguard_tunnels row and v21's clusters row already seeded, which
// freezeAndSeed guarantees by running every version in order).
// ---------------------------------------------------------------------

func seedV32(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `UPDATE clusters SET wg_tunnel_id = 'wg-v16' WHERE id = 'clus-v21'`)
}

func assertV32(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var tunnelID string
	if err := db.QueryRowContext(ctx, `SELECT wg_tunnel_id FROM clusters WHERE id = 'clus-v21'`).Scan(&tunnelID); err != nil {
		t.Errorf("clusters.wg_tunnel_id lost across migration: %v", err)
	} else if tunnelID != "wg-v16" {
		t.Errorf("clusters.wg_tunnel_id = %q, want wg-v16", tunnelID)
	}
}

// Note: the converse case — a database whose recorded schema_version is
// *newer* than this build's embedded migrations understand (downgrading a
// node) — is already covered by store_test.go's Open-with-a-future-
// schema_version case (ErrSchemaTooNew); not duplicated here.

// ---------------------------------------------------------------------
// Version 33 — 0033_changeset_revert_ticket.sql: changesets.
// revert_ticket_enc/revert_ticket_expires_at (ALTER TABLE ADD COLUMN,
// applied to v1's own cs-v1 changeset row, which freezeAndSeed guarantees
// is already seeded by the time this runs).
// ---------------------------------------------------------------------

func seedV33(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `UPDATE changesets SET revert_ticket_enc = x'0a0b0c0d', revert_ticket_expires_at = 1700099999 WHERE id = 'cs-v1'`)
}

func assertV33(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var expiresAt int64
	if err := db.QueryRowContext(ctx, `SELECT revert_ticket_expires_at FROM changesets WHERE id = 'cs-v1'`).Scan(&expiresAt); err != nil {
		t.Errorf("changesets.revert_ticket_expires_at lost across migration: %v", err)
	} else if expiresAt != 1700099999 {
		t.Errorf("changesets.revert_ticket_expires_at = %d, want 1700099999", expiresAt)
	}
}

// ---------------------------------------------------------------------
// Version 34 — 0034_changeset_review.sql: changeset_comments and
// changeset_approvals (T-2003). Both reference changesets(id) ON DELETE
// CASCADE, so both seed against v1's own cs-v1 row.
// ---------------------------------------------------------------------

func seedV34(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO changeset_comments (id, changeset_id, op_id, author, body, created_at)
		VALUES ('cmt-v34', 'cs-v1', 'op-1', 'alice', 'why this MTU?', 1700100000)`)
	mustExec(t, db, `INSERT INTO changeset_approvals (changeset_id, status, decided_by, reason, decided_at)
		VALUES ('cs-v1', 'approved', 'brian', '', 1700100001)`)
}

func assertV34(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var body, opID string
	if err := db.QueryRowContext(ctx, `SELECT body, op_id FROM changeset_comments WHERE id = 'cmt-v34'`).Scan(&body, &opID); err != nil {
		t.Errorf("changeset_comments row lost across migration: %v", err)
	} else if body != "why this MTU?" || opID != "op-1" {
		t.Errorf("changeset_comments row = (%q, %q), want (\"why this MTU?\", \"op-1\")", body, opID)
	}
	var status, decidedBy string
	if err := db.QueryRowContext(ctx, `SELECT status, decided_by FROM changeset_approvals WHERE changeset_id = 'cs-v1'`).Scan(&status, &decidedBy); err != nil {
		t.Errorf("changeset_approvals row lost across migration: %v", err)
	} else if status != "approved" || decidedBy != "brian" {
		t.Errorf("changeset_approvals row = (%q, %q), want (\"approved\", \"brian\")", status, decidedBy)
	}
}

func seedV35(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO finding_acks (finding_id, reason, acked_by, acked_at, expires_at)
		VALUES ('drift|pve1', 'known, tracked in TICKET-9', 'alice', 1700200000, 0)`)
}

func assertV35(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var reason, ackedBy string
	if err := db.QueryRowContext(ctx, `SELECT reason, acked_by FROM finding_acks WHERE finding_id = 'drift|pve1'`).Scan(&reason, &ackedBy); err != nil {
		t.Errorf("finding_acks row lost across migration: %v", err)
	} else if reason != "known, tracked in TICKET-9" || ackedBy != "alice" {
		t.Errorf("finding_acks row = (%q, %q), want (\"known, tracked in TICKET-9\", \"alice\")", reason, ackedBy)
	}
}

func seedV36(t *testing.T, db *sql.DB) {
	t.Helper()
	// The quiet-hours columns on the rule seeded at v8, plus a held event
	// in the durable deferral queue 0036 introduced.
	mustExec(t, db, `UPDATE alert_rules SET quiet_start = '22:00', quiet_end = '06:00', quiet_tz = 'Europe/Berlin',
		quiet_bypass_error = 1, digest_window_sec = 300 WHERE id = 'ar-v8'`)
	mustExec(t, db, `INSERT INTO alert_pending (id, rule_id, finding_id, finding_json, kind, at, flush_at, reason)
		VALUES ('ap-v36', 'ar-v8', 'finding-v36', '{"id":"finding-v36"}', 'new', 1700003600, 1700010800, 'quiet hours')`)
}

func assertV36(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var quietStart, quietTZ string
	var digestWindow int
	if err := db.QueryRowContext(ctx, `SELECT quiet_start, quiet_tz, digest_window_sec FROM alert_rules WHERE id = 'ar-v8'`).
		Scan(&quietStart, &quietTZ, &digestWindow); err != nil {
		t.Errorf("alert_rules quiet-hours columns lost across migration: %v", err)
	} else if quietStart != "22:00" || quietTZ != "Europe/Berlin" || digestWindow != 300 {
		t.Errorf("alert_rules quiet-hours = (%q, %q, %d), want (\"22:00\", \"Europe/Berlin\", 300)", quietStart, quietTZ, digestWindow)
	}

	var ruleID, reason string
	if err := db.QueryRowContext(ctx, `SELECT rule_id, reason FROM alert_pending WHERE id = 'ap-v36'`).Scan(&ruleID, &reason); err != nil {
		t.Errorf("alert_pending row lost across migration: %v", err)
	} else if ruleID != "ar-v8" || reason != "quiet hours" {
		t.Errorf("alert_pending row = (%q, %q), want (\"ar-v8\", \"quiet hours\")", ruleID, reason)
	}
}

func seedV37(t *testing.T, db *sql.DB) {
	t.Helper()
	// T-2601's installed policy set plus one rule's runtime bookkeeping.
	mustExec(t, db, `INSERT INTO policy_sets (cluster_id, revision, rules_json, updated_by, updated_at)
		VALUES ('', 3, '{"version":1,"rules":[{"id":"no-vlan1","description":"no guest on VLAN 1","severity":"deny"}]}', 'brian', 1700004000)`)
	mustExec(t, db, `INSERT INTO policy_rule_stats (cluster_id, rule_id, first_seen_at, last_matched_at, eval_count, match_count)
		VALUES ('', 'no-vlan1', 1700000000, 1700003000, 42, 7)`)
}

func assertV37(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var revision int64
	var updatedBy string
	if err := db.QueryRowContext(ctx, `SELECT revision, updated_by FROM policy_sets WHERE cluster_id = ''`).Scan(&revision, &updatedBy); err != nil {
		t.Errorf("policy_sets row lost across migration: %v", err)
	} else if revision != 3 || updatedBy != "brian" {
		t.Errorf("policy_sets row = (%d, %q), want (3, \"brian\")", revision, updatedBy)
	}

	var evalCount, matchCount int64
	if err := db.QueryRowContext(ctx, `SELECT eval_count, match_count FROM policy_rule_stats WHERE cluster_id = '' AND rule_id = 'no-vlan1'`).
		Scan(&evalCount, &matchCount); err != nil {
		t.Errorf("policy_rule_stats row lost across migration: %v", err)
	} else if evalCount != 42 || matchCount != 7 {
		t.Errorf("policy_rule_stats row = (%d, %d), want (42, 7)", evalCount, matchCount)
	}
}

func seedV38(t *testing.T, db *sql.DB) {
	t.Helper()
	// T-2602's paused staged (canary) apply, mid-sequence on the v1 changeset.
	mustExec(t, db, `INSERT INTO changeset_apply_stages
		(changeset_id, state, strategy_json, applied_nodes, pending_nodes, author, hold_started_at, hold_deadline, confirm_deadline)
		VALUES ('cs-v1', 'canary_hold', '{"mode":"canary"}', '["pve1"]', '["pve2"]', 'brian', 1700005000, 1700005600, 1700006000)`)
}

func assertV38(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var state, appliedNodes string
	var holdDeadline int64
	if err := db.QueryRowContext(ctx, `SELECT state, applied_nodes, hold_deadline FROM changeset_apply_stages WHERE changeset_id = 'cs-v1'`).
		Scan(&state, &appliedNodes, &holdDeadline); err != nil {
		t.Errorf("changeset_apply_stages row lost across migration: %v", err)
	} else if state != "canary_hold" || appliedNodes != `["pve1"]` || holdDeadline != 1700005600 {
		t.Errorf("changeset_apply_stages row = (%q, %q, %d), want (\"canary_hold\", \"[\\\"pve1\\\"]\", 1700005600)", state, appliedNodes, holdDeadline)
	}
}

func seedV39(t *testing.T, db *sql.DB) {
	t.Helper()
	// T-2705's provenance column: which MCP tool staged this draft. Set on
	// the v1 changeset, which every later assertion also reads.
	mustExec(t, db, `UPDATE changesets SET origin_tool = 'changesets.stage.bridge' WHERE id = 'cs-v1'`)
}

func assertV39(t *testing.T, db *sql.DB) {
	t.Helper()
	var tool sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT origin_tool FROM changesets WHERE id = 'cs-v1'`).Scan(&tool); err != nil {
		t.Errorf("changesets.origin_tool lost across migration: %v", err)
	} else if !tool.Valid || tool.String != "changesets.stage.bridge" {
		t.Errorf("changesets.origin_tool = %v, want \"changesets.stage.bridge\"", tool)
	}
}

func seedV40(t *testing.T, db *sql.DB) {
	t.Helper()
	// T-2604's two-person rule: a sign-off keyed (changeset_id, principal) —
	// the row whose PRIMARY KEY is what makes "the same person via two tokens
	// is one approver" a storage property — and the break-glass record, whose
	// invoked_at is the floor the 24h un-ackable finding counts from.
	mustExec(t, db, `INSERT INTO changeset_signoffs (changeset_id, principal, decided_at)
	                 VALUES ('cs-v1', 'alice@pam', 1750000000)`)
	mustExec(t, db, `INSERT INTO changeset_breakglass (changeset_id, reason, invoked_by, invoked_at, ops_fingerprint)
	                 VALUES ('cs-v1', 'link down, on-call alone', 'bob@pam', 1750000100, 'fp-v40')`)
}

func assertV40(t *testing.T, db *sql.DB) {
	t.Helper()
	var principal string
	var decidedAt int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT principal, decided_at FROM changeset_signoffs WHERE changeset_id = 'cs-v1'`).
		Scan(&principal, &decidedAt); err != nil {
		t.Errorf("changeset_signoffs row lost across migration: %v", err)
	} else if principal != "alice@pam" || decidedAt != 1750000000 {
		t.Errorf("changeset_signoffs = (%q, %d), want (\"alice@pam\", 1750000000)", principal, decidedAt)
	}

	var reason, invokedBy, fingerprint string
	var invokedAt int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT reason, invoked_by, invoked_at, ops_fingerprint FROM changeset_breakglass WHERE changeset_id = 'cs-v1'`).
		Scan(&reason, &invokedBy, &invokedAt, &fingerprint); err != nil {
		t.Errorf("changeset_breakglass row lost across migration: %v", err)
	} else if reason != "link down, on-call alone" || invokedBy != "bob@pam" ||
		invokedAt != 1750000100 || fingerprint != "fp-v40" {
		t.Errorf("changeset_breakglass = (%q, %q, %d, %q), want the seeded row",
			reason, invokedBy, invokedAt, fingerprint)
	}
}

func seedV41(t *testing.T, db *sql.DB) {
	t.Helper()
	// T-2804's incident view: the window and one operator annotation. There
	// is deliberately no incident_events table (see 0041's own comment) —
	// what must survive a migration is the window, because the timeline is
	// recomputed from the sources over it.
	mustExec(t, db, `INSERT INTO incidents (id, title, started_at, ended_at, opened_by, opened_at, status)
	                 VALUES ('inc-v41', 'uplink flap', 1750000000, 1750003600, 'alice@pam', 1750000050, 'closed')`)
	mustExec(t, db, `INSERT INTO incident_annotations (id, incident_id, at, author, body)
	                 VALUES ('ann-v41', 'inc-v41', 1750001000, 'alice@pam', 'swapped the SFP')`)
}

func assertV41(t *testing.T, db *sql.DB) {
	t.Helper()
	var title, openedBy, status string
	var startedAt, endedAt int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT title, started_at, ended_at, opened_by, status FROM incidents WHERE id = 'inc-v41'`).
		Scan(&title, &startedAt, &endedAt, &openedBy, &status); err != nil {
		t.Errorf("incidents row lost across migration: %v", err)
	} else if title != "uplink flap" || startedAt != 1750000000 || endedAt != 1750003600 ||
		openedBy != "alice@pam" || status != "closed" {
		t.Errorf("incidents = (%q, %d, %d, %q, %q), want the seeded row",
			title, startedAt, endedAt, openedBy, status)
	}

	var note string
	if err := db.QueryRowContext(context.Background(),
		`SELECT body FROM incident_annotations WHERE incident_id = 'inc-v41'`).Scan(&note); err != nil {
		t.Errorf("incident_annotations row lost across migration: %v", err)
	} else if note != "swapped the SFP" {
		t.Errorf("incident_annotations.body = %q, want %q", note, "swapped the SFP")
	}
}

// Schema version 41 (0041_incidents.sql, incidents + incident_annotations —
// T-2804) has no versionSeeds entry because it is the current latest, not a
// "prior" version any fixture in this file freezes at — its own forward
// application (as part of every case's migrate() call to latest) is exercised
// by every case above, and TestOpen_CreatesAllTables (store_test.go)
// exercises it from a fresh database. The next migration to land becomes the
// new latest and picks up a version 41 entry in versionSeeds at that time.
