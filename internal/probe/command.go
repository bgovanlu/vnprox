package probe

import (
	"strconv"
	"time"
)

// buildCommand renders the in-guest argv Run execs via the QEMU guest agent
// for one probe attempt.
//
// NEEDS HARDWARE VALIDATION (flagged per CLAUDE.md and this task's card —
// planning/reports/needs-hardware-validation.md carries the tracking entry):
// there is no portable, universally-installed command for "ping once" /
// "attempt a TCP connect" across every guest OS/toolchain a real PVE cluster
// might run (Debian/Ubuntu cloud images, minimal containers-turned-VMs,
// Windows, BSD, ...) — busybox images may lack `ping`'s `-W` flag or `nc`
// entirely; Windows guests need an entirely different command
// (Test-NetConnection / ping.exe with different flag spelling) not
// implemented at all here. Rather than silently guessing a "portable"
// command and asserting it works everywhere, this function deliberately
// implements exactly one target profile — a Linux guest with iputils-ping
// and a netcat variant supporting `-z`/`-w` (Debian/Ubuntu's default
// netcat-openbsd, matching PVE's own most common guest OS family) — and
// callers/operators must treat any other guest OS as unverified until
// tested against real hardware. unsupportedProto below is the honest
// "we don't know" fallback for anything this function wasn't built for.
//
// pvemock's handleGuestAgentExec (internal/pvemock/guest.go) parses exactly
// the two argv shapes emitted here to route a scripted fixture outcome —
// keep both in sync if this function's shape ever changes.
func buildCommand(proto, dstIP string, port int, timeout time.Duration) ([]string, bool) {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	switch proto {
	case ProtoICMP:
		// ping -c 1 -W <timeout-seconds> <ip>: one echo request, bounded
		// wait. Exit 0 = reply received, exit 1 = no reply (real
		// iputils-ping's documented exit codes), exit 2 = other error
		// (bad address, no route at all).
		return []string{"ping", "-c", "1", "-W", strconv.Itoa(secs), dstIP}, true
	case ProtoTCP:
		// nc -z -w <timeout-seconds> <ip> <port>: zero-I/O connect-only
		// scan. Exit 0 = connected (port open), non-zero = refused/
		// filtered/timed out — netcat-openbsd does not distinguish these
		// in its exit code alone, only in stderr text (best-effort parsed
		// by classify, see probe.go).
		return []string{"nc", "-z", "-w", strconv.Itoa(secs), dstIP, strconv.Itoa(port)}, true
	default:
		return nil, false
	}
}
