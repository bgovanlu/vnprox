// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
)

// Facts are the things doctor learned about this installation before any check
// ran — essentially "what the config says". Supplied by the caller
// (cmd/vnproxctl) rather than loaded here, so the checks stay pure and the
// config-loading path is exercised by internal/config's own tests.
type Facts struct {
	// ConfigErr is non-nil if the config could not be loaded at all. Every
	// check that depends on config values skips with this as its reason,
	// rather than reporting ten confusing failures with one cause.
	ConfigErr error

	ConfigPath     string
	ListenAddr     string
	SessionKeyFile string
	PVETokenFile   string
	PeerSecretFile string
	DBPath         string
	CaptureRoot    string
	SnapshotDir    string
	PmxcfsDir      string
	PVEAPIURL      string
	BinaryVersion  string
}

// PVEProbe is the PVE-facing half of the environment. Nil means "no PVE client
// available" and the PVE checks skip rather than fail — doctor is meant to run
// before install, when there may be no token yet.
type PVEProbe interface {
	// Ping reports whether the PVE API answers, and its clock, for the skew
	// check. A non-nil error is a reachability failure.
	Ping(ctx context.Context) (serverTime time.Time, err error)
	// Privileges returns the effective privilege names the configured token
	// holds on this node.
	Privileges(ctx context.Context) ([]string, error)
}

// StoreProbe reports the store's schema version against the binary's.
type StoreProbe interface {
	SchemaVersion(ctx context.Context) (current, latest int, err error)
}

// PeerProbe reports peer-secret agreement across the cluster.
type PeerProbe interface {
	// SecretDigests maps node name to a non-reversible digest of that node's
	// cluster secret. Digests, never the secret: doctor's output goes into
	// support bundles, and a support bundle is going to be pasted into a forum
	// thread.
	SecretDigests(ctx context.Context) (map[string]string, error)
}

// Env is every interaction with the outside world, injected so each check can
// be driven by a deliberately broken fixture (AC1).
type Env struct {
	Now func() time.Time
	// Stat is os.Stat in production.
	Stat func(path string) (fs.FileInfo, error)
	// PortHolder reports whether something is listening on the port, and what.
	// holder may be empty when the port is busy but the owner is not visible.
	PortHolder func(port int) (holder string, inUse bool, err error)
	// DiskFree reports free and total bytes for the filesystem holding path.
	DiskFree func(path string) (free, total uint64, err error)
	// SelfListening reports whether the local vnprox daemon is the process
	// holding ListenAddr — a busy port is only a problem if it is not us.
	SelfListening func(port int) bool

	PVE   PVEProbe
	Store StoreProbe
	Peers PeerProbe
}

// DaemonHasRun reports whether vnprox has ever started on this machine, which
// is the difference between "not set up yet" and "broken". A store exists only
// after a first successful start, so its presence is the signal.
func (e Env) DaemonHasRun() bool { return e.Store != nil }

// Run executes every check and assembles the report. Checks never panic on a
// nil probe; they skip.
func Run(ctx context.Context, facts Facts, env Env) Report {
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}

	results := []Result{
		checkConfig(facts),
		checkKeyFiles(facts, env),
		checkPmxcfs(facts, env),
		checkSchemaVersion(ctx, facts, env),
		checkDiskHeadroom(facts, env),
		checkPortConflict(facts, env),
		checkPVEReachable(ctx, facts, env),
		checkPVEPrivileges(ctx, facts, env),
		checkPeerSecret(ctx, facts, env),
		checkClockSkew(ctx, facts, env, now()),
	}

	return Report{
		GeneratedAt: now().UTC(),
		Version:     facts.BinaryVersion,
		Results:     results,
		Summary:     summarize(results),
	}
}

// --- individual checks -----------------------------------------------------

func checkConfig(f Facts) Result {
	if f.ConfigErr != nil {
		// A missing file and an unparseable one need different advice, and
		// telling someone to fix a syntax error in a file that does not exist
		// is the kind of remediation that erodes trust in the whole report.
		if errors.Is(f.ConfigErr, fs.ErrNotExist) {
			return fail(CheckConfig,
				fmt.Sprintf("%s does not exist", displayPath(f.ConfigPath)),
				fmt.Sprintf("vnprox is not installed on this machine, or the config was removed. Install the package, or point doctor at the right file: vnproxctl doctor --config %s", displayPath(f.ConfigPath)))
		}
		return fail(CheckConfig,
			fmt.Sprintf("could not parse %s: %v", displayPath(f.ConfigPath), f.ConfigErr),
			fmt.Sprintf("fix the syntax error reported above in %s; reinstalling the package restores the shipped default", displayPath(f.ConfigPath)))
	}
	if f.ListenAddr == "" {
		return fail(CheckConfig,
			fmt.Sprintf("%s has no [server] listen address", displayPath(f.ConfigPath)),
			fmt.Sprintf(`set listen = "0.0.0.0:8007" under [server] in %s`, displayPath(f.ConfigPath)))
	}
	if _, err := splitPort(f.ListenAddr); err != nil {
		return fail(CheckConfig,
			fmt.Sprintf("[server] listen = %q is not a host:port address: %v", f.ListenAddr, err),
			fmt.Sprintf(`set listen to host:port form, e.g. "0.0.0.0:8007", in %s`, displayPath(f.ConfigPath)))
	}
	return pass(CheckConfig, fmt.Sprintf("%s loads; listening on %s", displayPath(f.ConfigPath), f.ListenAddr))
}

// checkKeyFiles verifies the key material vnprox cannot start without, and
// that it is not world- or group-readable. A readable session key is a
// complete compromise of every sealed credential in the store, so a loose mode
// is a fail, not a warn.
func checkKeyFiles(f Facts, env Env) Result {
	if f.ConfigErr != nil {
		return skip(CheckKeyFiles, "config did not load, so the key paths are unknown")
	}
	if env.Stat == nil {
		return skip(CheckKeyFiles, "no filesystem probe configured")
	}

	type keyFile struct {
		path     string
		label    string
		required bool
	}
	files := []keyFile{
		{f.SessionKeyFile, "session key", true},
		{f.PVETokenFile, "PVE token", false},
	}

	var problems []string
	worst := StatusPass
	for _, kf := range files {
		if kf.path == "" {
			continue
		}
		info, err := env.Stat(kf.path)
		if err != nil {
			if !kf.required {
				continue
			}
			// A missing session key means two very different things depending
			// on whether the daemon has ever run. Immediately after `apt
			// install` it is the expected state — the daemon generates the key
			// on first start — and calling that a failure would make doctor
			// unusable as an install gate, since every correct install would
			// fail it. Once a store exists, the daemon *has* run, and a missing
			// key is real.
			if !env.DaemonHasRun() {
				return warn(CheckKeyFiles,
					fmt.Sprintf("%s is not present yet at %s", kf.label, kf.path),
					"expected before the first start — the daemon generates it. Start vnprox and re-run: systemctl start vnprox && vnproxctl doctor")
			}
			return fail(CheckKeyFiles,
				fmt.Sprintf("%s is missing at %s: %v", kf.label, kf.path, err),
				fmt.Sprintf("the daemon generates it on first start, so its absence after the daemon has run means it was deleted. Check that %s exists and is writable by the vnprox service user, then restart vnprox", parentDir(kf.path)))
		}
		if mode := info.Mode().Perm(); mode&^keyFileMaxMode != 0 {
			problems = append(problems, fmt.Sprintf("%s (%s) is mode %04o", kf.label, kf.path, mode))
			worst = StatusFail
		}
	}

	if worst == StatusFail {
		return fail(CheckKeyFiles,
			"key material is readable beyond its owner: "+strings.Join(problems, "; "),
			"run: chmod 0600 "+strings.Join(pathsOf(problems), " ")+" — anything that could read these can decrypt every stored credential")
	}
	return pass(CheckKeyFiles, "key files present with owner-only permissions")
}

// checkPmxcfs verifies /etc/pve is present and readable. Its absence is the
// single most common reason a fresh install does nothing useful: without
// pmxcfs there is no cluster config, no certificates, and no peer discovery.
func checkPmxcfs(f Facts, env Env) Result {
	dir := f.PmxcfsDir
	if dir == "" {
		dir = "/etc/pve"
	}
	if env.Stat == nil {
		return skip(CheckPmxcfs, "no filesystem probe configured")
	}
	info, err := env.Stat(dir)
	if err != nil {
		return fail(CheckPmxcfs,
			fmt.Sprintf("%s is not accessible: %v", dir, err),
			"vnprox must run on a Proxmox VE node. Check that pve-cluster is running: systemctl status pve-cluster")
	}
	if !info.IsDir() {
		return fail(CheckPmxcfs,
			fmt.Sprintf("%s exists but is not a directory", dir),
			"expected the pmxcfs mountpoint; check: systemctl status pve-cluster && mount | grep /etc/pve")
	}
	return pass(CheckPmxcfs, dir+" is present and readable")
}

func checkSchemaVersion(ctx context.Context, f Facts, env Env) Result {
	if env.Store == nil {
		return skip(CheckSchemaVersion, "no store available (daemon not installed, or the database has not been created yet)")
	}
	current, latest, err := env.Store.SchemaVersion(ctx)
	if err != nil {
		return fail(CheckSchemaVersion,
			fmt.Sprintf("could not read the schema version from %s: %v", displayPath(f.DBPath), err),
			fmt.Sprintf("check that %s exists and is readable by the vnprox service user; if it is corrupt, restore from a backup (vnproxctl restore)", displayPath(f.DBPath)))
	}
	switch {
	case current > latest:
		// The dangerous direction: a store written by a newer binary. Migrating
		// down is not supported and starting anyway risks silent data loss.
		return fail(CheckSchemaVersion,
			fmt.Sprintf("the database is at schema %d but this binary only knows up to %d — it was written by a newer vnprox", current, latest),
			"downgrade is not supported. Reinstall the newer vnprox version, or restore a backup taken at schema "+fmt.Sprint(latest))
	case current < latest:
		return warn(CheckSchemaVersion,
			fmt.Sprintf("the database is at schema %d; this binary expects %d", current, latest),
			"the daemon migrates forward automatically on start: systemctl restart vnprox")
	default:
		return pass(CheckSchemaVersion, fmt.Sprintf("database schema %d matches the binary", current))
	}
}

func checkDiskHeadroom(f Facts, env Env) Result {
	if env.DiskFree == nil {
		return skip(CheckDiskHeadroom, "no disk probe configured")
	}
	paths := uniqueNonEmpty(f.DBPath, f.CaptureRoot, f.SnapshotDir)
	if len(paths) == 0 {
		return skip(CheckDiskHeadroom, "config did not load, so the data paths are unknown")
	}

	worst := StatusPass
	var details, remediations []string
	for _, p := range paths {
		free, total, err := env.DiskFree(p)
		if err != nil {
			// A path that does not exist yet is not a disk problem.
			continue
		}
		switch {
		case free < diskHeadroomFailBytes:
			worst = StatusFail
			details = append(details, fmt.Sprintf("%s has %s free of %s", p, humanBytes(free), humanBytes(total)))
			remediations = append(remediations, fmt.Sprintf("free space on %s — snapshots and packet captures write here, and a full filesystem on a hypervisor is an outage", p))
		case free < diskHeadroomWarnBytes:
			if worst != StatusFail {
				worst = StatusWarn
			}
			details = append(details, fmt.Sprintf("%s has %s free of %s", p, humanBytes(free), humanBytes(total)))
			remediations = append(remediations, fmt.Sprintf("consider retention settings for %s (see [snapshots] and [capture] in the config)", p))
		}
	}

	switch worst {
	case StatusFail:
		return fail(CheckDiskHeadroom, strings.Join(details, "; "), strings.Join(remediations, "; "))
	case StatusWarn:
		return warn(CheckDiskHeadroom, strings.Join(details, "; "), strings.Join(remediations, "; "))
	default:
		return pass(CheckDiskHeadroom, fmt.Sprintf("adequate free space on %s", strings.Join(paths, ", ")))
	}
}

// checkPortConflict is the check install.sh already performs in its own way
// (the PBS :8007 collision). Here it also runs *after* install, where the
// interesting case is different: the port is busy because vnprox itself is
// running, which is fine and must not be reported as a conflict.
func checkPortConflict(f Facts, env Env) Result {
	if f.ConfigErr != nil || f.ListenAddr == "" {
		return skip(CheckPortConflict, "config did not load, so the listen port is unknown")
	}
	if env.PortHolder == nil {
		return skip(CheckPortConflict, "no port probe configured")
	}
	port, err := splitPort(f.ListenAddr)
	if err != nil {
		return skip(CheckPortConflict, "listen address is not host:port; see the config check")
	}

	holder, inUse, err := env.PortHolder(port)
	if err != nil {
		return skip(CheckPortConflict, fmt.Sprintf("could not determine what is listening on %d: %v", port, err))
	}
	if !inUse {
		return pass(CheckPortConflict, fmt.Sprintf("port %d is free", port))
	}
	if env.SelfListening != nil && env.SelfListening(port) {
		return pass(CheckPortConflict, fmt.Sprintf("port %d is held by vnprox itself", port))
	}

	detail := fmt.Sprintf("port %d is already in use by something other than vnprox", port)
	if holder != "" {
		detail += ": " + holder
	}
	remediation := fmt.Sprintf("stop the other service, or change [server] listen in %s to a free port", displayPath(f.ConfigPath))
	if port == 8007 {
		// The specific collision install.sh knows about, named so the operator
		// does not have to recognise it themselves.
		remediation = "port 8007 is also Proxmox Backup Server's default. " + remediation
	}
	return fail(CheckPortConflict, detail, remediation)
}

func checkPVEReachable(ctx context.Context, f Facts, env Env) Result {
	if env.PVE == nil {
		// Deliberately does NOT claim why. `vnproxctl doctor` has no
		// authenticated PVE client of its own (T-1904-followup-02), so from
		// here "not configured" and "configured and working" are
		// indistinguishable — and asserting the former on a node that is
		// perfectly set up is exactly the kind of confident-and-wrong output
		// that makes an operator stop trusting the whole report. Observed on
		// real hardware: pvecube runs this check as a skip while its
		// collectors are polling PVE successfully.
		return skip(CheckPVEReachable, "not checked from the CLI — this needs the daemon's authenticated PVE client. Use `vnproxctl status` for live PVE health, or see the daemon's own logs")
	}
	if _, err := env.PVE.Ping(ctx); err != nil {
		target := f.PVEAPIURL
		if target == "" {
			target = "the PVE API"
		}
		return fail(CheckPVEReachable,
			fmt.Sprintf("cannot reach %s: %v", target, err),
			fmt.Sprintf("check that pveproxy is running (systemctl status pveproxy), that %s is correct, and that the token in %s is valid and not expired", target, displayPath(f.PVETokenFile)))
	}
	return pass(CheckPVEReachable, "PVE API reachable and the token authenticates")
}

// checkPVEPrivileges names the privileges vnprox's OWN configured token
// (vnprox@pve!daemon) needs, taken from internal/auth.DaemonTokenPrivileges
// rather than a second list of its own.
//
// This deliberately does NOT use auth.RequiredPrivileges: that list answers
// a different question ("what would let an *operator's* own PVE ticket
// unlock every vnprox UI capability"), and includes three write privileges
// (Sys.Modify, SDN.Allocate, VM.Config.Network) the daemon's own token is
// never granted by design — vnprox-setup provisions it read-only, and every
// write vnprox makes goes out on the applying user's own sealed PVE ticket
// instead (docs/security.md's "Apply-time revert ticket"). Gating this
// check on RequiredPrivileges used to fail this check on every
// correctly-provisioned install, always — confirmed on a real two-node
// cluster, see auth.DaemonTokenPrivileges' doc comment for the full history
// (planning/reports/blocked-validation.md §2.4).
func checkPVEPrivileges(ctx context.Context, f Facts, env Env) Result {
	if env.PVE == nil {
		return skip(CheckPVEPrivileges, "not checked from the CLI — this needs the daemon's authenticated PVE client. The privileges vnprox uses are listed in docs/deployment.md")
	}
	held, err := env.PVE.Privileges(ctx)
	if err != nil {
		return skip(CheckPVEPrivileges, fmt.Sprintf("could not read the token's privileges: %v", err))
	}
	have := make(map[string]bool, len(held))
	for _, p := range held {
		have[p] = true
	}

	var missingRequired, missingOptional []string
	// From internal/auth, not a list of our own: see DaemonTokenPrivileges'
	// doc comment for why this is a distinct list from RequiredPrivileges,
	// and why a hand-maintained second copy here would eventually gate on
	// the wrong set again.
	for _, rp := range auth.DaemonTokenPrivileges() {
		if have[rp.Name] {
			continue
		}
		entry := fmt.Sprintf("%s (%s)", rp.Name, rp.Unlocks)
		if rp.Optional {
			missingOptional = append(missingOptional, entry)
		} else {
			missingRequired = append(missingRequired, entry)
		}
	}

	switch {
	case len(missingRequired) > 0:
		return fail(CheckPVEPrivileges,
			"the configured PVE token is missing privileges vnprox needs: "+strings.Join(missingRequired, "; "),
			fmt.Sprintf("re-run vnprox-setup (it provisions exactly these via the VnproxAuditor role), or grant them directly: pveum acl modify / --tokens '<user>@pve!<token>' --roles VnproxAuditor — or a custom role holding %s", strings.Join(namesOnly(missingRequired), ", ")))
	case len(missingOptional) > 0:
		return warn(CheckPVEPrivileges,
			"the configured PVE token is missing optional privileges: "+strings.Join(missingOptional, "; "),
			fmt.Sprintf("grant %s if you need those features; vnprox works without them", strings.Join(namesOnly(missingOptional), ", ")))
	default:
		return pass(CheckPVEPrivileges, "the PVE token holds every privilege vnprox's own daemon needs")
	}
}

// checkPeerSecret verifies every node agrees on the cluster secret. A
// disagreement is why peer requests 401 while every node individually looks
// healthy — the failure that is hardest to diagnose from any single node.
func checkPeerSecret(ctx context.Context, f Facts, env Env) Result {
	if env.Peers == nil {
		return skip(CheckPeerSecret, "not checked from the CLI — comparing the secret across nodes needs the daemon's peer client")
	}
	digests, err := env.Peers.SecretDigests(ctx)
	if err != nil {
		return skip(CheckPeerSecret, fmt.Sprintf("could not collect peer secret digests: %v", err))
	}
	if len(digests) <= 1 {
		return pass(CheckPeerSecret, "single-node cluster; nothing to agree with")
	}

	byDigest := make(map[string][]string)
	for node, d := range digests {
		byDigest[d] = append(byDigest[d], node)
	}
	if len(byDigest) == 1 {
		return pass(CheckPeerSecret, fmt.Sprintf("all %d nodes share the same cluster secret", len(digests)))
	}

	groups := make([]string, 0, len(byDigest))
	for d, nodes := range byDigest {
		sort.Strings(nodes)
		groups = append(groups, fmt.Sprintf("%s: %s", short(d), strings.Join(nodes, ",")))
	}
	sort.Strings(groups)
	return fail(CheckPeerSecret,
		fmt.Sprintf("nodes disagree on the cluster secret (%d distinct values) — %s", len(byDigest), strings.Join(groups, " | ")),
		fmt.Sprintf("copy %s from the majority node to the others and restart vnprox on each; peer API calls fail closed until they match", displayPath(f.PeerSecretFile)))
}

// checkClockSkew matters because it breaks two things silently: PVE ticket
// lifetimes, and internal/peer's ±30s replay window. Both present as
// authentication failures rather than as a clock problem.
func checkClockSkew(ctx context.Context, f Facts, env Env, localNow time.Time) Result {
	if env.PVE == nil {
		return skip(CheckClockSkew, "not checked from the CLI — there is no reference clock without the daemon's PVE client. `timedatectl` shows whether NTP is active")
	}
	serverTime, err := env.PVE.Ping(ctx)
	if err != nil {
		return skip(CheckClockSkew, "PVE is unreachable; see the pve_reachable check")
	}
	if serverTime.IsZero() {
		return skip(CheckClockSkew, "PVE did not report a server time")
	}

	skew := localNow.Sub(serverTime)
	if skew < 0 {
		skew = -skew
	}
	detail := fmt.Sprintf("local clock differs from PVE by %s", skew.Round(time.Millisecond))
	switch {
	case skew >= clockSkewFail:
		return fail(CheckClockSkew, detail,
			"enable NTP (timedatectl set-ntp true) — peer authentication uses a ±30s replay window, so this breaks cluster operations and PVE ticket lifetimes")
	case skew >= clockSkewWarn:
		return warn(CheckClockSkew, detail,
			"enable NTP (timedatectl set-ntp true) — this is over half the ±30s peer replay window, so further drift will start failing peer requests")
	default:
		return pass(CheckClockSkew, detail)
	}
}

// --- helpers ---------------------------------------------------------------

// errNoPort is returned by splitPort for an address with no usable port.
var errNoPort = errors.New("no port")

func splitPort(addr string) (int, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 || i == len(addr)-1 {
		return 0, errNoPort
	}
	var port int
	if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err != nil {
		return 0, errNoPort
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d out of range", port)
	}
	return port, nil
}

func displayPath(p string) string {
	if p == "" {
		return "the config file"
	}
	return p
}

func parentDir(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

// pathsOf pulls the parenthesised paths back out of the problem strings built
// in checkKeyFiles, so the remediation can name real chmod arguments.
func pathsOf(problems []string) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		start := strings.Index(p, "(")
		end := strings.Index(p, ")")
		if start >= 0 && end > start {
			out = append(out, p[start+1:end])
		}
	}
	return out
}

// namesOnly strips the "(explanation)" tail from privilege entries.
func namesOnly(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if i := strings.Index(e, " ("); i > 0 {
			out = append(out, e[:i])
			continue
		}
		out = append(out, e)
	}
	return out
}

func uniqueNonEmpty(paths ...string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
