// SPDX-License-Identifier: Apache-2.0

// restore.go replaces a node's vnprox store with the one in an archive.
//
// The ordering below is the whole design, and every step is placed where it
// is for a reason:
//
//	1. refuse if a daemon owns the store          (liveness.go)
//	2. fully validate the archive, writing NOTHING (archive.go's Inspect)
//	3. refuse a downgrade, from the manifest       (cheap, before any I/O)
//	4. extract into a private staging directory    (never the live path)
//	5. cross-check the extracted store's own schema against the manifest
//	6. run forward migration ON THE STAGED COPY    (a failed migration
//	                                                cannot touch the live
//	                                                store, because the live
//	                                                store has not been
//	                                                opened)
//	7. move the live store aside, then rename the staged copy into place
//	8. on any failure in 7, put the live store back
//
// Steps 1–6 are all "decide"; only 7 is "act", and 7 is two renames within
// one directory. That is what makes a restore atomic in the sense that
// matters: there is no partially-migrated, partially-copied store an
// operator can be left holding. The pre-restore copy is deliberately kept
// on disk afterwards (the same choice `vnproxctl snapshots restore` already
// makes for /etc/network/interfaces) — an operator running a disaster
// recovery wants the previous state recoverable by hand too.

package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// sidecars are the WAL-mode files that belong to a SQLite database and must
// move with it. Leaving a stale -wal next to a restored store would let
// SQLite replay the *old* database's uncheckpointed transactions over the
// restored one.
var sidecars = []string{"-wal", "-shm"}

// RestoreOptions configures Restore.
//
//nolint:govet // fieldalignment: an options struct read top-to-bottom by humans; grouping by meaning beats packing a handful of bytes.
type RestoreOptions struct {
	// ArchivePath is the backup to restore.
	ArchivePath string
	// DBPath is the store to replace.
	DBPath string
	// ConfigPath is where the archived config would be installed if
	// RestoreConfig is set. Also recorded in the plan for the operator.
	ConfigPath string
	// KeyDir is where archived key files would be installed if RestoreKeys
	// is set (normally /etc/vnprox/keys).
	KeyDir string
	// Listen is [server] listen, used by the default liveness check.
	Listen string

	// RestoreConfig installs the archived vnprox.toml over ConfigPath.
	// Off by default: an archive from another node carries that node's
	// listen address and certificate paths, and silently adopting them is
	// how a restore-to-different-hardware ends up unreachable.
	RestoreConfig bool
	// RestoreKeys installs the archive's key files into KeyDir. Only
	// possible for an archive taken with --include-keys.
	RestoreKeys bool
	// DryRun reports the plan and changes nothing.
	DryRun bool

	// Limits overrides the archive reader's budgets. Zero value means
	// DefaultLimits.
	Limits Limits
	// Liveness overrides the daemon-running check. Nil means
	// DaemonLiveness(DBPath, Listen).
	Liveness LivenessCheck
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
	// Logger is optional.
	Logger *slog.Logger

	// hooks are test seams for AC4's mid-restore failure injection. They
	// are unexported so nothing outside this package's tests can reach
	// them — a production caller cannot make a restore fail on purpose.
	hooks restoreHooks
}

// restoreHooks fire at the two points where a failure has a genuinely
// different meaning: after everything is staged and validated but before
// the live store is touched, and in the window between moving the live
// store aside and renaming the new one into place.
type restoreHooks struct {
	afterStage      func() error
	afterMoveAside  func() error
	beforeMoveAside func() error
}

// Plan is what a restore would do. Returned by both a dry run and a real
// one, so `--dry-run` output and the real thing cannot drift.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type Plan struct {
	ArchivePath string   `json:"archivePath"`
	Manifest    Manifest `json:"manifest"`
	// StorePath is the store that would be (or was) replaced.
	StorePath string `json:"storePath"`
	// PreRestorePath is where the current store is moved aside to.
	PreRestorePath string `json:"preRestorePath"`
	// SchemaFrom/SchemaTo describe the forward migration a restore runs.
	SchemaFrom int `json:"schemaFrom"`
	SchemaTo   int `json:"schemaTo"`
	// InstallConfig/InstallKeys record whether the optional extras were
	// requested, and where they would land.
	InstallConfig bool     `json:"installConfig"`
	ConfigPath    string   `json:"configPath,omitempty"`
	InstallKeys   bool     `json:"installKeys"`
	KeyPaths      []string `json:"keyPaths,omitempty"`
	// Notes are operator-facing statements about what does and does not
	// carry over — the "restore to different hardware" story, generated
	// from the archive rather than from documentation that can go stale.
	Notes []string `json:"notes"`
	// Applied is false for a dry run.
	Applied bool `json:"applied"`
}

// Restore performs (or, with DryRun, plans) a restore.
func Restore(ctx context.Context, opts RestoreOptions) (*Plan, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	lim := opts.Limits
	if lim == (Limits{}) {
		lim = DefaultLimits()
	}
	if opts.DBPath == "" {
		return nil, fmt.Errorf("backup: no store path configured")
	}

	// --- 1. refuse against a live daemon --------------------------------
	// Before validating the archive, not after: an operator who ran this on
	// the wrong box should be told that first, and a dry run should be
	// honest about the fact that the real thing would refuse.
	live := opts.Liveness
	if live == nil {
		live = DaemonLiveness(opts.DBPath, opts.Listen)
	}
	if err := live(); err != nil {
		return nil, err
	}

	// --- 2. validate the archive, writing nothing -----------------------
	m, err := inspectFile(opts.ArchivePath, lim)
	if err != nil {
		return nil, err
	}

	// --- 3. refuse a downgrade, and the wrong kind of archive ------------
	if m.Kind != KindBackup {
		return nil, fmt.Errorf("%w: %s is a %q archive; only %q can be restored",
			ErrWrongKind, opts.ArchivePath, m.Kind, KindBackup)
	}
	latest, err := store.LatestSchemaVersion()
	if err != nil {
		return nil, err
	}
	if m.SchemaVersion > latest {
		return nil, fmt.Errorf("%w: the archive's store is at schema version %d and this vnprox build understands up to %d — "+
			"install the newer vnprox first, then restore (forward migration is supported; downgrading a store is not)",
			ErrSchemaDowngrade, m.SchemaVersion, latest)
	}
	if _, ok := m.Entry(RoleStore); !ok {
		return nil, fmt.Errorf("%w: archive contains no store entry", ErrMalformedArchive)
	}
	if opts.RestoreKeys && !m.IncludesKeyMaterial {
		return nil, fmt.Errorf("backup: --restore-keys was requested but %s was taken without --include-keys and contains no key material", opts.ArchivePath)
	}

	stamp := now().UTC().Format("20060102-150405")
	plan := &Plan{
		ArchivePath:    opts.ArchivePath,
		Manifest:       *m,
		StorePath:      opts.DBPath,
		PreRestorePath: opts.DBPath + ".pre-restore-" + stamp,
		SchemaFrom:     m.SchemaVersion,
		SchemaTo:       latest,
		InstallConfig:  opts.RestoreConfig,
		ConfigPath:     opts.ConfigPath,
		InstallKeys:    opts.RestoreKeys,
		Notes:          restoreNotes(m, opts),
	}
	for _, e := range m.EntriesWithRole(RoleKey) {
		plan.KeyPaths = append(plan.KeyPaths, filepath.Join(opts.KeyDir, strings.TrimPrefix(e.Name, keyPrefix)))
	}
	if opts.DryRun {
		return plan, nil
	}

	// --- 4. extract into a private staging directory --------------------
	dbDir := filepath.Dir(opts.DBPath)
	if mkdirErr := os.MkdirAll(dbDir, 0o750); mkdirErr != nil {
		return nil, fmt.Errorf("backup: creating store directory %s: %w", dbDir, mkdirErr)
	}
	// Staged in the *same directory* as the target so the final move is a
	// rename within one filesystem — an atomic operation — rather than a
	// cross-device copy that can half-finish.
	stageDir, err := os.MkdirTemp(dbDir, ".vnprox-restore-")
	if err != nil {
		return nil, fmt.Errorf("backup: creating restore staging directory in %s: %w", dbDir, err)
	}
	if chmodErr := os.Chmod(stageDir, 0o700); chmodErr != nil {
		_ = os.RemoveAll(stageDir)
		return nil, fmt.Errorf("backup: securing restore staging directory %s: %w", stageDir, chmodErr)
	}
	defer func() {
		if rmErr := os.RemoveAll(stageDir); rmErr != nil {
			logger.Warn("backup: could not remove restore staging directory", "dir", stageDir, "error", rmErr)
		}
	}()

	af, err := os.Open(opts.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("backup: opening %s: %w", opts.ArchivePath, err)
	}
	_, err = Extract(af, stageDir, lim)
	_ = af.Close()
	if err != nil {
		return nil, err
	}

	stagedStore := filepath.Join(stageDir, filepath.FromSlash(entryStore))

	// --- 5. cross-check the extracted store's own schema ------------------
	// The manifest is part of the archive, so it can be edited by whoever
	// edited the archive. Reading the version out of the store itself and
	// requiring the two to agree means a forged manifest cannot walk a
	// newer store past step 3.
	actual, err := store.InspectSchemaVersion(ctx, stagedStore)
	if err != nil {
		return nil, fmt.Errorf("backup: the archive's store is not readable: %w", err)
	}
	if actual != m.SchemaVersion {
		return nil, fmt.Errorf("%w: manifest declares schema version %d but the store inside is at %d",
			ErrSchemaMismatch, m.SchemaVersion, actual)
	}
	if actual > latest {
		return nil, fmt.Errorf("%w: the archive's store is at schema version %d and this vnprox build understands up to %d",
			ErrSchemaDowngrade, actual, latest)
	}

	// --- 6. forward-migrate the STAGED copy ------------------------------
	// store.Open runs every pending migration. Doing it here, on the copy,
	// is what makes "restore across a schema upgrade" safe: if a migration
	// fails, the failure is confined to a file in a temp directory that is
	// about to be deleted, and the live store has not been opened at all.
	migrated, err := store.Open(ctx, stagedStore)
	if err != nil {
		return nil, fmt.Errorf("backup: migrating the restored store to schema version %d: %w", latest, err)
	}
	if err := migrated.Close(); err != nil {
		return nil, fmt.Errorf("backup: closing the migrated store: %w", err)
	}
	// The migration ran in WAL mode; Close checkpoints, but be explicit —
	// a stray sidecar renamed into place alongside the store would be
	// interpreted as that store's WAL.
	for _, s := range sidecars {
		if err := os.Remove(stagedStore + s); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("backup: clearing staged %s: %w", filepath.Base(stagedStore+s), err)
		}
	}

	if h := opts.hooks.afterStage; h != nil {
		if err := h(); err != nil {
			return nil, fmt.Errorf("backup: restore aborted before touching the live store: %w", err)
		}
	}
	if h := opts.hooks.beforeMoveAside; h != nil {
		if err := h(); err != nil {
			return nil, fmt.Errorf("backup: restore aborted before touching the live store: %w", err)
		}
	}

	// --- 7/8. swap, with rollback ----------------------------------------
	if err := swapStore(opts.DBPath, stagedStore, plan.PreRestorePath, opts.hooks, logger); err != nil {
		return nil, err
	}

	// --- optional extras, each with its own move-aside --------------------
	if opts.RestoreConfig {
		staged := filepath.Join(stageDir, filepath.FromSlash(entryConfig))
		if err := installAside(staged, opts.ConfigPath, stamp, 0o644); err != nil {
			return nil, err
		}
	}
	if opts.RestoreKeys {
		for _, e := range m.EntriesWithRole(RoleKey) {
			staged := filepath.Join(stageDir, filepath.FromSlash(e.Name))
			dst := filepath.Join(opts.KeyDir, strings.TrimPrefix(e.Name, keyPrefix))
			if err := os.MkdirAll(opts.KeyDir, 0o700); err != nil {
				return nil, fmt.Errorf("backup: creating key directory %s: %w", opts.KeyDir, err)
			}
			if err := installAside(staged, dst, stamp, fs.FileMode(e.Mode).Perm()); err != nil {
				return nil, err
			}
		}
	}

	plan.Applied = true
	logger.Info("backup: store restored",
		"archive", opts.ArchivePath, "store", opts.DBPath,
		"schemaFrom", m.SchemaVersion, "schemaTo", latest, "previousStore", plan.PreRestorePath)
	return plan, nil
}

// swapStore is the only step that touches the live store. Both halves are
// renames inside one directory; if the second fails, the first is undone.
func swapStore(dbPath, stagedStore, asidePath string, hooks restoreHooks, logger *slog.Logger) error {
	// Move the live store (and its sidecars) aside. A missing store is not
	// an error: restoring onto a fresh install is the disaster-recovery
	// case this whole card exists for.
	moved, err := moveAsideStore(dbPath, asidePath)
	if err != nil {
		return err
	}

	if h := hooks.afterMoveAside; h != nil {
		if hookErr := h(); hookErr != nil {
			undoMoveAside(moved, logger)
			return fmt.Errorf("backup: restore failed after moving the previous store aside; the previous store has been put back: %w", hookErr)
		}
	}

	if err := os.Rename(stagedStore, dbPath); err != nil {
		undoMoveAside(moved, logger)
		return fmt.Errorf("backup: installing the restored store at %s (the previous store has been put back): %w", dbPath, err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return fmt.Errorf("backup: setting permissions on %s: %w", dbPath, err)
	}
	// fsync the directory so the rename itself is durable — otherwise a
	// power loss immediately after a restore can resurrect the directory
	// entry for the old file.
	if d, err := os.Open(filepath.Dir(dbPath)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

type movedFile struct{ from, to string }

func moveAsideStore(dbPath, asidePath string) ([]movedFile, error) {
	var moved []movedFile
	for _, suffix := range append([]string{""}, sidecars...) {
		src, dst := dbPath+suffix, asidePath+suffix
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			undoMoveAside(moved, nil)
			return nil, fmt.Errorf("backup: examining %s: %w", src, err)
		}
		if err := os.Rename(src, dst); err != nil {
			undoMoveAside(moved, nil)
			return nil, fmt.Errorf("backup: moving %s aside to %s: %w", src, dst, err)
		}
		moved = append(moved, movedFile{from: src, to: dst})
	}
	return moved, nil
}

// undoMoveAside puts everything moveAsideStore moved back where it was.
// Failures here are logged, not returned: the caller is already returning
// an error, and the operator needs to know both facts.
func undoMoveAside(moved []movedFile, logger *slog.Logger) {
	for i := len(moved) - 1; i >= 0; i-- {
		if err := os.Rename(moved[i].to, moved[i].from); err != nil && logger != nil {
			logger.Error("backup: could not put the previous store back — recover it by hand",
				"from", moved[i].to, "to", moved[i].from, "error", err)
		}
	}
}

// installAside copies staged over dst, moving any existing dst to
// dst.pre-restore-<stamp> first. Used for the two opt-in extras.
func installAside(staged, dst, stamp string, mode fs.FileMode) error {
	if dst == "" {
		return fmt.Errorf("backup: no destination configured for %s", filepath.Base(staged))
	}
	if _, err := os.Lstat(dst); err == nil {
		aside := dst + ".pre-restore-" + stamp
		if renameErr := os.Rename(dst, aside); renameErr != nil {
			return fmt.Errorf("backup: moving %s aside: %w", dst, renameErr)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("backup: examining %s: %w", dst, err)
	}
	data, err := os.ReadFile(staged)
	if err != nil {
		return fmt.Errorf("backup: reading staged %s: %w", filepath.Base(staged), err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return fmt.Errorf("backup: installing %s: %w", dst, err)
	}
	return nil
}

// inspectFile validates an archive on disk, writing nothing.
func inspectFile(path string, lim Limits) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("backup: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	m, err := Inspect(f, lim)
	if err != nil {
		return nil, fmt.Errorf("backup: %s: %w", path, err)
	}
	return m, nil
}

// InspectArchive validates an archive on disk and returns its manifest. The
// public entry point for `restore --dry-run` on a host with no config, and
// for anything else that wants to know what an archive is without acting on
// it.
func InspectArchive(path string, lim Limits) (*Manifest, error) {
	if lim == (Limits{}) {
		lim = DefaultLimits()
	}
	return inspectFile(path, lim)
}

// restoreNotes generates the "what carries over, what must be
// re-established" statement for this specific archive.
func restoreNotes(m *Manifest, opts RestoreOptions) []string {
	notes := []string{
		"Carries over: changesets and their diffs, every pre/post rollback snapshot, the full " +
			"audit trail, saved layouts, tenants, blueprints, and every app-owned table in the store.",
		"Not touched: Proxmox's own configuration. /etc/network/interfaces and /etc/pve are PVE's, " +
			"not vnprox's — restoring this archive changes nothing about the cluster's network.",
	}
	if m.IncludesKeyMaterial {
		if opts.RestoreKeys {
			notes = append(notes, "This archive contains key material and --restore-keys was given: the session key "+
				"will be installed, so every sealed credential in the restored store stays readable. "+
				"Existing key files are moved aside, not deleted.")
		} else {
			notes = append(notes, "This archive contains key material but --restore-keys was NOT given. The store's "+
				"sealed columns (PVE tickets, federation and switch credentials, WireGuard keys, webhook "+
				"secrets) will not decrypt under this node's own session key and must be re-entered.")
		}
	} else {
		notes = append(notes, "This archive contains no key material (the correct default). Every sealed column in "+
			"the restored store — PVE tickets, federation and switch credentials, WireGuard private keys, "+
			"webhook secrets — is ciphertext this node's session key cannot open, and must be re-entered "+
			"through the UI or API after the restore.")
	}
	notes = append(notes,
		"Must be re-established on different hardware regardless: this node's identity (hostname, "+
			"which must match the node name PVE knows it by for per-node snapshots to resolve), the "+
			"peer cluster secret at /etc/pve/priv/vnprox/ (replicated by pmxcfs when the node rejoins a "+
			"cluster, regenerated on first start otherwise), and the PVE API token if key material was "+
			"excluded.")
	if !opts.RestoreConfig {
		notes = append(notes, "The archived vnprox.toml is NOT installed unless --restore-config is given: an archive "+
			"from another node carries that node's listen address and certificate paths.")
	}
	return notes
}
