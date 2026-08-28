// SPDX-License-Identifier: Apache-2.0

package store

// export_test.go exposes a small, deliberate slice of this package's
// test-only fixture machinery to the *external* test package (`store_test`,
// same directory). Symbols declared here are visible only to that package
// and never to production code or to any other package.
//
// It exists for exactly one reason: T-1901 AC3 ("restore across a schema
// upgrade works — backup at version N, restore into a binary at N+k") is
// most honestly tested against T-1807's existing fixture corpus, not
// against a second corpus invented for backup. That corpus lives in
// migrate_fromeach_test.go's unexported `versionSeeds`/`openFrozenAt`/
// `freezeAndSeed`/`assertSeededThrough`, and the test that needs it must
// import internal/backup — which imports internal/store, so the test cannot
// live in `package store` without an import cycle. An external test package
// breaks the cycle; this file is the bridge across it.
//
// The wrappers add nothing and decide nothing. They are pure re-exports, so
// the corpus stays single-sourced in T-1807's file and a new migration that
// lands without a seed/assert pair still fails there, loudly, exactly as
// before.

import (
	"database/sql"
	"testing"
)

// OpenFrozenStoreAt builds, at the given path, a database frozen at schema
// version v — migrations 1..v applied and nothing beyond — and returns the
// open handle. The caller closes it.
func OpenFrozenStoreAt(t *testing.T, path string, v int) *sql.DB {
	t.Helper()
	return openFrozenAtPath(t, path, v)
}

// SeedFrozenStore populates a database frozen at version v with T-1807's
// cumulative fixture corpus for versions 1..v.
func SeedFrozenStore(t *testing.T, db *sql.DB, v int) {
	t.Helper()
	freezeAndSeed(t, db, v)
}

// AssertSeededStore re-runs T-1807's per-version assertions for versions
// 1..v against db. Every one of them checks specific column values, not row
// counts, so a restore that silently dropped or mangled data fails here.
func AssertSeededStore(t *testing.T, db *sql.DB, v int) {
	t.Helper()
	assertSeededThrough(t, db, v)
}

// CheckSeededV1 is T-1807's version-1 data-preservation check as a PURE
// function: it returns a problem string per lost or altered row and takes
// no *testing.T, so a caller can assert on its result as an ordinary value.
//
// T-1807 split this out for exactly this reason (its own AC3 probe hit
// Go's unconditional subtest-failure propagation, see
// planning/reports/T-1807.md §2); T-1901's non-vacuity guard needs the same
// property for the same reason.
func CheckSeededV1(db *sql.DB) []string { return checkV1(db) }

// SeededVersionsAvailable reports the highest schema version T-1807's
// corpus has a fixture for, so a backup/restore test can pick a source
// version without hardcoding a number that goes stale on the next
// migration.
func SeededVersionsAvailable() int {
	highest := 0
	for v := range versionSeeds {
		if v > highest {
			highest = v
		}
	}
	return highest
}
