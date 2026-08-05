// bundlecmd.go implements `vnproxctl support-bundle` (T-1902).
//
// It joins the *daemon-independent* command family this binary's package
// doc describes (`status`, `snapshots`, `rollback-now`, `backup`,
// `restore`): it reads vnprox.toml directly, reads SQLite read-only, and
// probes the filesystem and network itself. That is the whole point — a
// support bundle is most needed when the daemon will not start, so a
// bundle that required a healthy daemon would be unavailable exactly when
// it matters. It keeps the family's 0/1/2 exit-code convention (see
// exitcodes.go).
//
// Unlike `backup`, this command has no --include-keys and no equivalent.
// There is no flag, environment variable or config value that makes a
// support bundle carry key material: internal/backup's bundle collectors
// implement an interface with no Emits method, so a bundle's declared
// secret-class set is empty by construction rather than by default.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/config"
)

// defaultBundleDir is where a bundle is written when no --out/--out-dir is
// given. A sibling of the backup directory under vnprox's own state
// directory, which the systemd unit already grants write access to and
// which `apt purge` already cleans up.
const defaultBundleDir = "/var/lib/vnprox/support"

func runSupportBundle(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl support-bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml")
	outDir := fs.String("out-dir", defaultBundleDir, "directory to write the bundle into")
	out := fs.String("out", "", "exact archive path to write (overrides --out-dir)")
	dryRun := fs.Bool("dry-run", false, "collect exactly as a real run does, print what would be in the bundle, and write nothing")
	noProbe := fs.Bool("no-probe", false, "do not make any outbound connection: skip peer reachability, the daemon health read, and the listen-port test")
	changesets := fs.Int("changesets", backup.DefaultBundleChangesets, "how many recent changesets to include")
	logLines := fs.Int("log-lines", backup.DefaultBundleLogLines, "how many journal lines to include")
	logFile := fs.String("log-file", "", "read the daemon log from this file instead of journalctl")
	logUnit := fs.String("log-unit", backup.DefaultLogUnit, "systemd unit to read the daemon log from")
	interfaces := fs.String("interfaces", backup.DefaultInterfacesPath, "path to interfaces(5) (parsed and allowlisted, never included verbatim)")
	corosync := fs.String("corosync", backup.DefaultCorosyncPath, "path to corosync.conf (peer discovery)")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl support-bundle: %v\n", ofErr)
		return ExitUsage
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "vnproxctl support-bundle: unexpected argument %q (use --out to name the archive)\n", fs.Arg(0))
		return ExitUsage
	}

	cfg, err := config.LoadRecoveryOnly(*configPath, discardLogger())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl support-bundle: loading %s: %v\n", *configPath, err)
		return ExitError
	}

	res, err := backup.Bundle(context.Background(), backup.BundleOptions{
		ConfigPath:     *configPath,
		DBPath:         cfg.DBPath,
		Listen:         cfg.Listen,
		KeyPaths:       keyPathRefsFor(cfg),
		OutDir:         *outDir,
		Dest:           *out,
		ToolVersion:    version,
		InterfacesPath: *interfaces,
		CorosyncPath:   *corosync,
		LogSource:      backup.LogSource{Path: *logFile, Unit: *logUnit},
		ChangesetLimit: *changesets,
		LogTailLines:   *logLines,
		Probe:          !*noProbe,
		DryRun:         *dryRun,
		Logger:         discardLogger(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl support-bundle: %v\n", err)
		return ExitError
	}

	if jsonOut {
		//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
		type bundleJSON struct {
			Path  string            `json:"path"`
			Bytes int64             `json:"bytes"`
			Plan  backup.BundlePlan `json:"plan"`
		}
		if err := writeJSONOut(stdout, bundleJSON{Path: res.Path, Bytes: res.Bytes, Plan: res.Plan}); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl support-bundle: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}

	printBundlePlan(stdout, res)
	return ExitSuccess
}

func printBundlePlan(stdout io.Writer, res *backup.BundleResult) {
	p := res.Plan
	if p.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would write %s\n", p.ArchivePath)
	} else {
		_, _ = fmt.Fprintf(stdout, "Wrote %s (%s)\n", res.Path, humanBytes(res.Bytes))
	}
	_, _ = fmt.Fprintf(stdout, "  node:      %s\n", p.Node)
	_, _ = fmt.Fprintf(stdout, "  collected: %s\n\n", p.CollectedAt)

	_, _ = fmt.Fprintln(stdout, "Contents:")
	for _, e := range p.Entries {
		_, _ = fmt.Fprintf(stdout, "  %-28s %s\n", e.Name, e.About)
		_, _ = fmt.Fprintf(stdout, "  %-28s   redaction: %s\n", "", e.Redaction)
	}

	_, _ = fmt.Fprintln(stdout, "\nDeliberately NOT collected:")
	for _, o := range p.Omitted {
		_, _ = fmt.Fprintf(stdout, "  * %s\n", o)
	}

	if p.DryRun {
		_, _ = fmt.Fprintln(stdout, "\nNothing was written (--dry-run).")
		return
	}
	_, _ = fmt.Fprintln(stdout, "\nThis bundle contains no credential, but it does describe your network.")
	_, _ = fmt.Fprintln(stdout, "Read readme.txt inside it before you attach it to anything public.")
}

// keyPathRefsFor ties this node's configured key files to the SecretClass
// each one belongs to, so the bundle's key-file probe can report
// "the session encryption key is missing" rather than a bare path.
//
// The class IDs are internal/backup's declared inventory
// (internal/backup/secrets.go). A key-file class added there without a line
// here still appears in the bundle — with an empty path and exists=false —
// which is visible rather than silent.
func keyPathRefsFor(cfg config.RecoveryConfig) []backup.KeyPathRef {
	refs := []backup.KeyPathRef{
		{ClassID: "session_key", Path: cfg.SessionKeyFile},
		{ClassID: "pve_api_token", Path: cfg.PVETokenFile},
		{ClassID: "metrics_scrape_token", Path: cfg.MetricsKeyFile},
		{ClassID: "blueprint_signing_key", Path: cfg.BlueprintSigningKeyFile},
	}
	if cfg.OIDCClientSecretFile != "" {
		refs = append(refs, backup.KeyPathRef{ClassID: "oidc_client_secret", Path: cfg.OIDCClientSecretFile})
	}
	return refs
}
