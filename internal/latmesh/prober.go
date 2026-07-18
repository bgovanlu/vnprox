package latmesh

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Prober issues one tick's probe attempt for a Pair and reports the
// resulting Reading. Scheduler calls this once per pair per tick; a nil
// error with Reading.LossPct == 100 is a normal "no reply" outcome, not a
// failure — Probe should only return a non-nil error for a condition the
// probe itself could not even attempt to classify (see RealProber's doc
// comment).
type Prober interface {
	Probe(ctx context.Context, p Pair) (Reading, error)
}

// DefaultProbeCount is how many echoes RealProber sends per tick — a small
// burst (not a single ping) is what lets one tick's Reading carry a
// meaningful LossPct rather than only ever reading 0% or 100%.
const DefaultProbeCount = 5

// DefaultProbeTimeout bounds one tick's whole burst.
const DefaultProbeTimeout = 3 * time.Second

// RealProber issues real ICMP probes host-to-host via the system `ping`
// binary — this package's actual production Prober, run directly by the
// vnproxd process on each node (no guest-agent indirection at all, unlike
// internal/probe.Run — see doc.go).
//
// NEEDS HARDWARE VALIDATION (planning/reports/needs-hardware-validation.md
// carries the tracking entry, per CLAUDE.md and this task's card): unlike
// internal/probe's guest-exec probing (which has to guess at an unknown
// guest OS/toolchain), this probe always runs against the *host* OS vnproxd
// itself is packaged for — a Debian/PVE node with iputils-ping, per
// docs/development.md's packaging target — so the summary-line format
// parsePingSummary expects (`rtt min/avg/max/mdev = ...` and `N received,
// P% packet loss`) is iputils-ping's own well-documented, version-stable
// output. It is still flagged because this task has no live PVE cluster to
// confirm against: a genuinely different ping build on some future package
// target (e.g. a musl-based minimal host image) could format either line
// differently, and parsePingSummary's defensive stance (an unparsable
// summary reports 100% loss, RealProber never panics on it) is exactly the
// same honesty-first fallback internal/host.ParseCorosyncStatus already
// established for a comparable "exact wording isn't guaranteed" situation.
type RealProber struct {
	// Count overrides DefaultProbeCount when > 0.
	Count int
	// Timeout overrides DefaultProbeTimeout when > 0.
	Timeout time.Duration
}

// Probe execs `ping -c <count> -W <timeout-seconds> <target>` against
// p.ToAddr (falling back to p.ToNode by name when ToAddr is unset — see
// Pair's doc comment) and parses iputils-ping's own summary line. A ping
// invocation that fails to even start (binary missing, permission denied —
// exec-level failures, not network-level ones) returns a non-nil error;
// every other outcome (partial loss, total loss, full success) is reported
// as a Reading with a nil error, per this package's honesty-contract
// convention (a "no reply" tick is data, not a failure).
func (p RealProber) Probe(ctx context.Context, pair Pair) (Reading, error) {
	target := pair.ToAddr
	if target == "" {
		target = pair.ToNode
	}
	count := p.Count
	if count <= 0 {
		count = DefaultProbeCount
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}

	cmd := exec.CommandContext(ctx, "ping", "-c", strconv.Itoa(count), "-W", strconv.Itoa(secs), target) //nolint:gosec // fixed argv shape, target is a cluster-known node address/name, not external user input
	out, err := cmd.CombinedOutput()
	if err != nil {
		// ping's own exit code for "some/all packets lost" is non-zero —
		// that is NOT an exec failure, it's the very outcome this probe
		// exists to observe, so a non-zero exit alone is not treated as an
		// error here; only distinguish a real exec-level failure (binary
		// not found) that produced no parseable output at all.
		if len(out) == 0 {
			return Reading{}, fmt.Errorf("latmesh: running ping toward %s: %w", target, err)
		}
	}
	return parsePingSummary(out), nil
}

var (
	packetLossRe = regexp.MustCompile(`(\d+(?:\.\d+)?)%\s+packet loss`)
	rttAvgRe     = regexp.MustCompile(`=\s*[\d.]+/([\d.]+)/[\d.]+`)
)

// parsePingSummary defensively extracts loss% and average RTT from
// iputils-ping's summary output (see RealProber's doc comment for the exact
// lines expected). Never panics: any line it can't parse is skipped, and a
// summary with no recognizable loss line at all reports 100% loss (the
// same "can't confirm healthy -> treat as worth flagging" default
// host.ParseCorosyncStatus's Faulty field uses) rather than a
// misleadingly-cheerful 0%.
func parsePingSummary(out []byte) Reading {
	loss := 100.0
	lossFound := false
	var rtt float64

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		if m := packetLossRe.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				loss = v
				lossFound = true
			}
		}
		if m := rttAvgRe.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				rtt = v
			}
		}
	}
	if !lossFound {
		return Reading{LossPct: 100}
	}
	return Reading{RttMs: rtt, LossPct: loss}
}
