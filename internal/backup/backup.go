// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// Archive entry names. Fixed, lowercase, and inside a single directory per
// role so a future addition cannot collide with an existing name.
const (
	entryStore  = "store/vnprox.db"
	entryConfig = "config/vnprox.toml"
	entryReadme = "readme.txt"
	keyPrefix   = "keys/"
)

// Options configures Create.
//
//nolint:govet // fieldalignment: an options struct read top-to-bottom by humans; grouping by meaning beats packing a handful of bytes.
type Options struct {
	// ConfigPath is the vnprox.toml this backup captures and reads its
	// other paths from.
	ConfigPath string
	// DBPath is the store to snapshot.
	DBPath string
	// KeyPaths are the on-disk secret files to include when IncludeKeys is
	// set. Ignored entirely otherwise. Paths that do not exist are skipped
	// (an install with no OIDC has no OIDC client secret).
	KeyPaths []string
	// OutDir is the directory the archive is written to. Ignored if Dest is
	// set.
	OutDir string
	// Dest, if set, is the exact archive path to write.
	Dest string
	// IncludeKeys turns this into a total-compromise archive. See
	// KeyWarning.
	IncludeKeys bool
	// Keep, if > 0, prunes older vnprox backup archives in OutDir after a
	// successful write, retaining this many (including the one just
	// written). This is the whole of vnprox's backup retention story on
	// purpose: a cron job or systemd timer is the scheduler, and this flag
	// is the ceiling — there is no scheduler inside the daemon.
	Keep int
	// Node is the hostname recorded in the manifest. Defaults to
	// os.Hostname.
	Node string
	// ToolVersion is recorded in the manifest.
	ToolVersion string
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
	// Logger is optional.
	Logger *slog.Logger
}

// Result describes a completed backup.
//
//nolint:govet // fieldalignment: an options struct read top-to-bottom by humans; grouping by meaning beats packing a handful of bytes.
type Result struct {
	Path     string
	Manifest Manifest
	Bytes    int64
	// Pruned lists archives removed by Keep, if any.
	Pruned []string
	// Warnings are operator-facing, non-fatal notes (a key file the config
	// declares but that does not exist, say).
	Warnings []string
}

// ---------------------------------------------------------------- collectors

// storeCollector takes the consistent SQLite snapshot.
//
// Emits nothing: the store is full of sealed ciphertext, and ciphertext is
// not a secret in the clear. That distinction is the entire basis of the
// "a backup without --include-keys is safe to store anywhere" claim, so it
// is stated here rather than assumed.
type storeCollector struct{ dbPath string }

func (c storeCollector) Name() string         { return "store" }
func (c storeCollector) Emits() []SecretClass { return nil }
func (c storeCollector) Collect(ctx context.Context, st *Staging) error {
	dst, err := st.Reserve(entryStore)
	if err != nil {
		return err
	}
	if err := store.SnapshotTo(ctx, c.dbPath, dst); err != nil {
		return fmt.Errorf("backup: snapshotting the store: %w", err)
	}
	return st.Record(entryStore, RoleStore, c.dbPath)
}

// configCollector captures vnprox.toml verbatim.
//
// Verbatim, not redacted: a backup exists to reconstruct this node, and a
// config with its paths stripped reconstructs nothing. vnprox's own
// convention keeps every secret in a separate file referenced by path (see
// docs/security.md), so the config itself carries paths and policy, not
// credentials — which is why this collector can honestly declare it emits
// no secret class. T-1902's bundle will need the redacting variant of this
// collector; that is a different policy over the same seam, not a change
// here.
type configCollector struct{ path string }

func (c configCollector) Name() string         { return "config" }
func (c configCollector) Emits() []SecretClass { return nil }
func (c configCollector) Collect(_ context.Context, st *Staging) error {
	return st.CopyFile(entryConfig, RoleConfig, c.path)
}

// keyCollector captures on-disk key material. It runs only under
// --include-keys, and it declares exactly what that means.
//
//nolint:govet // fieldalignment: a test fixture struct; readability beats packing.
type keyCollector struct {
	paths    []string
	warnings *[]string
}

func (c *keyCollector) Name() string { return "keys" }

func (c *keyCollector) Emits() []SecretClass { return SecretClassesBy(StorageKeyFile) }

func (c *keyCollector) Collect(_ context.Context, st *Staging) error {
	for _, p := range c.paths {
		info, err := os.Lstat(p)
		if err != nil {
			if os.IsNotExist(err) {
				*c.warnings = append(*c.warnings, fmt.Sprintf("key file %s does not exist on this node — not included", p))
				continue
			}
			return fmt.Errorf("backup: reading key file %s: %w", p, err)
		}
		if !info.Mode().IsRegular() {
			*c.warnings = append(*c.warnings, fmt.Sprintf("%s is not a regular file — not included", p))
			continue
		}
		name := keyPrefix + sanitizeKeyName(filepath.Base(p))
		if err := st.CopyFile(name, RoleKey, p); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeKeyName maps a key file's basename into the archive's entry-name
// vocabulary. Key files are named by vnprox itself (session.key,
// metrics.key, pve-token, ...), but the config lets an operator point at an
// arbitrary path, so the basename is untrusted and is normalised rather
// than trusted to already be safe.
var unsafeNameChars = regexp.MustCompile(`[^a-z0-9._-]+`)

func sanitizeKeyName(base string) string {
	s := unsafeNameChars.ReplaceAllString(strings.ToLower(base), "-")
	s = strings.TrimLeft(s, ".-_")
	if s == "" {
		s = "key"
	}
	return s
}

// ---------------------------------------------------------------- warning

// KeyWarning is the text `vnproxctl backup --include-keys` prints before it
// writes anything. It names every class the archive will contain, because
// "contains key material" is not actionable and "contains the key that
// decrypts every PVE credential, every WireGuard private key and every
// sealed revert ticket in the store" is.
func KeyWarning() string {
	var b strings.Builder
	b.WriteString("WARNING: --include-keys produces an archive that is a COMPLETE COMPROMISE of this\n")
	b.WriteString("installation if it is ever read by someone else. It will contain, in the clear:\n\n")
	for _, c := range SecretClassesBy(StorageKeyFile) {
		fmt.Fprintf(&b, "  * %s\n      %s\n", c.Name, c.Detail)
	}
	b.WriteString("\nBecause the session encryption key is one of them, this archive also makes every\n")
	b.WriteString("sealed column in the store readable:\n\n")
	for _, c := range SecretClassesBy(StorageSealedColumn) {
		fmt.Fprintf(&b, "  * %s (%s)\n", c.Name, c.Column)
	}
	b.WriteString("\nTreat the resulting file exactly as you would /etc/vnprox/keys itself: it is written\n")
	b.WriteString("0600, and it must never be copied to shared storage, a ticket, or a forum thread.\n")
	b.WriteString("A backup taken WITHOUT --include-keys contains none of the above — the store's\n")
	b.WriteString("sealed columns stay ciphertext that this node's session key, which is not in the\n")
	b.WriteString("archive, is the only way to open.\n")
	return b.String()
}

// ---------------------------------------------------------------- create

// Create takes a backup and returns where it landed.
func Create(ctx context.Context, opts Options) (*Result, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	node := opts.Node
	if node == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("backup: determining node name: %w", err)
		}
		node = h
	}

	if opts.DBPath == "" {
		return nil, fmt.Errorf("backup: no store path configured")
	}
	if _, err := os.Stat(opts.DBPath); err != nil {
		return nil, fmt.Errorf("backup: store %s is not readable: %w", opts.DBPath, err)
	}

	schemaVersion, err := store.InspectSchemaVersion(ctx, opts.DBPath)
	if err != nil {
		return nil, err
	}

	dest := opts.Dest
	outDir := opts.OutDir
	if dest == "" {
		if outDir == "" {
			return nil, fmt.Errorf("backup: neither an output directory nor a destination path was given")
		}
		dest = filepath.Join(outDir, ArchiveName(node, now(), opts.IncludeKeys))
	} else {
		outDir = filepath.Dir(dest)
	}
	if mkdirErr := os.MkdirAll(outDir, 0o700); mkdirErr != nil {
		return nil, fmt.Errorf("backup: creating output directory %s: %w", outDir, mkdirErr)
	}

	// Stage inside the output directory so the archive's final rename is a
	// same-filesystem operation and the plaintext staging copy never lands
	// somewhere with different permissions from the archive itself.
	st, err := NewStaging(outDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rmErr := st.Remove(); rmErr != nil {
			logger.Warn("backup: could not remove staging directory", "error", rmErr)
		}
	}()

	res := &Result{}
	collectors := []Collector{
		storeCollector{dbPath: opts.DBPath},
		configCollector{path: opts.ConfigPath},
	}
	if opts.IncludeKeys {
		collectors = append(collectors, &keyCollector{paths: opts.KeyPaths, warnings: &res.Warnings})
	}

	for _, c := range collectors {
		if collectErr := c.Collect(ctx, st); collectErr != nil {
			return nil, fmt.Errorf("backup: collector %s: %w", c.Name(), collectErr)
		}
	}

	if readmeErr := st.WriteFile(entryReadme, RoleMeta, 0o600, []byte(readmeText(node, schemaVersion, opts.IncludeKeys, now()))); readmeErr != nil {
		return nil, readmeErr
	}

	declared := declaredSecretClasses(collectors)
	m := Manifest{
		Format:              FormatVersion,
		Kind:                KindBackup,
		CreatedAt:           now().UTC().Format(time.RFC3339),
		Tool:                "vnproxctl",
		ToolVersion:         opts.ToolVersion,
		Node:                node,
		SchemaVersion:       schemaVersion,
		IncludesKeyMaterial: opts.IncludeKeys,
		SecretClasses:       secretClassIDs(declared),
		Entries:             st.Entries(),
	}
	if m.SecretClasses == nil {
		m.SecretClasses = []string{}
	}

	size, err := Write(dest, m, st.Dir())
	if err != nil {
		return nil, err
	}
	res.Path = dest
	res.Manifest = m
	res.Bytes = size

	if opts.Keep > 0 && outDir != "" {
		pruned, err := Prune(outDir, opts.Keep, dest)
		if err != nil {
			// Retention failing must not make a successful backup look
			// failed: the archive is on disk and valid.
			res.Warnings = append(res.Warnings, fmt.Sprintf("retention: %v", err))
		}
		res.Pruned = pruned
	}
	return res, nil
}

// archiveNamePattern matches archives this tool produced, and only those —
// Prune deletes files, so it must never match anything an operator dropped
// in the same directory.
var archiveNamePattern = regexp.MustCompile(`^vnprox-backup-.+-\d{8}-\d{6}(-with-keys)?\.tar\.gz$`)

// ArchiveName is the conventional filename for a backup of node at t.
//
// The `-with-keys` suffix is deliberate and is a safety feature, not
// cosmetics: it is the one part of the archive's marking that is visible in
// an `ls`, an rsync log, or a filename pasted into a chat window, without
// anyone having to open the file.
func ArchiveName(node string, t time.Time, withKeys bool) string {
	suffix := ""
	if withKeys {
		suffix = "-with-keys"
	}
	return fmt.Sprintf("vnprox-backup-%s-%s%s.tar.gz",
		sanitizeKeyName(node), t.UTC().Format("20060102-150405"), suffix)
}

// Prune removes all but the newest keep archives in dir, never touching
// files that do not match ArchiveName's pattern and never touching protect
// (the archive just written).
//
// Newest is decided by the timestamp embedded in the filename, which sorts
// lexicographically because the format is zero-padded and UTC. Deciding by
// mtime instead would be wrong the moment an archive is copied.
func Prune(dir string, keep int, protect string) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("backup: listing %s for retention: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !archiveNamePattern.MatchString(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) <= keep {
		return nil, nil
	}

	protectBase := filepath.Base(protect)
	var removed []string
	var firstErr error
	for _, name := range names[keep:] {
		if name == protectBase {
			continue
		}
		p := filepath.Join(dir, name)
		if err := os.Remove(p); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("backup: pruning %s: %w", p, err)
			}
			continue
		}
		removed = append(removed, p)
	}
	return removed, firstErr
}

func readmeText(node string, schemaVersion int, withKeys bool, at time.Time) string {
	var b strings.Builder
	b.WriteString("vnprox backup archive\n")
	b.WriteString("=====================\n\n")
	fmt.Fprintf(&b, "node:            %s\n", node)
	fmt.Fprintf(&b, "taken:           %s\n", at.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "store schema:    %d\n", schemaVersion)
	fmt.Fprintf(&b, "key material:    %v\n\n", withKeys)
	b.WriteString("Contents:\n")
	b.WriteString("  store/vnprox.db     vnprox's app-owned SQLite store, captured with VACUUM INTO\n")
	b.WriteString("                      (a consistent point-in-time copy, not a file copy):\n")
	b.WriteString("                      changesets, pre/post rollback snapshots, audit history,\n")
	b.WriteString("                      layout, tenants and blueprint state.\n")
	b.WriteString("  config/vnprox.toml  this node's configuration, verbatim.\n")
	if withKeys {
		b.WriteString("  keys/...            KEY MATERIAL IN THE CLEAR. This archive decrypts every\n")
		b.WriteString("                      sealed credential in the store above. Treat it as you\n")
		b.WriteString("                      would /etc/vnprox/keys itself.\n")
	}
	b.WriteString("\nThis archive does NOT contain Proxmox's own configuration. vnprox never owns\n")
	b.WriteString("network config — /etc/network/interfaces and /etc/pve are PVE's, and are covered\n")
	b.WriteString("by ordinary Proxmox backup practice. Restoring this archive restores vnprox's\n")
	b.WriteString("history and app state; it changes nothing about the cluster's network.\n")
	b.WriteString("\nRestore with: vnproxctl restore <this file>\n")
	return b.String()
}
