// SPDX-License-Identifier: Apache-2.0

// Package store_test is the external test package for internal/store. It
// exists so this file can import internal/backup (which imports
// internal/store, so `package store` could not).
//
// This is T-1901 AC3's forward half: "Restore across a schema upgrade works
// (backup at version N, restore into a binary at N+k, forward migration
// runs)." The refusal half — restoring into an OLDER binary — lives in
// internal/backup/restore_test.go's TestRestore_RefusesADowngrade, which is
// where the manifest and staged-store checks it exercises live.
//
// The fixtures are T-1807's corpus, reached through internal/store's
// export_test.go, NOT a second corpus invented here. That matters for two
// reasons. First, the seeds are the real cumulative on-disk shape a node
// upgrading from release V would have, built by replaying the actual
// migration SQL rather than from a committed blob. Second, the assertions
// are T-1807's own per-version `assertVN` functions, which check specific
// column values per table — so "the restore worked" means every seeded row
// from every version 1..N survived being snapshotted, archived, extracted,
// forward-migrated to the current schema, and swapped into place, not
// merely that migrate() returned nil.
package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/store"
)

func fixedNow() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }

// TestBackupRestore_AC3_AcrossASchemaUpgrade backs up a store frozen at
// each shipped schema version and restores it into this build, asserting
// forward migration ran and every seeded row survived.
//
// Every version in T-1807's corpus is covered, not a sample: the loop is
// driven by store.SeededVersionsAvailable(), so the next migration to land
// widens this test automatically the moment T-1807's own registry gains its
// seed/assert pair (and fails T-1807's exhaustiveness check until it does).
func TestBackupRestore_AC3_AcrossASchemaUpgrade(t *testing.T) {
	ctx := context.Background()
	latest, err := store.LatestSchemaVersion()
	if err != nil {
		t.Fatalf("LatestSchemaVersion: %v", err)
	}
	highestSeeded := store.SeededVersionsAvailable()
	if highestSeeded < 32 {
		t.Fatalf("T-1807's fixture corpus reports only %d versions — this test would cover almost nothing", highestSeeded)
	}
	if highestSeeded >= latest {
		t.Fatalf("the corpus's highest seeded version (%d) is not below latest (%d); T-1807's registry is out of step", highestSeeded, latest)
	}

	for v := 1; v <= highestSeeded; v++ {
		t.Run(fmt.Sprintf("from schema %d", v), func(t *testing.T) {
			t.Parallel()
			runUpgradeRestore(t, ctx, v, latest)
		})
	}
}

//nolint:revive // context-as-second-arg: this is a test helper called from a subtest, not an API.
func runUpgradeRestore(t *testing.T, ctx context.Context, from, latest int) {
	t.Helper()

	// --- an old node's store, at schema `from`, with real data on it -----
	oldNode := t.TempDir()
	oldStore := filepath.Join(oldNode, "vnprox.db")
	db := store.OpenFrozenStoreAt(t, oldStore, from)
	store.SeedFrozenStore(t, db, from)
	if err := db.Close(); err != nil {
		t.Fatalf("closing the frozen store: %v", err)
	}

	if got, err := store.InspectSchemaVersion(ctx, oldStore); err != nil || got != from {
		t.Fatalf("precondition: frozen store reports schema %d (%v), want %d", got, err, from)
	}

	cfgPath := filepath.Join(oldNode, "vnprox.toml")
	if err := os.WriteFile(cfgPath, []byte("[storage]\ndb_path = \""+oldStore+"\"\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// --- back it up, exactly as `vnproxctl backup` would ----------------
	res, err := backup.Create(ctx, backup.Options{
		ConfigPath: cfgPath, DBPath: oldStore,
		OutDir: filepath.Join(oldNode, "backups"),
		Node:   "old-node", ToolVersion: "test", Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("backup of a schema-%d store: %v", from, err)
	}
	if res.Manifest.SchemaVersion != from {
		t.Fatalf("manifest records schema %d, want %d — a restore would make the wrong migration decision",
			res.Manifest.SchemaVersion, from)
	}

	// --- restore it into THIS build, whose schema is `latest` -----------
	// A different directory entirely: this is a rebuilt node, not the one
	// the backup came from.
	newNode := t.TempDir()
	newStore := filepath.Join(newNode, "vnprox.db")
	plan, err := backup.Restore(ctx, backup.RestoreOptions{
		ArchivePath: res.Path, DBPath: newStore,
		ConfigPath: filepath.Join(newNode, "vnprox.toml"),
		KeyDir:     filepath.Join(newNode, "keys"),
		// 127.0.0.1:0 is never bound, so the liveness probe is a no-op and
		// this test claims no port.
		Listen: "127.0.0.1:0",
		Now:    fixedNow,
	})
	if err != nil {
		t.Fatalf("restoring a schema-%d archive into a schema-%d build: %v", from, latest, err)
	}
	if plan.SchemaFrom != from || plan.SchemaTo != latest {
		t.Errorf("plan reports %d -> %d, want %d -> %d", plan.SchemaFrom, plan.SchemaTo, from, latest)
	}

	// --- forward migration actually ran ---------------------------------
	got, err := store.InspectSchemaVersion(ctx, newStore)
	if err != nil {
		t.Fatalf("inspecting the restored store: %v", err)
	}
	if got != latest {
		t.Fatalf("restored store is at schema %d, want %d — forward migration did not run", got, latest)
	}

	// --- and every seeded row from every version 1..from survived -------
	// T-1807's own assertions, per table, on real column values.
	restored, err := sql.Open("sqlite", "file:"+newStore+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("opening the restored store: %v", err)
	}
	defer func() { _ = restored.Close() }()
	store.AssertSeededStore(t, restored, from)

	// The restored store opens cleanly through the ordinary path too — no
	// pending migration, no lingering sidecar from the swap.
	opened, err := store.Open(ctx, newStore)
	if err != nil {
		t.Fatalf("the restored store does not open through store.Open: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
}

// TestBackupRestore_AC3_TheUpgradeTestIsNotVacuous is the guard the loop
// above needs. Without it, "every seeded row survived the round trip" could
// be true because there was nothing to survive, or because the checks
// cannot tell a populated store from an empty one.
//
// It runs the SAME backup→restore path twice at the same schema version —
// once from a seeded store, once from an identically-frozen but UNSEEDED
// one — and asserts T-1807's own version-1 data-preservation check reports
// no problems for the first and problems for the second. Both directions
// are asserted, on the restore path specifically, so a restore that
// produced an empty-but-valid store would be caught.
//
// The check is used through store.CheckSeededV1, which returns problems as
// a []string rather than reporting them to a *testing.T — Go propagates a
// subtest's failure to its parent unconditionally, so an "assert this
// assertion fails" probe cannot be written with t.Run.
func TestBackupRestore_AC3_TheUpgradeTestIsNotVacuous(t *testing.T) {
	ctx := context.Background()

	roundTrip := func(t *testing.T, seed bool) []string {
		t.Helper()
		dir := t.TempDir()
		src := filepath.Join(dir, "vnprox.db")
		db := store.OpenFrozenStoreAt(t, src, 1)
		if seed {
			store.SeedFrozenStore(t, db, 1)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("closing the frozen store: %v", err)
		}
		cfgPath := filepath.Join(dir, "vnprox.toml")
		if err := os.WriteFile(cfgPath, []byte("[storage]\ndb_path = \""+src+"\"\n"), 0o600); err != nil {
			t.Fatalf("writing config: %v", err)
		}
		res, err := backup.Create(ctx, backup.Options{
			ConfigPath: cfgPath, DBPath: src, OutDir: filepath.Join(dir, "backups"),
			Node: "probe", ToolVersion: "test", Now: fixedNow,
		})
		if err != nil {
			t.Fatalf("backup: %v", err)
		}
		newDir := t.TempDir()
		newStore := filepath.Join(newDir, "vnprox.db")
		if _, restoreErr := backup.Restore(ctx, backup.RestoreOptions{
			ArchivePath: res.Path, DBPath: newStore,
			ConfigPath: filepath.Join(newDir, "vnprox.toml"),
			KeyDir:     filepath.Join(newDir, "keys"),
			Listen:     "127.0.0.1:0", Now: fixedNow,
		}); restoreErr != nil {
			t.Fatalf("restore: %v", restoreErr)
		}
		restored, err := sql.Open("sqlite", "file:"+newStore)
		if err != nil {
			t.Fatalf("opening the restored store: %v", err)
		}
		defer func() { _ = restored.Close() }()
		return store.CheckSeededV1(restored)
	}

	if problems := roundTrip(t, true); len(problems) != 0 {
		t.Errorf("a seeded schema-1 store lost data across backup+restore: %v", problems)
	}
	problems := roundTrip(t, false)
	if len(problems) == 0 {
		t.Fatal("T-1807's data-preservation check found NO problems in a store that was never seeded — " +
			"it cannot distinguish a restored store from an empty one, so the AC3 loop's " +
			"\"every seeded row survived\" claim proves nothing")
	}
	t.Logf("non-vacuity confirmed: the check reports %d problem(s) for an unseeded store, e.g. %q",
		len(problems), problems[0])
}
