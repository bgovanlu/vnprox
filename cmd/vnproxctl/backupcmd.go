// SPDX-License-Identifier: Apache-2.0

// backupcmd.go implements `vnproxctl backup` and `vnproxctl restore`
// (T-1901).
//
// Both belong to the *daemon-independent* command family this binary's
// package doc describes (`status`, `snapshots`, `rollback-now`): they read
// vnprox.toml directly, touch SQLite and the filesystem themselves, and
// involve no HTTP API anywhere. That is not a shortcut — a backup you can
// only take while the daemon is healthy is not a disaster-recovery tool,
// and a restore is defined by the daemon NOT running. They keep that
// family's 0/1/2 exit-code convention unchanged (see exitcodes.go).
//
// `backup` is safe to run against a live daemon: internal/store.SnapshotTo
// uses SQLite's VACUUM INTO, which takes a consistent point-in-time copy
// from a second connection. `restore` is the opposite and refuses outright
// — see internal/backup/liveness.go.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/config"
)

// defaultBackupDir is where `backup` writes when no --out/--out-dir is
// given: a subdirectory of vnprox's own app-owned state directory, which
// the systemd unit already grants write access to and which `apt purge`
// already cleans up.
const defaultBackupDir = "/var/lib/vnprox/backups"

func runBackup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml (for storage.db_path and the key file paths)")
	outDir := fs.String("out-dir", defaultBackupDir, "directory to write the archive into")
	out := fs.String("out", "", "exact archive path to write (overrides --out-dir)")
	includeKeys := fs.Bool("include-keys", false, "ALSO archive key material (session key, PVE token, ...) — see the warning this prints")
	yes := fs.Bool("yes", false, "with --include-keys: do not require an interactive confirmation")
	keep := fs.Int("keep", 0, "after writing, keep only this many vnprox backup archives in the output directory (0 = keep all)")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl backup: %v\n", ofErr)
		return ExitUsage
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "vnproxctl backup: unexpected argument %q (use --out to name the archive)\n", fs.Arg(0))
		return ExitUsage
	}

	cfg, err := config.LoadRecoveryOnly(*configPath, discardLogger())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl backup: loading %s: %v\n", *configPath, err)
		return ExitError
	}

	// The warning goes to stderr BEFORE anything is written, and names
	// every class the archive will contain — that is the "loudly" half of
	// "key material is opt-in, loudly". It is printed even with --yes: the
	// point is that it appears in the operator's terminal and in the log of
	// whatever automation ran this, not that it blocks.
	if *includeKeys {
		_, _ = fmt.Fprint(stderr, backup.KeyWarning())
		if !*yes {
			ok, promptErr := confirmIncludeKeys(stderr)
			if promptErr != nil {
				_, _ = fmt.Fprintf(stderr, "vnproxctl backup: --include-keys needs an interactive confirmation; pass --yes to run unattended (%v)\n", promptErr)
				return ExitError
			}
			if !ok {
				_, _ = fmt.Fprintln(stderr, "vnproxctl backup: aborted; nothing was written")
				return ExitError
			}
		}
	}

	res, err := backup.Create(context.Background(), backup.Options{
		ConfigPath:  *configPath,
		DBPath:      cfg.DBPath,
		KeyPaths:    keyPathsFor(cfg),
		OutDir:      *outDir,
		Dest:        *out,
		IncludeKeys: *includeKeys,
		Keep:        *keep,
		ToolVersion: version,
		Logger:      discardLogger(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl backup: %v\n", err)
		return ExitError
	}

	if jsonOut {
		//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
		type backupJSON struct {
			Path                string   `json:"path"`
			Bytes               int64    `json:"bytes"`
			Node                string   `json:"node"`
			CreatedAt           string   `json:"createdAt"`
			SchemaVersion       int      `json:"schemaVersion"`
			IncludesKeyMaterial bool     `json:"includesKeyMaterial"`
			SecretClasses       []string `json:"secretClasses"`
			Entries             int      `json:"entries"`
			Pruned              []string `json:"pruned"`
			Warnings            []string `json:"warnings"`
		}
		v := backupJSON{
			Path: res.Path, Bytes: res.Bytes, Node: res.Manifest.Node,
			CreatedAt: res.Manifest.CreatedAt, SchemaVersion: res.Manifest.SchemaVersion,
			IncludesKeyMaterial: res.Manifest.IncludesKeyMaterial,
			SecretClasses:       res.Manifest.SecretClasses,
			Entries:             len(res.Manifest.Entries),
			Pruned:              res.Pruned, Warnings: res.Warnings,
		}
		if v.Pruned == nil {
			v.Pruned = []string{}
		}
		if v.Warnings == nil {
			v.Warnings = []string{}
		}
		if err := writeJSONOut(stdout, v); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl backup: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}

	_, _ = fmt.Fprintf(stdout, "Wrote %s (%s, schema %d, %d entries).\n",
		res.Path, humanBytes(res.Bytes), res.Manifest.SchemaVersion, len(res.Manifest.Entries))
	if res.Manifest.IncludesKeyMaterial {
		_, _ = fmt.Fprintf(stdout, "This archive CONTAINS KEY MATERIAL (%s). Store it as you would /etc/vnprox/keys.\n",
			strings.Join(res.Manifest.SecretClasses, ", "))
	} else {
		_, _ = fmt.Fprintln(stdout, "No key material included: the store's sealed columns stay ciphertext this node's session key is the only way to open.")
	}
	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(stdout, "note: %s\n", w)
	}
	for _, p := range res.Pruned {
		_, _ = fmt.Fprintf(stdout, "retention: removed %s\n", p)
	}
	return ExitSuccess
}

func runRestore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml (for storage.db_path and server.listen)")
	dryRun := fs.Bool("dry-run", false, "validate the archive and print what would happen; change nothing")
	restoreConfig := fs.Bool("restore-config", false, "also install the archive's vnprox.toml over --config (the current one is moved aside)")
	restoreKeys := fs.Bool("restore-keys", false, "also install the archive's key files (requires an archive taken with --include-keys)")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl restore: %v\n", ofErr)
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl restore: expected exactly one archive path")
		return ExitUsage
	}
	archivePath := fs.Arg(0)

	cfg, err := config.LoadRecoveryOnly(*configPath, discardLogger())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl restore: loading %s: %v\n", *configPath, err)
		return ExitError
	}

	plan, err := backup.Restore(context.Background(), backup.RestoreOptions{
		ArchivePath:   archivePath,
		DBPath:        cfg.DBPath,
		ConfigPath:    *configPath,
		KeyDir:        filepath.Dir(cfg.SessionKeyFile),
		Listen:        cfg.Listen,
		RestoreConfig: *restoreConfig,
		RestoreKeys:   *restoreKeys,
		DryRun:        *dryRun,
		Logger:        discardLogger(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl restore: %v\n", err)
		return ExitError
	}

	if jsonOut {
		if err := writeJSONOut(stdout, plan); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl restore: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}

	verb := "Would restore"
	if plan.Applied {
		verb = "Restored"
	}
	_, _ = fmt.Fprintf(stdout, "%s %s\n", verb, plan.StorePath)
	_, _ = fmt.Fprintf(stdout, "  from:            %s\n", plan.ArchivePath)
	_, _ = fmt.Fprintf(stdout, "  taken:           %s on node %s\n", plan.Manifest.CreatedAt, plan.Manifest.Node)
	_, _ = fmt.Fprintf(stdout, "  schema:          %d -> %d (forward migration)\n", plan.SchemaFrom, plan.SchemaTo)
	_, _ = fmt.Fprintf(stdout, "  key material:    %v\n", plan.Manifest.IncludesKeyMaterial)
	_, _ = fmt.Fprintf(stdout, "  previous store:  %s\n", plan.PreRestorePath)
	if plan.InstallConfig {
		_, _ = fmt.Fprintf(stdout, "  config:          installed at %s\n", plan.ConfigPath)
	}
	for _, p := range plan.KeyPaths {
		if plan.InstallKeys {
			_, _ = fmt.Fprintf(stdout, "  key:             installed at %s\n", p)
		}
	}
	_, _ = fmt.Fprintln(stdout, "")
	for _, n := range plan.Notes {
		_, _ = fmt.Fprintf(stdout, "  - %s\n", n)
	}
	if plan.Applied {
		_, _ = fmt.Fprintln(stdout, "\nStart the daemon again with: systemctl start vnprox")
	} else {
		_, _ = fmt.Fprintln(stdout, "\nNothing was changed (--dry-run).")
	}
	return ExitSuccess
}

// keyPathsFor is the ordered list of on-disk secret files `--include-keys`
// collects, derived from this node's own config rather than hardcoded, so
// an install that relocated any of them is still backed up correctly.
// Non-existent paths are skipped by the collector with a note.
func keyPathsFor(cfg config.RecoveryConfig) []string {
	paths := []string{
		cfg.SessionKeyFile,
		cfg.PVETokenFile,
		cfg.MetricsKeyFile,
		cfg.BlueprintSigningKeyFile,
	}
	if cfg.OIDCClientSecretFile != "" {
		paths = append(paths, cfg.OIDCClientSecretFile)
	}
	return paths
}

// confirmIncludeKeys reads a yes/no from the controlling terminal. Reading
// /dev/tty rather than stdin deliberately: `vnproxctl backup --include-keys
// < /dev/null` in a script must not be silently answered "yes" by an empty
// stdin, and a piped stdin is not a human.
func confirmIncludeKeys(stderr io.Writer) (bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = tty.Close() }()
	_, _ = fmt.Fprint(stderr, "\nType 'include-keys' to confirm: ")
	var answer string
	if _, err := fmt.Fscanln(tty, &answer); err != nil {
		return false, err
	}
	return strings.TrimSpace(answer) == "include-keys", nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
