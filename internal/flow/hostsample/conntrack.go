// SPDX-License-Identifier: Apache-2.0

package hostsample

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// DefaultConntrackPath is the standard Linux procfs conntrack table —
// present whenever the nf_conntrack kernel module is loaded AND the
// running kernel was built with CONFIG_NF_CONNTRACK_PROCFS (PVE 9 kernels
// are not — T-3711). No longer read by default; see
// NewNetlinkConntrackReader/NewFileConntrackReader below.
const DefaultConntrackPath = "/proc/net/nf_conntrack"

// DefaultHostSampleInterval is [flows] host_sample_interval_sec's default
// (10s) — shared by both samplers in this package, per T-1004's task card.
const DefaultHostSampleInterval = 10 * time.Second

// ConntrackReader is the seam ConntrackSampler polls through: the
// production reader (NewNetlinkConntrackReader, conntrack_netlink_linux.go,
// T-3711) dumps the live netlink conntrack table directly; the
// text-format reader (realConntrackReader, NewFileConntrackReader) parses
// a /proc/net/nf_conntrack-format file — used by tests, and available as a
// secondary operator override for an unusual deployment (e.g. a conntrack
// table exposed at a non-default text path inside a container) — never
// what cmd/vnproxd wires in by default.
type ConntrackReader interface {
	// ReadEntries returns the current poll's parsed connections plus the
	// count of malformed/unparsable input skipped along the way (always 0
	// for the netlink reader, since there is no free-text to skip — that
	// count only has meaning for realConntrackReader's text path).
	ReadEntries(ctx context.Context) (entries []ConntrackEntry, skipped int, err error)
}

// realConntrackReader reads path fresh on every call — /proc files have no
// meaningful "stale read" concern (the kernel regenerates the view on each
// open/read), so there is nothing to cache here.
type realConntrackReader struct {
	path string
}

// NewFileConntrackReader builds a ConntrackReader over an arbitrary
// text-format (/proc/net/nf_conntrack-shaped) path — used by tests to
// point at a fixture file, and available for an operator to override in an
// unusual deployment (e.g. a conntrack table exposed at a non-default text
// path inside a container, or a kernel that genuinely still has
// CONFIG_NF_CONNTRACK_PROCFS). This is a secondary path: cmd/vnproxd wires
// in NewNetlinkConntrackReader by default (T-3711) — see ConntrackReader's
// doc comment.
func NewFileConntrackReader(path string) ConntrackReader {
	return realConntrackReader{path: path}
}

func (r realConntrackReader) ReadEntries(_ context.Context) ([]ConntrackEntry, int, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil, 0, fmt.Errorf("hostsample: reading conntrack table %s: %w", r.path, err)
	}
	entries, skipped := ParseConntrackTable(data)
	return entries, skipped, nil
}

// ConntrackEntry is one decoded /proc/net/nf_conntrack line's "original
// direction" tuple and cumulative counters (the kernel prints original and
// reply direction tuples/counters on every line; this package only ever
// reads the first — original — occurrence of each key, matching the
// convention a simple accounting collector would use: the reply direction
// is the same conversation's other half, already implied by the pair, not
// a second flow).
type ConntrackEntry struct {
	SrcIP   string
	DstIP   string
	Proto   int
	SrcPort int
	DstPort int
	// Packets/Bytes are the kernel's own cumulative counters for this
	// connection since it was created — only present when
	// net.netfilter.nf_conntrack_acct=1 (accounting) is enabled; zero
	// otherwise, which is a valid (if unhelpful) reading, not malformed.
	Packets int64
	Bytes   int64
}

// key is the diff/dedup key ConntrackSampler tracks a connection's last-
// seen counters under.
func (e ConntrackEntry) key() string {
	return fmt.Sprintf("%d|%s|%d|%s|%d", e.Proto, e.SrcIP, e.SrcPort, e.DstIP, e.DstPort)
}

// ParseConntrackTable parses a whole /proc/net/nf_conntrack-format table
// (one connection per line) into entries, returning the count of lines
// skipped as unparsable (mirrors internal/fwlog.ParseAll's Result/
// "skip and count, never fail the whole read" convention — a garbage line
// here is simply one this sampler cannot attribute a src/dst tuple to, not
// a fatal read error). Blank lines are also counted as skipped, matching
// internal/fwlog.ParseAll's exact treatment.
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

// parseConntrackLine parses one non-empty, trimmed nf_conntrack line. The
// kernel's own format is positional for its leading fields (family, family
// number, proto name, proto number, timeout, [tcp-only state]) but the
// remaining fields are unordered "key=value" tokens repeated once for the
// original direction and once for the reply direction (icmp has no
// sport/dport at all) — rather than track every protocol's exact field
// count, this parser scans every token for key=value pairs and keeps only
// the FIRST occurrence of each key of interest (the original direction,
// listed first on every real kernel's output). A line is only rejected
// (ok=false) when it has too few tokens to plausibly be a conntrack line,
// or is missing a required key (src/dst) or one of those keys fails to
// parse — a legitimately absent optional key (sport/dport for icmp,
// packets/bytes with accounting disabled) is not an error.
func parseConntrackLine(line string) (ConntrackEntry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return ConntrackEntry{}, false
	}
	proto, err := strconv.Atoi(fields[3])
	if err != nil {
		return ConntrackEntry{}, false
	}

	var (
		src, dst           string
		sport, dport       int
		packets, byteCount int64
		seen               = map[string]bool{}
	)
	for _, tok := range fields[4:] {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || seen[k] {
			continue // bare flag ([ASSURED], a state word, ...) or a repeated (reply-direction) key — keep only the first
		}
		switch k {
		case "src":
			src = v
			seen[k] = true
		case "dst":
			dst = v
			seen[k] = true
		case "sport":
			n, perr := strconv.Atoi(v)
			if perr != nil {
				return ConntrackEntry{}, false
			}
			sport = n
			seen[k] = true
		case "dport":
			n, perr := strconv.Atoi(v)
			if perr != nil {
				return ConntrackEntry{}, false
			}
			dport = n
			seen[k] = true
		case "packets":
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				return ConntrackEntry{}, false
			}
			packets = n
			seen[k] = true
		case "bytes":
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				return ConntrackEntry{}, false
			}
			byteCount = n
			seen[k] = true
		}
	}
	if src == "" || dst == "" {
		return ConntrackEntry{}, false
	}

	return ConntrackEntry{
		Proto: proto, SrcIP: src, DstIP: dst,
		SrcPort: sport, DstPort: dport,
		Packets: packets, Bytes: byteCount,
	}, true
}

// ConntrackSampler polls a ConntrackReader on an interval, diffing each
// connection's cumulative packets/bytes counters against the previous
// poll's snapshot (a connection observed for the first time reports its
// full current counters — a defensible "first sample of a long-lived
// connection" reading), and emits one flow.Record per connection whose
// counters actually advanced (a connection with no new traffic since the
// last poll produces no Record — nothing changed to report).
type ConntrackSampler struct {
	Reader ConntrackReader
	Now    func() time.Time
	Logger *slog.Logger
	// last/mu below are private diff state, not config — set only via
	// NewConntrackSampler/Sample, never by a caller.
	last map[string]ConntrackEntry
	Node string
	mu   sync.Mutex
}

// NewConntrackSampler builds a ConntrackSampler reading through reader,
// tagging every emitted Record with node.
func NewConntrackSampler(reader ConntrackReader, node string) *ConntrackSampler {
	return &ConntrackSampler{Reader: reader, Node: node, last: map[string]ConntrackEntry{}}
}

// Sample polls once and returns the diffed flow.Records (source: conntrack)
// plus the count of malformed lines skipped in this poll (always 0 for the
// production netlink reader). An error is only ever the reader's own error
// (the netlink dump failed, or — for the text-format reader — the file
// couldn't be read) — never a parse failure, which is always a defensive
// skip instead.
func (s *ConntrackSampler) Sample(ctx context.Context) ([]flow.Record, int, error) {
	entries, skipped, err := s.Reader.ReadEntries(ctx)
	if err != nil {
		return nil, 0, err
	}

	now := s.Now
	if now == nil {
		now = time.Now
	}
	at := now().Unix()

	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]struct{}, len(entries))
	var records []flow.Record
	for _, e := range entries {
		k := e.key()
		seen[k] = struct{}{}
		prev, existed := s.last[k]

		deltaPackets := e.Packets
		deltaBytes := e.Bytes
		if existed {
			deltaPackets = e.Packets - prev.Packets
			deltaBytes = e.Bytes - prev.Bytes
			if deltaPackets < 0 || deltaBytes < 0 {
				// The kernel's own counter went backwards — the connection
				// was almost certainly torn down and a new one reused the
				// same tuple between polls. Treat this poll's absolute
				// values as a fresh first sample rather than emitting a
				// nonsensical negative-byte Record.
				deltaPackets = e.Packets
				deltaBytes = e.Bytes
			}
		}
		s.last[k] = e

		if deltaPackets == 0 && deltaBytes == 0 {
			continue // no new traffic on this connection since the last poll
		}
		records = append(records, flow.Record{
			Node: s.Node, SrcIP: e.SrcIP, DstIP: e.DstIP,
			SrcPort: e.SrcPort, DstPort: e.DstPort, Proto: e.Proto,
			Bytes: deltaBytes, Packets: deltaPackets,
			At:     at,
			Source: flow.SourceConntrack,
		})
	}

	// Connections that vanished between polls (torn down, timed out) are
	// simply dropped from the snapshot — there is nothing further to
	// report for them, and keeping them around would leak memory across a
	// long-running daemon.
	for k := range s.last {
		if _, ok := seen[k]; !ok {
			delete(s.last, k)
		}
	}

	return records, skipped, nil
}

// Run polls every interval until ctx is cancelled, calling ingest with each
// non-empty batch Sample produces — the same run-loop shape
// flow.Service.RunPruneLoop and flow.Listener.Run both use (prime
// immediately, then tick; never returns a non-nil error on a clean
// shutdown).
//
// Two error outcomes are handled differently (T-3711 — this used to log
// "conntrack poll failed" every interval forever, indistinguishable from a
// working-but-empty poll except by reading the log): a genuinely
// unavailable conntrack interface (Sample's error wraps
// ErrConntrackUnavailable — no CAP_NET_ADMIN, or the kernel has no
// conntrack netlink support at all) is logged ONCE at Error level with a
// clear reason, and the loop stops (Run returns nil, not an error — this
// is a clean, expected degradation, not a crash) rather than retrying a
// condition that will not change until the daemon restarts with different
// privileges. Any other error (a transient read failure) is logged at
// Warn and does not stop the loop — retrying is the right response to a
// blip.
func (s *ConntrackSampler) Run(ctx context.Context, interval time.Duration, ingest func(ctx context.Context, records []flow.Record)) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultHostSampleInterval
	}

	// poll runs one Sample and reports whether the sampler must stop
	// permanently.
	poll := func() (stop bool) {
		records, skipped, err := s.Sample(ctx)
		if err != nil {
			if errors.Is(err, ErrConntrackUnavailable) {
				logger.Error("hostsample: conntrack interface unavailable on this node; stopping conntrack sampling", "error", err)
				return true
			}
			logger.Warn("hostsample: conntrack poll failed", "error", err)
			return false
		}
		if skipped > 0 {
			logger.Debug("hostsample: skipped malformed conntrack lines", "skipped", skipped)
		}
		if len(records) > 0 && ingest != nil {
			ingest(ctx, records)
		}
		return false
	}

	if poll() { // prime immediately rather than waiting a full interval
		return nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if poll() {
				return nil
			}
		}
	}
}
