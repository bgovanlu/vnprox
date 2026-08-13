package telemetry

// install.go owns the install-id: the ONLY correlator in the payload.
//
// Three properties, each of which is a decision rather than an accident:
//
//   - **It is random, not derived.** Not a hash of the cluster name, not a
//     machine-id, not a fingerprint of anything. A ULID from
//     internal/store.NewULID, which is a timestamp plus 80 bits of
//     crypto/rand entropy. Two installs that reset on the same day are
//     indistinguishable, and an id cannot be recomputed by anyone who
//     learns something about the machine — which a derived id always can.
//   - **It lives in the kv table, not a table of its own.** This is one
//     string. internal/store's kv table exists for exactly this ("available
//     to other packages for similar small persistent settings"), and a
//     migration adding a one-row table for it would be schema weight with
//     no schema in it. NO MIGRATION WAS TAKEN FOR T-2503.
//   - **Reset is a delete, then an insert.** Not an UPDATE: the old value's
//     row is removed outright, so no store query can return it afterwards
//     — see ResetInstallID, and install_test.go, which greps every column
//     of every table for the old id.
//
// Nothing here runs unless telemetry is enabled. `Ensure` is called by the
// telemetry commands only; a daemon or a CLI that never opts in never
// generates an id at all, so an install that has telemetry off has nothing
// to correlate even if the database is later handed to somebody.

import (
	"context"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/store"
)

// InstallIDKey is the kv key the install-id lives under.
const InstallIDKey = "telemetry.install_id"

// EnsureInstallID returns the install-id, generating and persisting one on
// first use. The bool reports whether this call created it, so the CLI can
// tell an operator that an id now exists where none did before — a thing
// that happened to their machine and should not be silent.
func EnsureInstallID(ctx context.Context, db *store.DB) (string, bool, error) {
	repo := store.NewKVRepo(db)
	existing, err := repo.Get(ctx, InstallIDKey)
	switch {
	case err == nil && existing != "":
		return existing, false, nil
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return "", false, fmt.Errorf("reading the telemetry install-id: %w", err)
	}

	id := store.NewULID()
	if setErr := repo.Set(ctx, InstallIDKey, id); setErr != nil {
		return "", false, fmt.Errorf("storing the telemetry install-id: %w", setErr)
	}
	return id, true, nil
}

// PeekInstallID returns the stored install-id without creating one, or ""
// when none exists. Used by `telemetry status`, which must be able to answer
// "do I have one" without the act of asking being what gives you one.
func PeekInstallID(ctx context.Context, db *store.DB) (string, error) {
	id, err := store.NewKVRepo(db).Get(ctx, InstallIDKey)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the telemetry install-id: %w", err)
	}
	return id, nil
}

// ResetInstallID replaces the install-id with a new one and returns it.
//
// The old id is DELETED and a fresh row inserted, rather than updated in
// place, and nothing anywhere records what it used to be: not an audit row,
// not a log line, not a "previous" column. An operator who resets their
// correlator because they no longer want their history joined up has to be
// able to rely on that being what happened — a reset that left the old value
// somewhere readable would be worse than no reset, because they would think
// they had one.
//
// The honest residual: SQLite may keep the freed page containing the old
// string in the database file until that page is reused or the store is
// compacted (internal/store's incremental vacuum). No query returns it, and
// this function does not attempt to overwrite the raw file — a promise about
// on-disk bytes is one this code cannot keep on every filesystem. It is
// documented in docs/security.md rather than implied away.
func ResetInstallID(ctx context.Context, db *store.DB) (string, error) {
	repo := store.NewKVRepo(db)
	if err := repo.Delete(ctx, InstallIDKey); err != nil {
		return "", fmt.Errorf("clearing the telemetry install-id: %w", err)
	}
	id := store.NewULID()
	if err := repo.Insert(ctx, InstallIDKey, id); err != nil {
		return "", fmt.Errorf("storing the new telemetry install-id: %w", err)
	}
	return id, nil
}
