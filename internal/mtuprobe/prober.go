// SPDX-License-Identifier: Apache-2.0

package mtuprobe

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// Prober issues one tick's MTU probe attempt for a path and reports the
// discovered mtu. Target is latmesh.Pair — this package's path identity is
// the exact same type latmesh.Discoverer already produces (doc.go's "reuses
// T-1303's infrastructure" section), never a second, parallel target shape.
// A non-nil error means the probe could not even be attempted for this
// path this tick (see BinarySearchMTU's ProbeFunc doc comment for the same
// honesty-first distinction); mtu==0 with a nil error is an honest "probed,
// but even the floor MTU failed" result.
type Prober interface {
	ProbeMTU(ctx context.Context, target latmesh.Pair) (mtu int, probeCount int, err error)
}

// DefaultProbeTimeout bounds one DF-probe attempt.
const DefaultProbeTimeout = 2 * time.Second

// ipv4Overhead is the IPv4 header (20 bytes) + ICMP echo header (8 bytes)
// this package's RealProber must subtract from a candidate path-MTU size to
// get the `ping -s` payload-size argument (which is the ICMP data size, not
// the resulting on-wire packet size) — see RealProber.ProbeMTU.
const ipv4Overhead = 28

// RealProber issues real DF-set (Don't Fragment) ICMP probes host-to-host
// via the system `ping` binary's `-M do` flag — this package's actual
// production Prober, run directly by the vnproxd process on each node
// (no guest-agent indirection, same as internal/latmesh.RealProber).
//
// NEEDS HARDWARE VALIDATION (planning/reports/needs-hardware-validation.md
// carries the tracking entry, per CLAUDE.md and this task's card): like
// internal/latmesh.RealProber, this always runs against the host OS
// vnproxd itself is packaged for (Debian/PVE, iputils-ping) so `-M do`'s
// exact availability/output-on-drop shape is iputils-ping's own
// well-documented, version-stable behavior — flagged because this task has
// no live PVE cluster to confirm against. dfProbe's defensive stance (an
// unparsable/ambiguous ping outcome reports ok=false, never panics) is the
// same honesty-first fallback internal/latmesh.parsePingSummary already
// established for a comparable situation.
type RealProber struct {
	// Timeout overrides DefaultProbeTimeout when > 0.
	Timeout time.Duration
}

// ProbeMTU runs BinarySearchMTU against pair's target address (falling back
// to pair.ToNode by name when ToAddr is unset, matching latmesh.Pair's own
// documented convention), using dfProbe as the per-size ProbeFunc.
func (p RealProber) ProbeMTU(ctx context.Context, pair latmesh.Pair) (mtu int, probeCount int, err error) {
	target := pair.ToAddr
	if target == "" {
		target = pair.ToNode
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}

	probe := func(size int) (bool, error) {
		return dfProbe(ctx, target, size, timeout)
	}
	return BinarySearchMTU(probe, MinMTU, MaxMTU)
}

// dfProbe sends one `ping -M do -c 1 -s <payload> -W <timeoutSecs> target`
// and reports whether that exact path-MTU size got through. size is the
// full path MTU under test; the ICMP payload argument ping wants is
// size-ipv4Overhead. A size too small to carry even the ICMP/IP headers
// (size <= ipv4Overhead) can never succeed and is reported ok=false without
// shelling out at all.
func dfProbe(ctx context.Context, target string, size int, timeout time.Duration) (bool, error) {
	payload := size - ipv4Overhead
	if payload <= 0 {
		return false, nil
	}
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}

	// "--" ends option parsing (T-2905): without it a target beginning with
	// "-" is parsed by ping as an option — root running `ping -f` on
	// attacker-influenced input is exactly the class internal/latmesh's
	// sibling prober already guards against with the same two bytes. Targets
	// are cluster-derived today, but PUT /wan/targets feeds the same Pair
	// type one wiring change away, so the guard is defense in depth, not
	// decoration.
	cmd := exec.CommandContext(ctx, "ping", "-M", "do", "-c", "1", "-W", strconv.Itoa(secs), "-s", strconv.Itoa(payload), "--", target) //nolint:gosec // fixed argv shape with end-of-options guard; target can never be parsed as an option
	out, err := cmd.CombinedOutput()
	text := string(out)

	// "Frag needed"/"Message too long" are iputils-ping's own DF-drop
	// reports (ICMP type 3 code 4 either locally rejected by the sending
	// stack or reflected back from an intermediate hop) — an honest
	// "too big for this path" negative result, not a transport failure.
	if strings.Contains(text, "Frag needed") || strings.Contains(text, "Message too long") ||
		strings.Contains(text, "Local error") {
		return false, nil
	}
	if err != nil {
		if len(out) == 0 {
			// Real exec-level failure (binary missing, permission denied) —
			// nothing to parse at all, distinct from a network-level outcome.
			return false, fmt.Errorf("mtuprobe: running DF ping toward %s at size %d: %w", target, size, err)
		}
		// A non-zero exit with output (e.g. "100% packet loss", unrelated to
		// fragmentation) — treat as an honest "didn't get through this size",
		// never a hard error.
		return false, nil
	}
	if strings.Contains(text, "1 received") || strings.Contains(text, "0% packet loss") {
		return true, nil
	}
	return false, nil
}
