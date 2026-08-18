package doctor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// liveProbe is a PVEProbe whose behaviour a test dictates — the "deliberately
// broken fixture" shape T-1904 required of every check, applied to the live
// path.
type liveProbe struct {
	serverTime time.Time
	pingErr    error
	privsErr   error
	privs      []string
}

func (p liveProbe) Ping(context.Context) (time.Time, error) {
	return p.serverTime, p.pingErr
}

func (p liveProbe) Privileges(context.Context) ([]string, error) {
	return p.privs, p.privsErr
}

func resultFor(results []Result, check string) (Result, bool) {
	for _, r := range results {
		if r.Check == check {
			return r, true
		}
	}
	return Result{}, false
}

// AC1's shape: with a live probe wired, the checks return a real verdict
// rather than `skip`.
func TestRunLive_ProducesRealVerdictsWithAProbe(t *testing.T) {
	privs := make([]string, 0, len(DaemonTokenPrivilegeNamesForTest()))
	privs = append(privs, DaemonTokenPrivilegeNamesForTest()...)
	results := RunLive(context.Background(), Facts{PVEAPIURL: "https://pve1:8006"}, Env{
		PVE: liveProbe{privs: privs},
	})

	reach, ok := resultFor(results, CheckPVEReachable)
	if !ok {
		t.Fatal("pve_reachable missing from the live results")
	}
	if reach.Status != StatusPass {
		t.Fatalf("pve_reachable = %s (%s), want pass", reach.Status, reach.Detail)
	}
	privResult, ok := resultFor(results, CheckPVEPrivileges)
	if !ok {
		t.Fatal("pve_privileges missing from the live results")
	}
	if privResult.Status != StatusPass {
		t.Fatalf("pve_privileges = %s (%s), want pass", privResult.Status, privResult.Detail)
	}
}

// AC3: each newly-wired check has a broken fixture proving it can FAIL. A
// check that can only pass is a decoration.
func TestRunLive_EachCheckCanFail(t *testing.T) {
	t.Run("pve_reachable fails when the API does not answer", func(t *testing.T) {
		results := RunLive(context.Background(), Facts{PVEAPIURL: "https://pve1:8006", PVETokenFile: "/etc/vnprox/keys/pve-token"}, Env{
			PVE: liveProbe{pingErr: errors.New("connection refused")},
		})
		r, _ := resultFor(results, CheckPVEReachable)
		if r.Status != StatusFail {
			t.Fatalf("status = %s, want fail", r.Status)
		}
		if r.Remediation == "" {
			t.Fatal("a fail with no remediation")
		}
	})

	t.Run("pve_privileges fails when a required privilege is missing", func(t *testing.T) {
		results := RunLive(context.Background(), Facts{}, Env{
			PVE: liveProbe{privs: []string{"VM.Audit"}}, // deliberately incomplete
		})
		r, _ := resultFor(results, CheckPVEPrivileges)
		if r.Status != StatusFail {
			t.Fatalf("status = %s (%s), want fail", r.Status, r.Detail)
		}
		if r.Remediation == "" {
			t.Fatal("a fail with no remediation")
		}
	})
}

// The whole point of the feature: without a probe these skip, with one they do
// not. Asserted as a pair so "it always skips" and "it always passes" are both
// caught.
func TestRunLive_SkipsWithoutAProbeAndNotWithOne(t *testing.T) {
	without := RunLive(context.Background(), Facts{}, Env{})
	for _, c := range []string{CheckPVEReachable, CheckPVEPrivileges} {
		r, _ := resultFor(without, c)
		if r.Status != StatusSkip {
			t.Fatalf("%s without a probe = %s, want skip", c, r.Status)
		}
	}
	with := RunLive(context.Background(), Facts{}, Env{PVE: liveProbe{privs: DaemonTokenPrivilegeNamesForTest()}})
	for _, c := range []string{CheckPVEReachable, CheckPVEPrivileges} {
		r, _ := resultFor(with, c)
		if r.Status == StatusSkip {
			t.Fatalf("%s WITH a probe still skipped: %s", c, r.Detail)
		}
	}
}

// AC2: a daemon that cannot be reached yields SKIP, never FAIL. Reporting
// failure would blame PVE, the peer secret, or the clock for a stopped
// service, and send the operator to look at the wrong thing.
func TestUnreachableDaemonResults_AreSkipsNamingTheDaemon(t *testing.T) {
	results := UnreachableDaemonResults("connection refused on :8007")
	if len(results) != len(LiveChecks) {
		t.Fatalf("got %d results, want one per live check (%d)", len(results), len(LiveChecks))
	}
	for _, r := range results {
		if r.Status != StatusSkip {
			t.Fatalf("%s = %s, want skip — a stopped daemon is not a PVE failure", r.Check, r.Status)
		}
		if r.Detail == "" {
			t.Fatalf("%s skipped with no reason", r.Check)
		}
		// The reason must be actionable: it names what was not reached.
		if !containsSubstr(r.Detail, "connection refused on :8007") {
			t.Fatalf("%s does not carry the caller's reason: %q", r.Check, r.Detail)
		}
	}
	// And an empty reason still produces something legible rather than a
	// dangling sentence.
	for _, r := range UnreachableDaemonResults("") {
		if r.Detail == "" {
			t.Fatalf("%s skipped with an empty detail", r.Check)
		}
	}
}

// MergeLive replaces only live checks. A daemon response naming a LOCAL check
// must be ignored: the CLI observed those itself on this machine, and a remote
// answer about the local filesystem is not better information.
func TestMergeLive_ReplacesOnlyLiveChecks(t *testing.T) {
	local := Report{Results: []Result{
		pass(CheckKeyFiles, "key files look right"),
		skip(CheckPVEReachable, "needs the daemon"),
		skip(CheckPVEPrivileges, "needs the daemon"),
	}}
	local.Summary = summarize(local.Results)

	merged := MergeLive(local, []Result{
		pass(CheckPVEReachable, "PVE API reachable"),
		// A hostile or buggy daemon claiming the local key files are broken.
		fail(CheckKeyFiles, "your keys are wrong", "do something"),
	})

	r, _ := resultFor(merged.Results, CheckPVEReachable)
	if r.Status != StatusPass {
		t.Fatalf("the live result did not replace the local skip: %s", r.Status)
	}
	k, _ := resultFor(merged.Results, CheckKeyFiles)
	if k.Status != StatusPass {
		t.Fatalf("a remote answer overrode a LOCAL check: %s (%s)", k.Status, k.Detail)
	}
	// The untouched live check keeps its skip.
	p, _ := resultFor(merged.Results, CheckPVEPrivileges)
	if p.Status != StatusSkip {
		t.Fatalf("pve_privileges = %s, want the untouched skip", p.Status)
	}
	// And the summary is recomputed, not carried over stale.
	if merged.Summary.Pass != 2 || merged.Summary.Skip != 1 {
		t.Fatalf("summary = %+v, want 2 pass / 1 skip", merged.Summary)
	}
}

// MergeLive must not mutate the caller's report — the CLI prints the local one
// on the failure path.
func TestMergeLive_DoesNotMutateTheLocalReport(t *testing.T) {
	local := Report{Results: []Result{skip(CheckPVEReachable, "needs the daemon")}}
	local.Summary = summarize(local.Results)

	_ = MergeLive(local, []Result{pass(CheckPVEReachable, "reachable")})
	if local.Results[0].Status != StatusSkip {
		t.Fatal("MergeLive mutated the caller's report")
	}
}

// A merged report must still satisfy the structural rule T-1904 established:
// a fail or warn with no remediation is a malformed report.
func TestMergeLive_ResultStillValidates(t *testing.T) {
	local := Report{
		GeneratedAt: time.Unix(1, 0),
		Version:     "test",
		Results:     []Result{skip(CheckPVEReachable, "needs the daemon")},
	}
	local.Summary = summarize(local.Results)

	merged := MergeLive(local, []Result{fail(CheckPVEReachable, "cannot reach PVE", "start pveproxy")})
	if err := merged.Validate(); err != nil {
		t.Fatalf("a merged report must validate: %v", err)
	}
}

func TestIsLiveCheck(t *testing.T) {
	if !IsLiveCheck(CheckPVEReachable) {
		t.Error("pve_reachable should be a live check")
	}
	if IsLiveCheck(CheckKeyFiles) {
		t.Error("key_files is observed locally and must not be a live check")
	}
}

func containsSubstr(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
