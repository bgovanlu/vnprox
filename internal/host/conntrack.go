// SPDX-License-Identifier: Apache-2.0

// conntrack.go implements T-1305's live conntrack/NAT table read:
// Reader.Conntrack (docs/architecture.md §7's "app-owned data only" rule
// does not apply here — this is a live, ephemeral kernel read, never
// persisted). The real, per-node collection now reads the netlink
// conntrack socket by default (netlink_linux.go's Real.Conntrack,
// T-3711) — PVE 9 kernels ship CONFIG_NF_CONNTRACK_PROCFS=n, so
// /proc/net/nf_conntrack does not exist there even though the module is
// loaded and netlink works fine; ParseConntrackTable below now backs only
// a secondary, explicitly-opted-into text-format path (Real.ConntrackPath,
// see its doc comment) — an operator override or a test fixture, never the
// default. It remains pure and platform-independent (no build tag),
// following T-1004's internal/flow/hostsample.ParseConntrackTable
// precedent for the general shape of the format and its "skip and count,
// never fail the whole read" tolerance — but unlike that sampler (which
// only needs the diffable packets/bytes counters), this parser recovers
// the fields the conntrack explorer actually shows: connection state,
// remaining timeout, and NAT translation.

package host

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// DefaultConntrackPath is the standard Linux procfs conntrack table —
// present whenever the nf_conntrack kernel module is loaded AND the
// running kernel was built with CONFIG_NF_CONNTRACK_PROCFS (PVE 9 kernels
// are not — T-3711), mirroring internal/flow/hostsample.DefaultConntrackPath
// (that package's own copy of the same constant — see this file's doc
// comment on why the two packages each parse the same text format for
// different purposes rather than sharing one reader). No longer read by
// default — see Real.ConntrackPath's doc comment.
const DefaultConntrackPath = "/proc/net/nf_conntrack"

// ErrConntrackUnavailable indicates the netlink conntrack interface itself
// is not usable on this node: refused for lack of CAP_NET_ADMIN (see
// ErrConntrackPermissionDenied, which additionally wraps this for that
// specific cause) or the kernel has no nf_conntrack netlink support at all
// (module not loaded, or built without conntrack netlink support).
// Real.Conntrack (netlink_linux.go, T-3711) wraps this so callers can
// errors.Is-check it without string-matching, mirroring
// ErrCorosyncUnavailable/ErrOVSUnavailable/ErrLLDPUnavailable — and so
// hostsample.ConntrackSampler.Run can report it once and stop polling
// rather than retrying forever, and internal/api/conntrack.go can surface
// "this node cannot provide conntrack" distinctly from an ordinary read
// failure.
var ErrConntrackUnavailable = errors.New("host: conntrack netlink interface unavailable")

// ErrConntrackPermissionDenied indicates the netlink conntrack dump was
// refused specifically for lack of CAP_NET_ADMIN (T-3711) — distinguished
// from ErrConntrackUnavailable's other causes because the operator fix
// differs: grant the capability (packaging/systemd/vnprox.service already
// does for a packaged install; this case is expected only for an
// unprivileged dev/test run or a future non-root deployment, T-604), not
// "conntrack genuinely isn't available on this node." Always wrapped
// together with ErrConntrackUnavailable, never alone, so a caller that
// only checks the broader sentinel still catches this case.
var ErrConntrackPermissionDenied = errors.New("host: conntrack netlink read requires CAP_NET_ADMIN")

// NatAddr is one NAT-translated endpoint — the address/port a connection's
// source or destination was actually rewritten to, recovered by comparing
// a conntrack line's "original" and "reply" direction tuples (see
// parseConntrackLine's doc comment for the exact detection logic). Nil on
// ConntrackEntry.NatSrc/NatDst when no translation was detected for that
// side.
type NatAddr struct {
	IP   string
	Port int
}

// ConntrackEntry is one live conntrack table connection (T-1305's
// docs/api.md Conntrack section): the "original" direction's src/dst
// tuple, the kernel's own state/timeout for the connection, and — when the
// connection is NAT'd — the translated source and/or destination address.
type ConntrackEntry struct {
	NatSrc     *NatAddr
	NatDst     *NatAddr
	SrcIP      string
	DstIP      string
	State      string
	Proto      int
	SrcPort    int
	DstPort    int
	TimeoutSec int
}

// ParseConntrackTable parses a whole /proc/net/nf_conntrack-format table
// (one connection per line) into entries, returning the count of lines
// skipped as unparsable — the same "skip and count, never fail the whole
// read" convention internal/flow/hostsample.ParseConntrackTable and
// internal/host.ParseDHCPLeases already establish. Blank lines are also
// counted as skipped.
func ParseConntrackTable(data []byte) (entries []ConntrackEntry, skipped int) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			skipped++
			continue
		}
		entry, ok := parseConntrackLine(line)
		if !ok {
			skipped++
			continue
		}
		entries = append(entries, entry)
	}
	return entries, skipped
}

// conntrackTuple is one direction's src/dst/sport/dport, as recorded on one
// nf_conntrack line (a line always carries two: "original", as the
// connection was first seen, and "reply", the expected return-direction
// tuple — see parseConntrackLine).
type conntrackTuple struct {
	src, dst     string
	sport, dport int
}

// parseConntrackLine parses one non-empty, trimmed nf_conntrack line. The
// kernel's own format is positional for its leading fields (family, family
// number, proto name, proto number, timeout, [tcp-only state]) but the
// remaining fields are unordered "key=value" tokens, repeated once for the
// original direction and once for the reply direction (icmp has no
// sport/dport at all; UNREPLIED/ASSURED appear as bare "[...]" flags rather
// than key=value pairs — see conntrack_basic.txt-style fixtures). This
// parser scans every non-bracket token for key=value pairs, starting a new
// "direction" the second time it sees src/dst/sport/dport — the exact
// point the kernel's own original-then-reply pair boundary falls at — so it
// recovers both tuples rather than discarding the second the way
// hostsample's diff-only parser does.
//
// NAT detection: for an untranslated connection the reply tuple is exactly
// the original's mirror (reply.src==original.dst, reply.dst==original.src,
// ports likewise) — that is what "the kernel expects a reply" means absent
// any address/port rewriting. When SNAT is in effect, the reply tuple's
// dst is the *translated* source (what the outside world actually sees as
// this connection's source) rather than original.src, so NatSrc is set
// from reply.dst/reply.dport whenever they diverge from original.src/
// original.sport. Symmetrically, when DNAT is in effect, the reply tuple's
// src is the connection's *real* destination (the backend a virtual/public
// destination was translated to) rather than a mirror of original.dst, so
// NatDst is set from reply.src/reply.sport whenever they diverge from
// original.dst/original.dport. A line with no reply tuple recovered at all
// (some non-standard/truncated input) never reports NAT — there is nothing
// to compare against.
func parseConntrackLine(line string) (ConntrackEntry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return ConntrackEntry{}, false
	}
	proto, err := strconv.Atoi(fields[3])
	if err != nil {
		return ConntrackEntry{}, false
	}
	timeoutSec, err := strconv.Atoi(fields[4])
	if err != nil {
		return ConntrackEntry{}, false
	}

	idx := 5
	state := ""
	if idx < len(fields) && !strings.Contains(fields[idx], "=") && !strings.HasPrefix(fields[idx], "[") {
		state = fields[idx]
		idx++
	}

	var dirs [2]conntrackTuple
	dirIdx := 0
	seen := map[string]bool{}
	bracket := ""

	for _, tok := range fields[idx:] {
		if strings.HasPrefix(tok, "[") {
			if bracket == "" {
				bracket = strings.Trim(tok, "[]")
			}
			continue
		}
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch k {
		case "src", "dst", "sport", "dport":
			if seen[k] && dirIdx == 0 {
				dirIdx = 1
				seen = map[string]bool{}
			}
			if dirIdx > 1 {
				continue
			}
			seen[k] = true
			switch k {
			case "src":
				dirs[dirIdx].src = v
			case "dst":
				dirs[dirIdx].dst = v
			case "sport":
				n, perr := strconv.Atoi(v)
				if perr != nil {
					return ConntrackEntry{}, false
				}
				dirs[dirIdx].sport = n
			case "dport":
				n, perr := strconv.Atoi(v)
				if perr != nil {
					return ConntrackEntry{}, false
				}
				dirs[dirIdx].dport = n
			}
		default:
			// packets/bytes/mark/zone/use/type/code/id/secctx/... — not
			// needed for this parser's output.
		}
	}

	if dirs[0].src == "" || dirs[0].dst == "" {
		return ConntrackEntry{}, false
	}

	e := ConntrackEntry{
		Proto: proto, SrcIP: dirs[0].src, DstIP: dirs[0].dst,
		SrcPort: dirs[0].sport, DstPort: dirs[0].dport,
		State: state, TimeoutSec: timeoutSec,
	}
	if e.State == "" && bracket != "" {
		e.State = bracket
	}

	if dirs[1].src != "" && dirs[1].dst != "" {
		if dirs[1].dst != dirs[0].src || dirs[1].dport != dirs[0].sport {
			e.NatSrc = &NatAddr{IP: dirs[1].dst, Port: dirs[1].dport}
		}
		if dirs[1].src != dirs[0].dst || dirs[1].sport != dirs[0].dport {
			e.NatDst = &NatAddr{IP: dirs[1].src, Port: dirs[1].sport}
		}
	}

	return e, true
}
