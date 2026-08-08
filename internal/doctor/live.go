package doctor

import (
	"context"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
)

// LiveChecks are exactly the checks that cannot be answered from the CLI
// alone, because each needs a credential or a client only the running daemon
// holds: an authenticated PVE token, the peer HMAC secret, and a reference
// clock reachable through the first of those.
//
// T-1904 shipped all four implemented and tested, reporting `skip` from the
// CLI with a reason. That was honest but weak: six-of-ten green is a weaker
// signal than it looks, and `skip` is not `pass`. T-2406 (this file) closes
// that by letting the daemon run them and hand back the verdicts.
var LiveChecks = []string{
	CheckPVEReachable,
	CheckPVEPrivileges,
	CheckPeerSecret,
	CheckClockSkew,
}

// IsLiveCheck reports whether a check's verdict requires the daemon.
func IsLiveCheck(name string) bool {
	for _, c := range LiveChecks {
		if c == name {
			return true
		}
	}
	return false
}

// RunLive executes only the daemon-dependent checks and returns their results
// in LiveChecks order.
//
// It is deliberately a separate entry point rather than a flag on Run: the
// daemon has no business re-checking file permissions or port conflicts on
// behalf of a CLI that is standing on the same machine and can see them
// itself, and mixing the two would blur which half of a report came from
// where.
func RunLive(ctx context.Context, facts Facts, env Env) []Result {
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}
	return []Result{
		checkPVEReachable(ctx, facts, env),
		checkPVEPrivileges(ctx, facts, env),
		checkPeerSecret(ctx, facts, env),
		checkClockSkew(ctx, facts, env, now()),
	}
}

// MergeLive replaces each local result whose check appears in live, and
// returns a fresh report with a recomputed summary.
//
// Two rules, both of which exist so a `--live` run cannot end up LESS truthful
// than a plain one:
//
//   - Only LiveChecks may be replaced. A daemon response naming any other
//     check is ignored rather than trusted — the CLI observed those itself,
//     locally, and a remote answer about the local filesystem is not better
//     information.
//   - A live result that is itself a `skip` still replaces the local skip,
//     because the daemon's reason ("the token lacks Sys.Audit") is strictly
//     more specific than the CLI's ("this needs the daemon").
func MergeLive(local Report, live []Result) Report {
	byCheck := make(map[string]Result, len(live))
	for _, r := range live {
		if !IsLiveCheck(r.Check) {
			continue
		}
		byCheck[r.Check] = r
	}

	out := local
	out.Results = make([]Result, len(local.Results))
	copy(out.Results, local.Results)
	for i, r := range out.Results {
		if replacement, ok := byCheck[r.Check]; ok {
			out.Results[i] = replacement
		}
	}
	out.Summary = summarize(out.Results)
	return out
}

// UnreachableDaemonResults returns the `skip` verdicts to use when --live was
// asked for and the daemon could not be reached.
//
// SKIP, NEVER FAIL. A daemon that is down does not mean PVE is unreachable or
// that the clock is wrong — reporting `fail` here would blame the wrong thing,
// and an operator acting on it would go and look at PVE while the actual
// problem is a stopped service. The reason names the daemon so the next step
// is obvious.
func UnreachableDaemonResults(reason string) []Result {
	if reason == "" {
		reason = "the vnprox daemon could not be reached"
	}
	detail := "not checked: " + reason + ". This says nothing about PVE, the peer secret, or the clock — only that the daemon that would check them is not answering"
	out := make([]Result, 0, len(LiveChecks))
	for _, c := range LiveChecks {
		out = append(out, skip(c, detail))
	}
	return out
}

// RequiredPrivilegeNamesForTest exposes EVERY privilege name
// checkPVEPrivileges knows about — required and optional — so a test can build
// a genuinely complete probe without restating the list. A second copy would
// drift, and the test would then assert against the wrong set: the exact
// failure checkPVEPrivileges' own doc comment warns about.
//
// Optional ones are included because omitting them produces a `warn`, not a
// `pass`, and a test that wanted "everything is fine" would otherwise be
// asserting the wrong verdict.
func RequiredPrivilegeNamesForTest() []string {
	privs := auth.RequiredPrivileges()
	out := make([]string, 0, len(privs))
	for _, p := range privs {
		out = append(out, p.Name)
	}
	return out
}
