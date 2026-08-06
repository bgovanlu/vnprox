package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
)

// --- fakes -----------------------------------------------------------------

type fakeInfo struct {
	mode  fs.FileMode
	isDir bool
}

func (f fakeInfo) Name() string       { return "fake" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.isDir }
func (f fakeInfo) Sys() any           { return nil }

// statFS builds a Stat func from a path -> info map. Anything absent errors,
// which is what a missing file looks like to a real os.Stat.
func statFS(files map[string]fakeInfo) func(string) (fs.FileInfo, error) {
	return func(path string) (fs.FileInfo, error) {
		info, ok := files[path]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return info, nil
	}
}

type fakePVE struct {
	serverTime time.Time
	pingErr    error
	privErr    error
	privs      []string
}

func (f fakePVE) Ping(context.Context) (time.Time, error)      { return f.serverTime, f.pingErr }
func (f fakePVE) Privileges(context.Context) ([]string, error) { return f.privs, f.privErr }

type fakeStore struct {
	err             error
	current, latest int
}

func (f fakeStore) SchemaVersion(context.Context) (int, int, error) {
	return f.current, f.latest, f.err
}

type fakePeers struct {
	digests map[string]string
	err     error
}

func (f fakePeers) SecretDigests(context.Context) (map[string]string, error) {
	return f.digests, f.err
}

// healthyFacts/healthyEnv are the all-good baseline every negative case is a
// single mutation away from. Without them a "check fails on broken input" test
// proves nothing — the check might fail on everything.
func healthyFacts() Facts {
	return Facts{
		ConfigPath:     "/etc/vnprox/vnprox.toml",
		ListenAddr:     "0.0.0.0:8007",
		SessionKeyFile: "/etc/vnprox/keys/session.key",
		PVETokenFile:   "/etc/vnprox/keys/pve-token",
		PeerSecretFile: "/etc/pve/priv/vnprox/cluster-secret",
		DBPath:         "/var/lib/vnprox/vnprox.db",
		CaptureRoot:    "/var/lib/vnprox/captures",
		PmxcfsDir:      "/etc/pve",
		PVEAPIURL:      "https://127.0.0.1:8006",
		BinaryVersion:  "3.0.4",
	}
}

func healthyEnv(now time.Time) Env {
	return Env{
		Now: func() time.Time { return now },
		Stat: statFS(map[string]fakeInfo{
			"/etc/vnprox/keys/session.key": {mode: 0o600},
			"/etc/vnprox/keys/pve-token":   {mode: 0o600},
			"/etc/pve":                     {mode: 0o755 | fs.ModeDir, isDir: true},
		}),
		PortHolder:    func(int) (string, bool, error) { return "", false, nil },
		DiskFree:      func(string) (uint64, uint64, error) { return 50 << 30, 100 << 30, nil },
		SelfListening: func(int) bool { return false },
		PVE:           fakePVE{privs: allPrivilegeNames(), serverTime: now},
		Store:         fakeStore{current: 42, latest: 42},
		Peers:         fakePeers{digests: map[string]string{"pve1": "aaaa", "pve2": "aaaa"}},
	}
}

func allPrivilegeNames() []string {
	var out []string
	for _, rp := range auth.RequiredPrivileges() {
		out = append(out, rp.Name)
	}
	return out
}

func find(t *testing.T, r Report, check string) Result {
	t.Helper()
	for _, res := range r.Results {
		if res.Check == check {
			return res
		}
	}
	t.Fatalf("check %q not present in report", check)
	return Result{}
}

// --- the control -----------------------------------------------------------

// TestHealthyInstallPassesEverything is the control for every negative case
// below. If a healthy fixture does not pass cleanly, then "broken input fails"
// tells us nothing about whether the check discriminates.
func TestHealthyInstallPassesEverything(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	rep := Run(context.Background(), healthyFacts(), healthyEnv(now))

	if err := rep.Validate(); err != nil {
		t.Fatalf("healthy report is malformed: %v", err)
	}
	for _, res := range rep.Results {
		if res.Status != StatusPass {
			t.Errorf("check %q = %s (%s); the healthy baseline must pass everything", res.Check, res.Status, res.Detail)
		}
	}
	if rep.Failed() {
		t.Error("healthy report reports Failed()")
	}
	if rep.Summary.Pass != len(AllChecks) {
		t.Errorf("summary counted %d passes; want %d (one per check)", rep.Summary.Pass, len(AllChecks))
	}
}

// TestRunReportsEveryCheck guards against a check being silently dropped from
// Run — the report would still look green, with one fewer thing looked at.
func TestRunReportsEveryCheck(t *testing.T) {
	rep := Run(context.Background(), healthyFacts(), healthyEnv(time.Now()))
	if len(rep.Results) != len(AllChecks) {
		t.Fatalf("Run produced %d results; AllChecks lists %d", len(rep.Results), len(AllChecks))
	}
	for _, name := range AllChecks {
		find(t, rep, name)
	}
}

// --- one deliberately broken fixture per check (AC1) -----------------------

func TestEachCheckFailsOnBrokenInput(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		mutate     func(*Facts, *Env)
		name       string
		check      string
		wantStatus Status
		wantDetail string
	}{
		{
			name:  "config that will not parse",
			check: CheckConfig,
			mutate: func(f *Facts, _ *Env) {
				f.ConfigErr = errors.New("line 4: expected '='")
			},
			wantStatus: StatusFail,
			wantDetail: "line 4",
		},
		{
			// Distinct from the parse error above: a missing file must not be
			// told to fix its syntax.
			name:  "config file absent",
			check: CheckConfig,
			mutate: func(f *Facts, _ *Env) {
				f.ConfigErr = fmt.Errorf("reading config: %w", fs.ErrNotExist)
			},
			wantStatus: StatusFail,
			wantDetail: "does not exist",
		},
		{
			name:  "listen address with no port",
			check: CheckConfig,
			mutate: func(f *Facts, _ *Env) {
				f.ListenAddr = "0.0.0.0"
			},
			wantStatus: StatusFail,
			wantDetail: "host:port",
		},
		{
			name:  "session key missing",
			check: CheckKeyFiles,
			mutate: func(_ *Facts, e *Env) {
				e.Stat = statFS(map[string]fakeInfo{
					"/etc/pve": {mode: 0o755 | fs.ModeDir, isDir: true},
				})
			},
			wantStatus: StatusFail,
			wantDetail: "session key is missing",
		},
		{
			name:  "session key world-readable",
			check: CheckKeyFiles,
			mutate: func(_ *Facts, e *Env) {
				e.Stat = statFS(map[string]fakeInfo{
					"/etc/vnprox/keys/session.key": {mode: 0o644},
					"/etc/vnprox/keys/pve-token":   {mode: 0o600},
					"/etc/pve":                     {mode: 0o755 | fs.ModeDir, isDir: true},
				})
			},
			wantStatus: StatusFail,
			wantDetail: "0644",
		},
		{
			name:  "pmxcfs absent",
			check: CheckPmxcfs,
			mutate: func(_ *Facts, e *Env) {
				e.Stat = statFS(map[string]fakeInfo{
					"/etc/vnprox/keys/session.key": {mode: 0o600},
					"/etc/vnprox/keys/pve-token":   {mode: 0o600},
				})
			},
			wantStatus: StatusFail,
			wantDetail: "/etc/pve",
		},
		{
			name:  "store newer than the binary",
			check: CheckSchemaVersion,
			mutate: func(_ *Facts, e *Env) {
				e.Store = fakeStore{current: 43, latest: 42}
			},
			wantStatus: StatusFail,
			wantDetail: "written by a newer vnprox",
		},
		{
			name:  "store behind the binary",
			check: CheckSchemaVersion,
			mutate: func(_ *Facts, e *Env) {
				e.Store = fakeStore{current: 41, latest: 42}
			},
			wantStatus: StatusWarn,
			wantDetail: "schema 41",
		},
		{
			name:  "disk nearly full",
			check: CheckDiskHeadroom,
			mutate: func(_ *Facts, e *Env) {
				e.DiskFree = func(string) (uint64, uint64, error) { return 100 << 20, 100 << 30, nil }
			},
			wantStatus: StatusFail,
			wantDetail: "free",
		},
		{
			name:  "disk low but not critical",
			check: CheckDiskHeadroom,
			mutate: func(_ *Facts, e *Env) {
				e.DiskFree = func(string) (uint64, uint64, error) { return 1 << 30, 100 << 30, nil }
			},
			wantStatus: StatusWarn,
			wantDetail: "free",
		},
		{
			name:  "port held by another service",
			check: CheckPortConflict,
			mutate: func(_ *Facts, e *Env) {
				e.PortHolder = func(int) (string, bool, error) { return "proxmox-backup-proxy", true, nil }
			},
			wantStatus: StatusFail,
			wantDetail: "proxmox-backup-proxy",
		},
		{
			name:  "PVE unreachable",
			check: CheckPVEReachable,
			mutate: func(_ *Facts, e *Env) {
				e.PVE = fakePVE{pingErr: errors.New("connection refused")}
			},
			wantStatus: StatusFail,
			wantDetail: "connection refused",
		},
		{
			name:  "token missing a required privilege",
			check: CheckPVEPrivileges,
			mutate: func(_ *Facts, e *Env) {
				e.PVE = fakePVE{privs: []string{"Sys.Audit"}, serverTime: now}
			},
			wantStatus: StatusFail,
			wantDetail: "Sys.Modify",
		},
		{
			name:  "token missing only an optional privilege",
			check: CheckPVEPrivileges,
			mutate: func(_ *Facts, e *Env) {
				var required []string
				for _, rp := range auth.RequiredPrivileges() {
					if !rp.Optional {
						required = append(required, rp.Name)
					}
				}
				e.PVE = fakePVE{privs: required, serverTime: now}
			},
			wantStatus: StatusWarn,
			wantDetail: "Sys.Console",
		},
		{
			name:  "nodes disagree on the cluster secret",
			check: CheckPeerSecret,
			mutate: func(_ *Facts, e *Env) {
				e.Peers = fakePeers{digests: map[string]string{
					"pve1": "aaaaaaaaaaaaaaaa",
					"pve2": "bbbbbbbbbbbbbbbb",
					"pve3": "aaaaaaaaaaaaaaaa",
				}}
			},
			wantStatus: StatusFail,
			wantDetail: "disagree",
		},
		{
			name:  "clock skewed past the replay window",
			check: CheckClockSkew,
			mutate: func(_ *Facts, e *Env) {
				e.PVE = fakePVE{privs: allPrivilegeNames(), serverTime: now.Add(-90 * time.Second)}
			},
			wantStatus: StatusFail,
			wantDetail: "differs from PVE",
		},
		{
			name:  "clock skewed past half the window",
			check: CheckClockSkew,
			mutate: func(_ *Facts, e *Env) {
				e.PVE = fakePVE{privs: allPrivilegeNames(), serverTime: now.Add(-20 * time.Second)}
			},
			wantStatus: StatusWarn,
			wantDetail: "differs from PVE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := healthyFacts()
			env := healthyEnv(now)
			tt.mutate(&facts, &env)

			rep := Run(context.Background(), facts, env)
			if err := rep.Validate(); err != nil {
				t.Fatalf("report is malformed: %v", err)
			}

			got := find(t, rep, tt.check)
			if got.Status != tt.wantStatus {
				t.Errorf("check %q = %s (%s); want %s", tt.check, got.Status, got.Detail, tt.wantStatus)
			}
			if !strings.Contains(got.Detail, tt.wantDetail) {
				t.Errorf("detail %q does not mention %q", got.Detail, tt.wantDetail)
			}
			if got.Remediation == "" {
				t.Errorf("check %q is %s with no remediation (AC2)", tt.check, got.Status)
			}
			if tt.wantStatus == StatusFail && !rep.Failed() {
				t.Error("a failing check did not set Failed(), so the exit code would be 0 (AC4)")
			}
		})
	}
}

// TestEveryCheckHasABrokenFixture is the meta-assertion behind AC1: it is not
// enough that the checks above fail: every check in AllChecks must appear in
// that table. A check with no broken fixture is a check nobody has proven can
// fail.
func TestEveryCheckHasABrokenFixture(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	covered := make(map[string]bool)
	// Re-derive coverage by running each mutation and recording which check it
	// moved off pass, rather than trusting the table's own `check` field.
	mutations := []func(*Facts, *Env){
		func(f *Facts, _ *Env) { f.ConfigErr = errors.New("boom") },
		func(_ *Facts, e *Env) { e.Stat = statFS(map[string]fakeInfo{}) },
		func(_ *Facts, e *Env) { e.Store = fakeStore{current: 43, latest: 42} },
		func(_ *Facts, e *Env) {
			e.DiskFree = func(string) (uint64, uint64, error) { return 1 << 20, 100 << 30, nil }
		},
		func(_ *Facts, e *Env) {
			e.PortHolder = func(int) (string, bool, error) { return "pbs", true, nil }
		},
		func(_ *Facts, e *Env) { e.PVE = fakePVE{pingErr: errors.New("refused")} },
		func(_ *Facts, e *Env) { e.PVE = fakePVE{privs: []string{"Sys.Audit"}, serverTime: now} },
		func(_ *Facts, e *Env) {
			e.Peers = fakePeers{digests: map[string]string{"a": "1", "b": "2"}}
		},
		func(_ *Facts, e *Env) {
			e.PVE = fakePVE{privs: allPrivilegeNames(), serverTime: now.Add(-time.Hour)}
		},
	}

	for _, mutate := range mutations {
		facts := healthyFacts()
		env := healthyEnv(now)
		mutate(&facts, &env)
		for _, res := range Run(context.Background(), facts, env).Results {
			if res.Status != StatusPass {
				covered[res.Check] = true
			}
		}
	}

	for _, name := range AllChecks {
		if !covered[name] {
			t.Errorf("check %q never left pass under any broken fixture: nothing proves it can fail (AC1)", name)
		}
	}
}

// TestSkipIsNotPass pins the distinction the report depends on. A missing probe
// must report skip — never pass — or an absent check reads as a healthy one.
func TestSkipIsNotPass(t *testing.T) {
	facts := healthyFacts()
	env := Env{Now: func() time.Time { return time.Now() }} // every probe nil

	rep := Run(context.Background(), facts, env)
	if err := rep.Validate(); err != nil {
		t.Fatalf("report is malformed: %v", err)
	}
	if rep.Failed() {
		t.Error("an all-probes-absent run reported Failed(); absence of information is not failure")
	}
	for _, res := range rep.Results {
		// CheckConfig is the one legitimate exception: it validates the Facts
		// the caller already loaded and consults no probe at all, so it has
		// really looked at something here. Every other check depends on a probe
		// and must say so rather than claiming health it did not verify.
		if res.Check == CheckConfig {
			continue
		}
		if res.Status == StatusPass {
			t.Errorf("check %q passed with no probe configured: %s", res.Check, res.Detail)
		}
	}
	if rep.Summary.Skip == 0 {
		t.Fatal("no check reported skip with every probe absent")
	}
	for _, res := range rep.Results {
		if res.Status == StatusSkip && strings.TrimSpace(res.Detail) == "" {
			t.Errorf("check %q skipped without saying why", res.Check)
		}
	}
}

// TestValidateCatchesMissingRemediation proves Validate's guard is real, so the
// AC2 assertions in the table above are not the only thing standing between a
// remediation-less warn and a shipped release.
func TestValidateCatchesMissingRemediation(t *testing.T) {
	// The table holds the results rather than a whole Report: the Report value
	// is large enough that an anonymous struct wrapping it trips the
	// fieldalignment linter, and the wrapper adds nothing.
	tests := []struct {
		name    string
		wantErr string
		results []Result
	}{
		{
			name:    "fail without remediation",
			results: []Result{{Check: "x", Status: StatusFail, Detail: "d"}},
			wantErr: "no remediation",
		},
		{
			name:    "warn without remediation",
			results: []Result{{Check: "x", Status: StatusWarn, Detail: "d"}},
			wantErr: "no remediation",
		},
		{
			name:    "duplicate check",
			results: []Result{{Check: "x", Status: StatusPass, Detail: "d"}, {Check: "x", Status: StatusPass, Detail: "d"}},
			wantErr: "twice",
		},
		{
			name:    "unknown status",
			results: []Result{{Check: "x", Status: "maybe", Detail: "d"}},
			wantErr: "unknown status",
		},
		{
			name:    "no detail",
			results: []Result{{Check: "x", Status: StatusPass}},
			wantErr: "no detail",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Report{Results: tt.results}.Validate()
			if err == nil {
				t.Fatalf("Validate() succeeded; want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q; want it to contain %q", err, tt.wantErr)
			}
		})
	}

	// Control: a well-formed report validates, so the cases above fail for the
	// reason claimed rather than because Validate rejects everything.
	ok := Report{Results: []Result{
		{Check: "x", Status: StatusPass, Detail: "fine"},
		{Check: "y", Status: StatusFail, Detail: "broken", Remediation: "fix it"},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("the control report failed to validate, making every case above vacuous: %v", err)
	}
}

// TestPortConflictIgnoresOurselves pins the post-install case: the port being
// busy because vnprox is running is the normal state, not a conflict.
func TestPortConflictIgnoresOurselves(t *testing.T) {
	now := time.Now()
	env := healthyEnv(now)
	env.PortHolder = func(int) (string, bool, error) { return "vnproxd", true, nil }
	env.SelfListening = func(int) bool { return true }

	got := find(t, Run(context.Background(), healthyFacts(), env), CheckPortConflict)
	if got.Status != StatusPass {
		t.Errorf("port_conflict = %s (%s); a port held by vnprox itself is not a conflict", got.Status, got.Detail)
	}
}

// TestPortConflictNamesPBS checks the specific collision install.sh already
// knows about is named, since "port 8007 is busy" on a Proxmox host is almost
// always Proxmox Backup Server and saying so saves the lookup.
func TestPortConflictNamesPBS(t *testing.T) {
	env := healthyEnv(time.Now())
	env.PortHolder = func(int) (string, bool, error) { return "", true, nil }

	got := find(t, Run(context.Background(), healthyFacts(), env), CheckPortConflict)
	if got.Status != StatusFail {
		t.Fatalf("port_conflict = %s; want fail", got.Status)
	}
	if !strings.Contains(got.Remediation, "Backup Server") {
		t.Errorf("remediation %q does not mention Proxmox Backup Server for port 8007", got.Remediation)
	}
}

// TestPeerSecretDigestsNeverLeakSecrets: doctor output goes into support
// bundles. The peer check must report agreement without reproducing the value
// nodes are agreeing on.
func TestPeerSecretDigestsNeverLeakSecrets(t *testing.T) {
	const secretLike = "s3cr3t-cluster-value-do-not-print"
	env := healthyEnv(time.Now())
	env.Peers = fakePeers{digests: map[string]string{
		"pve1": secretLike,
		"pve2": "different-value-entirely",
	}}

	rep := Run(context.Background(), healthyFacts(), env)
	got := find(t, rep, CheckPeerSecret)
	if got.Status != StatusFail {
		t.Fatalf("peer_secret = %s; want fail on disagreement", got.Status)
	}
	full := got.Detail + " " + got.Remediation
	if strings.Contains(full, secretLike) {
		t.Errorf("the peer check printed a full digest value: %q", full)
	}
	// Control: it must still be useful — the node names have to appear, or the
	// operator cannot tell which node to fix.
	if !strings.Contains(got.Detail, "pve1") || !strings.Contains(got.Detail, "pve2") {
		t.Errorf("detail %q does not name the disagreeing nodes", got.Detail)
	}
}

// TestRenderPutsProblemsFirst: an operator reads the top of the output.
func TestRenderPutsProblemsFirst(t *testing.T) {
	rep := Report{Results: []Result{
		{Check: "a_pass", Status: StatusPass, Detail: "fine"},
		{Check: "b_fail", Status: StatusFail, Detail: "broken", Remediation: "fix"},
		{Check: "c_warn", Status: StatusWarn, Detail: "iffy", Remediation: "look"},
	}}
	rep.Summary = summarize(rep.Results)

	out := rep.Render()
	failAt := strings.Index(out, "b_fail")
	warnAt := strings.Index(out, "c_warn")
	passAt := strings.Index(out, "a_pass")
	if failAt >= warnAt || warnAt >= passAt {
		t.Errorf("render order is fail(%d) warn(%d) pass(%d); want failures first:\n%s", failAt, warnAt, passAt, out)
	}
	if !strings.Contains(out, "-> fix") {
		t.Error("render dropped the remediation")
	}
}

// TestSplitPort covers the parsing the config and port checks both depend on.
func TestSplitPort(t *testing.T) {
	tests := []struct {
		addr    string
		want    int
		wantErr bool
	}{
		{addr: "0.0.0.0:8007", want: 8007},
		{addr: "127.0.0.1:8007", want: 8007},
		{addr: "[::1]:8007", want: 8007},
		{addr: ":8007", want: 8007},
		{addr: "0.0.0.0", wantErr: true},
		{addr: "0.0.0.0:", wantErr: true},
		{addr: "0.0.0.0:notaport", wantErr: true},
		{addr: "0.0.0.0:70000", wantErr: true},
		{addr: "", wantErr: true},
	}
	for _, tt := range tests {
		got, err := splitPort(tt.addr)
		if tt.wantErr {
			if err == nil {
				t.Errorf("splitPort(%q) = %d, nil; want an error", tt.addr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitPort(%q) errored: %v", tt.addr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("splitPort(%q) = %d; want %d", tt.addr, got, tt.want)
		}
	}
}

// TestSessionKeyAbsentBeforeFirstStartIsNotAFailure pins the distinction that
// makes doctor usable as an install gate: immediately after `apt install` the
// session key does not exist yet, and calling that a failure would make every
// correct install fail its own verification.
func TestSessionKeyAbsentBeforeFirstStartIsNotAFailure(t *testing.T) {
	now := time.Now()

	// Fresh install: no key, and no store — the daemon has never run.
	env := healthyEnv(now)
	env.Store = nil
	env.Stat = statFS(map[string]fakeInfo{
		"/etc/pve": {mode: 0o755 | fs.ModeDir, isDir: true},
	})

	rep := Run(context.Background(), healthyFacts(), env)
	got := find(t, rep, CheckKeyFiles)
	if got.Status != StatusWarn {
		t.Errorf("key_files = %s (%s); a missing session key before first start is expected, not a failure", got.Status, got.Detail)
	}
	if rep.Failed() {
		t.Error("a freshly installed, never-started node reports Failed(); install.sh could never gate on doctor")
	}

	// Control: the same missing key *after* the daemon has run is a real
	// failure. Without this, the case above could be passing because the check
	// stopped discriminating entirely.
	env.Store = fakeStore{current: 42, latest: 42}
	after := find(t, Run(context.Background(), healthyFacts(), env), CheckKeyFiles)
	if after.Status != StatusFail {
		t.Errorf("key_files = %s after the daemon has run; a deleted session key must fail", after.Status)
	}
}
