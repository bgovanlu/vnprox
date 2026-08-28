// SPDX-License-Identifier: Apache-2.0

package fwlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// Compile-time assertions that both concrete Source implementations
// actually satisfy the interface (function-value signature typos would
// otherwise only surface where they're wired in, much later).
var (
	_ Source = (*FileSource)(nil)
	_ Source = (*MemorySource)(nil)
)

// ErrNotFound indicates the requested node has no known log source —
// mirrors host.ErrNotFound/pvemock.ErrNotFound's convention so callers can
// use errors.Is uniformly across vnprox's various per-node reader seams.
var ErrNotFound = errors.New("fwlog: not found")

// Source is the per-node "read this node's firewall log" seam — the same
// conceptual slot host.Reader's InterfacesFile/Stats/etc. occupy for other
// node-local files, but deliberately its own small interface (mirroring
// internal/peer's AuditReader/SnapshotReader precedent, T-303) rather than
// added to host.Reader: pve-firewall's log is not part of the interfaces/
// netlink/LLDP/stats set every existing Reader implementation already has
// to serve, so adding a method there would ripple into every implementer
// (Real, FixtureReader, pvemock.FixtureHostReader, peer.HostReader) for a
// single new capability. A remote node's log is read via the peer API
// (internal/peer's FirewallLogReader/Client.FirewallLog, added alongside
// this package) — this interface is what backs *both* sides of that call:
// a Source directly on the local node, and a Source-shaped adapter over
// peer.Client for a remote one (see Service).
//
// Tail returns lines for node either from the most recent window (cursor
// == "") or appended since cursor (an opaque, source-defined position —
// callers must treat it as an opaque string, never parse it). nextCursor
// resumes exactly after the last line returned; passing it back on the
// next call returns only newly appended lines (the "follow" mechanism). A
// cursor from a rotated/truncated file is not an error: implementations
// restart from the beginning and say so via the returned reset flag so
// callers know history may have been skipped, without ever erroring out
// of a routine log-rotation event.
type Source interface {
	Tail(ctx context.Context, node, cursor string, maxLines int) (lines []string, nextCursor string, reset bool, err error)
}

// tailBytes is the pure, source-independent "offset over a line-oriented
// byte blob" tailer both FileSource and MemorySource are built on: cursor
// == "" returns the last maxLines lines of data (bounded initial "tail -n");
// a numeric byte-offset cursor returns every complete line appended after
// that offset, capped at maxLines (excess lines are simply left for the
// next call — cursor only ever advances to the end of what's returned, so
// nothing is skipped). A trailing, not-yet-newline-terminated partial line
// is never returned (the writer may still be appending to it); the offset
// held back for it is included in nextCursor so the next call picks it up
// once complete.
func tailBytes(data []byte, cursor string, maxLines int) (lines []string, nextCursor string, reset bool) {
	if maxLines <= 0 {
		maxLines = 1
	}

	var start int64
	if cursor != "" {
		off, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || off < 0 || off > int64(len(data)) {
			// Malformed or stale (rotated/truncated) cursor: restart from
			// the beginning rather than erroring — routine for a log file,
			// per this function's doc comment.
			start = 0
			reset = true
		} else {
			start = off
		}
	}

	window := data[start:]
	// Only complete lines: hold back a trailing partial line (no final
	// newline yet).
	complete := window
	partialLen := 0
	if n := len(window); n > 0 && window[n-1] != '\n' {
		if idx := bytes.LastIndexByte(window, '\n'); idx >= 0 {
			complete = window[:idx+1]
			partialLen = n - (idx + 1)
		} else {
			complete = nil
			partialLen = n
		}
	}

	all := splitLines(complete)
	consumedBytes := len(complete)

	if cursor == "" && len(all) > maxLines {
		// Initial tail: keep only the newest maxLines lines, but the
		// cursor must still advance to the true end of what was scanned
		// (start + consumedBytes), not to some earlier point — otherwise
		// the very next follow tick would re-return the lines this call
		// already discarded as "too old for the initial view".
		all = all[len(all)-maxLines:]
	} else if len(all) > maxLines {
		// Follow increment: more new lines arrived than the cap allows.
		// Return only the oldest maxLines of them (never skip forward —
		// the caller's own cursor bookkeeping must stay contiguous), and
		// advance the cursor only as far as those consumed lines actually
		// reach, so the remainder is retried (not lost) on the next call.
		consumedBytes = 0
		for _, l := range all[:maxLines] {
			consumedBytes += len(l) + 1 // +1 for the newline stripped by splitLines
		}
		all = all[:maxLines]
	}

	next := start + int64(consumedBytes)
	_ = partialLen // documented above: intentionally not consumed
	return all, strconv.FormatInt(next, 10), reset
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	raw := bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
	out := make([]string, len(raw))
	for i, l := range raw {
		out[i] = string(bytes.TrimSuffix(l, []byte("\r")))
	}
	return out
}

// FileSource reads a real pve-firewall log file from disk — the
// production Source, always serving this daemon's own node (mirrors
// host.Real's "only ever serves its own node" convention: routing a
// remote node's request to that node's own FileSource instance is the
// peer API's job, not this type's).
type FileSource struct {
	// Path is the log file's location. docs/features/firewall.md §4
	// doesn't pin an exact path; this defaults to the conventional
	// /var/log/pve-firewall.log (see DefaultLogPath) reported by PVE
	// community documentation — flagged for hardware validation, same as
	// this package's chain-naming/format assumptions (doc.go).
	Path string
}

// DefaultLogPath is pve-firewall's conventional log location.
const DefaultLogPath = "/var/log/pve-firewall.log"

// Tail implements Source. node is accepted but unused (see doc comment);
// a missing file is reported as ErrNotFound rather than a bare os error so
// callers can special-case "not installed/no rotation yet" the same way
// they already do for host.ErrNotFound.
func (f *FileSource) Tail(_ context.Context, _ string, cursor string, maxLines int) ([]string, string, bool, error) {
	path := f.Path
	if path == "" {
		path = DefaultLogPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, cursor, false, fmt.Errorf("fwlog: reading %s: %w", path, ErrNotFound)
		}
		return nil, cursor, false, fmt.Errorf("fwlog: reading %s: %w", path, err)
	}
	lines, next, reset := tailBytes(data, cursor, maxLines)
	return lines, next, reset, nil
}

// MemorySource is an in-memory Source: the fixture/test double used by
// `make dev` (seeded once from a static corpus — see cmd/vnproxd's
// wiring) and by this package's own tests, including the storm test,
// which Append()s synthetic lines to simulate a live log growing much
// faster than a real disk file conveniently would in a unit test. Safe
// for concurrent use.
type MemorySource struct {
	content map[string][]byte
	mu      sync.Mutex
}

// NewMemorySource builds an empty MemorySource.
func NewMemorySource() *MemorySource {
	return &MemorySource{content: map[string][]byte{}}
}

// Seed sets node's full log content, replacing anything previously
// written (used to load a static fixture corpus at startup).
func (m *MemorySource) Seed(node, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.content[node] = []byte(content)
}

// Append adds lines to node's log content (each terminated with a
// newline), simulating new lines being written to a live log.
func (m *MemorySource) Append(node string, lines ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	buf := m.content[node]
	for _, l := range lines {
		buf = append(buf, []byte(l)...)
		buf = append(buf, '\n')
	}
	m.content[node] = buf
}

// Tail implements Source over the in-memory content for node. Unlike
// FileSource, node is meaningful here: MemorySource is used both to seed
// the local node's own content and, in tests, to stand in for every
// peer's content too (one MemorySource, many node keys).
func (m *MemorySource) Tail(_ context.Context, node, cursor string, maxLines int) ([]string, string, bool, error) {
	m.mu.Lock()
	data, ok := m.content[node]
	m.mu.Unlock()
	if !ok {
		return nil, cursor, false, fmt.Errorf("fwlog: node %s: %w", node, ErrNotFound)
	}
	lines, next, reset := tailBytes(data, cursor, maxLines)
	return lines, next, reset, nil
}
