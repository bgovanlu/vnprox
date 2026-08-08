package main

// `vnproxctl doctor` (T-1904): a preflight and self-check that turns "it
// doesn't work" into a message naming the file, port, privilege, or command
// involved.
//
// Two design points worth stating here rather than in the package:
//
//  1. **It must work with the daemon down, and before install.** Every probe is
//     optional. A missing store, an absent PVE token, no cluster — each makes
//     its checks report `skip` with a reason, not `fail` and not `pass`. That
//     is what lets install.sh run this as a preflight on a machine where
//     vnprox does not exist yet.
//  2. **It reads; it never writes.** Safe to run as root, mid-incident,
//     against a live daemon.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/doctor"
	"github.com/bgovanlu/vnprox/internal/store"
)

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fset.SetOutput(stderr)
	var (
		configPath = fset.String("config", defaultConfigPath, "path to vnprox.toml")
		outputFmt  = fset.String("o", defaultOutputFormat, outputFlagUsage)
		// T-2406. Without it, behaviour is exactly as before: the
		// daemon-dependent checks report `skip` with their reason.
		live      = fset.Bool("live", false, "ask the running daemon for the checks that need its credentials (pve_reachable, pve_privileges)")
		liveURL   = fset.String("url", "", "with --live: the daemon's /api/v1 base URL (default: derived from --config)")
		liveToken = fset.String("token", "", "with --live: bearer token (falls back to "+remoteTokenEnvVar+")")
	)
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	asJSON, err := parseOutputFormat(*outputFmt)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl doctor: %v\n", err)
		return ExitUsage
	}

	ctx := context.Background()
	facts, env := collectEnvironment(ctx, *configPath)
	report := doctor.Run(ctx, facts, env)

	// T-2406: merge the daemon's verdicts over the local skips.
	//
	// A daemon that cannot be reached yields `skip` results naming the daemon,
	// NEVER `fail`. A stopped service does not mean PVE is unreachable or that
	// the token is wrong, and reporting failure here would send an operator to
	// look at the wrong thing — doctor's whole value is not doing that.
	if *live {
		report = doctor.MergeLive(report, fetchLiveResults(ctx, *configPath, *liveURL, *liveToken, stderr))
	}

	// A malformed report is a bug in a check, not an operator problem. Say so
	// loudly rather than printing something that looks authoritative.
	if err := report.Validate(); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl doctor: internal error assembling the report: %v\n", err)
		return ExitError
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl doctor: %v\n", err)
			return ExitError
		}
	} else {
		_, _ = fmt.Fprint(stdout, report.Render())
	}

	// AC4: non-zero iff at least one check fails. Warnings do not gate.
	if report.Failed() {
		return ExitError
	}
	return ExitSuccess
}

// collectEnvironment loads what it can and wires the real probes. Anything it
// cannot establish is left nil, which the checks report as `skip`.
func collectEnvironment(ctx context.Context, configPath string) (doctor.Facts, doctor.Env) {
	facts := doctor.Facts{
		ConfigPath:    configPath,
		BinaryVersion: version,
	}

	cfg, err := config.LoadRecoveryOnly(configPath, nil)
	if err != nil {
		// Deliberately not fatal: a broken config is exactly the condition
		// doctor exists to report, and the checks that do not depend on it
		// (pmxcfs, disk) still run and still say something useful.
		facts.ConfigErr = err
	} else {
		facts.ListenAddr = cfg.Listen
		facts.SessionKeyFile = cfg.SessionKeyFile
		facts.PVETokenFile = cfg.PVETokenFile
		facts.DBPath = cfg.DBPath
		// CaptureRoot and PVEAPIURL are not part of RecoveryConfig (the
		// daemon-independent subset), so doctor uses the packaged defaults for
		// them rather than widening that type. If an operator has moved either,
		// the disk check reports on the default location — worth knowing, and
		// noted in docs/deployment.md rather than silently wrong.
		facts.CaptureRoot = config.DefaultCaptureRoot
		facts.PVEAPIURL = config.DefaultPVEAPIURL
	}
	applyDoctorDefaults(&facts)

	env := doctor.Env{
		Now:           time.Now,
		Stat:          os.Stat,
		PortHolder:    portHolder,
		DiskFree:      diskFree,
		SelfListening: selfListening,
	}
	if facts.DBPath != "" {
		if probe := openStoreProbe(ctx, facts.DBPath); probe != nil {
			env.Store = probe
		}
	}
	// PVE and Peers stay nil for now: both need an authenticated client that
	// only the daemon holds, and doctor's contract is that it works daemon-down.
	// Their checks report `skip` with that as the reason, which is accurate —
	// see the card note in docs/deployment.md.
	return facts, env
}

// applyDoctorDefaults fills in the paths the config does not carry explicitly,
// so the checks report against the real locations rather than skipping.
func applyDoctorDefaults(f *doctor.Facts) {
	if f.SessionKeyFile == "" {
		f.SessionKeyFile = config.DefaultSessionKeyFile
	}
	if f.PVETokenFile == "" {
		f.PVETokenFile = config.DefaultPVETokenFile
	}
	if f.DBPath == "" {
		f.DBPath = config.DefaultDBPath
	}
	if f.CaptureRoot == "" {
		f.CaptureRoot = config.DefaultCaptureRoot
	}
	if f.PmxcfsDir == "" {
		f.PmxcfsDir = "/etc/pve"
	}
	if f.PeerSecretFile == "" {
		f.PeerSecretFile = "/etc/pve/priv/vnprox/cluster-secret"
	}
	if f.SnapshotDir == "" && f.DBPath != "" {
		f.SnapshotDir = filepath.Dir(f.DBPath)
	}
}

// storeProbe adapts store.InspectSchemaVersion to doctor.StoreProbe. It reads
// the database file directly rather than through a running daemon, so it works
// with vnprox stopped.
type storeProbe struct{ path string }

func (s storeProbe) SchemaVersion(ctx context.Context) (int, int, error) {
	current, err := store.InspectSchemaVersion(ctx, s.path)
	if err != nil {
		return 0, 0, err
	}
	latest, err := store.LatestSchemaVersion()
	if err != nil {
		return 0, 0, err
	}
	return current, latest, nil
}

// openStoreProbe returns nil when there is no database to inspect, which is the
// normal state before first start — the check then skips rather than failing an
// install that has not happened yet.
func openStoreProbe(_ context.Context, path string) doctor.StoreProbe {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return storeProbe{path: path}
}

// portHolder answers "is anything listening, and what". It shells out to `ss`
// because that is what is present on a Proxmox node and because reading
// /proc/net/tcp correctly (IPv4 + IPv6, hex-encoded, then inode -> pid) is a
// great deal of code for a diagnostic. A missing `ss` is reported as an error,
// which the check turns into `skip` — not a false "port is free".
func portHolder(port int) (string, bool, error) {
	ss, err := exec.LookPath("ss")
	if err != nil {
		return "", false, fmt.Errorf("ss not found: %w", err)
	}
	out, err := exec.Command(ss, "-tlnpH", fmt.Sprintf("sport = :%d", port)).Output()
	if err != nil {
		return "", false, fmt.Errorf("ss: %w", err)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", false, nil
	}
	return summarizeSSLine(text), true, nil
}

// summarizeSSLine pulls the process name out of ss's users:(("name",pid=N,fd=M))
// column, falling back to the raw first line.
func summarizeSSLine(text string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if i := strings.Index(line, `users:(("`); i >= 0 {
		rest := line[i+len(`users:(("`):]
		if j := strings.IndexByte(rest, '"'); j > 0 {
			name := rest[:j]
			if k := strings.Index(rest, "pid="); k >= 0 {
				digits := rest[k+4:]
				end := 0
				for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
					end++
				}
				if end > 0 {
					return fmt.Sprintf("%s (pid %s)", name, digits[:end])
				}
			}
			return name
		}
	}
	return strings.Join(strings.Fields(line), " ")
}

// selfListening reports whether the process holding the port is vnproxd. Used
// to keep "the daemon is running" from being reported as a port conflict.
func selfListening(port int) bool {
	holder, inUse, err := portHolder(port)
	if err != nil || !inUse {
		return false
	}
	return strings.Contains(holder, "vnproxd")
}

// diskFree reports free and total bytes for the filesystem holding path,
// walking up to the nearest existing ancestor so a not-yet-created capture
// directory still reports its filesystem rather than erroring.
func diskFree(path string) (uint64, uint64, error) {
	target := path
	for i := 0; i < 32; i++ {
		if _, err := os.Stat(target); err == nil {
			break
		}
		parent := filepath.Dir(target)
		if parent == target {
			return 0, 0, fs.ErrNotExist
		}
		target = parent
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(target, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", target, err)
	}
	//nolint:gosec // Bavail/Blocks and Bsize are kernel-supplied filesystem
	// counters; the conversion is the documented way to get bytes.
	free := uint64(st.Bavail) * uint64(st.Bsize)
	total := uint64(st.Blocks) * uint64(st.Bsize)
	return free, total, nil
}

// parsePort is used by tests and by install.sh integration to keep a single
// spelling of "the listen port" in this file.
func parsePort(addr string) (int, bool) {
	i := strings.LastIndex(addr, ":")
	if i < 0 || i == len(addr)-1 {
		return 0, false
	}
	p, err := strconv.Atoi(addr[i+1:])
	if err != nil || p < 1 || p > 65535 {
		return 0, false
	}
	return p, true
}

// doctorLiveResponse mirrors internal/api's `GET /doctor/live` body.
type doctorLiveResponse struct {
	Results []doctor.Result `json:"results"`
}

// fetchLiveResults asks the running daemon for the checks it alone can answer.
//
// Every failure path — no token, no reachable daemon, an error status, a
// malformed body — resolves to `skip` results naming the daemon, and the
// reason is written to stderr so it is visible in a terminal without polluting
// the report (which may be JSON on stdout). Nothing here can turn into a
// `fail`: see the merge site's comment.
func fetchLiveResults(ctx context.Context, configPath, urlOverride, tokenOverride string, stderr io.Writer) []doctor.Result {
	rf := &remoteFlags{
		configPath: &configPath,
		url:        &urlOverride,
		token:      &tokenOverride,
		timeout:    ptrDuration(10 * time.Second),
		insecure:   ptrBool(true),
		output:     ptrString(defaultOutputFormat),
	}
	client, code := buildRemoteClient(rf, "doctor --live", io.Discard)
	if code != ExitSuccess || client == nil {
		reason := "no bearer token (--token or " + remoteTokenEnvVar + "), or the daemon's URL could not be determined"
		_, _ = fmt.Fprintf(stderr, "vnproxctl doctor: --live skipped: %s\n", reason)
		return doctor.UnreachableDaemonResults(reason)
	}

	var body doctorLiveResponse
	status, apiErr, err := client.doJSON(ctx, "GET", "/doctor/live", nil, &body)
	switch {
	case err != nil:
		reason := "could not reach the daemon: " + err.Error()
		_, _ = fmt.Fprintf(stderr, "vnproxctl doctor: --live skipped: %s\n", reason)
		return doctor.UnreachableDaemonResults(reason)
	case status >= 400:
		reason := fmt.Sprintf("the daemon answered %d", status)
		if apiErr != nil && apiErr.Message != "" {
			reason += ": " + apiErr.Message
		}
		_, _ = fmt.Fprintf(stderr, "vnproxctl doctor: --live skipped: %s\n", reason)
		return doctor.UnreachableDaemonResults(reason)
	case len(body.Results) == 0:
		reason := "the daemon returned no live results"
		_, _ = fmt.Fprintf(stderr, "vnproxctl doctor: --live skipped: %s\n", reason)
		return doctor.UnreachableDaemonResults(reason)
	}
	return body.Results
}

func ptrDuration(d time.Duration) *time.Duration { return &d }
func ptrBool(b bool) *bool                       { return &b }
func ptrString(s string) *string                 { return &s }
