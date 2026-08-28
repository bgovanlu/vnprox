// SPDX-License-Identifier: Apache-2.0

package host

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrCorosyncUnavailable indicates corosync-cfgtool is not installed (or
// corosync is not running at all) on this node — docs/features/
// monitoring.md §5's `corosync_link_degraded` check degrades gracefully for
// a node with no corosync at all (e.g. a single, not-yet-clustered node),
// distinct from "installed but erroring". Real.CorosyncStatus wraps exec's
// "not found" failure with this sentinel so callers can report a clean
// per-node "no corosync" instead of a hard error.
var ErrCorosyncUnavailable = errors.New("host: corosync-cfgtool not available")

// DefaultCorosyncConfPath is the standard location of PVE's corosync
// config. Unlike every other read in this package, this one file needs no
// per-node routing: /etc/pve is itself pmxcfs, the cluster's distributed
// filesystem (docs/architecture.md, docs/security.md), so corosync.conf is
// byte-identical on every node's local disk — any node's daemon can read
// its own local copy to learn every node's corosync ring addresses, rather
// than fanning out to a peer.
const DefaultCorosyncConfPath = "/etc/pve/corosync.conf"

// CorosyncNode is one corosync.conf nodelist entry: a cluster member's
// name, corosync node id, and ring addresses (ring0_addr is the primary
// corosync link; ring1_addr etc. are optional redundant rings — PVE's
// "safety interlocks" treat every configured ring as a protected link,
// docs/security.md).
type CorosyncNode struct {
	Name      string
	RingAddrs []string
	NodeID    int
}

// CorosyncConfig is a parsed corosync.conf's nodelist.
type CorosyncConfig struct {
	Nodes []CorosyncNode
}

// NodeByName returns the CorosyncNode entry named name, or ok=false. Safe
// to call on a nil *CorosyncConfig (a node with no configured corosync at
// all, e.g. a not-yet-clustered single node, has no ring addresses to
// protect).
func (c *CorosyncConfig) NodeByName(name string) (CorosyncNode, bool) {
	if c == nil {
		return CorosyncNode{}, false
	}
	for _, n := range c.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return CorosyncNode{}, false
}

// ParseCorosyncConf parses corosync.conf's brace-delimited "block {" /
// "key: value" / "}" syntax, extracting only the nodelist { node { ... } }
// stanzas this package cares about (name, nodeid, ring*_addr). This is a
// deliberately narrow, tolerant parser, not a general corosync.conf
// grammar: every other section (totem, quorum, logging) and every other
// key inside a node stanza is silently skipped, so an unrelated corosync
// feature this package doesn't understand never breaks safety-interlock
// detection (see internal/change.DetectProtected, the caller).
func ParseCorosyncConf(data []byte) (*CorosyncConfig, error) {
	cfg := &CorosyncConfig{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	// path tracks the brace-nesting block-name stack, e.g. ["nodelist", "node"].
	var path []string
	var cur *CorosyncNode

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case strings.HasSuffix(line, "{"):
			name := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			if name == "node" && len(path) > 0 && path[len(path)-1] == "nodelist" {
				cfg.Nodes = append(cfg.Nodes, CorosyncNode{})
				cur = &cfg.Nodes[len(cfg.Nodes)-1]
			}
			path = append(path, name)

		case line == "}":
			if len(path) > 0 {
				closed := path[len(path)-1]
				path = path[:len(path)-1]
				if closed == "node" {
					cur = nil
				}
			}

		default:
			if cur == nil {
				continue
			}
			key, val, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			val = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(val), ";"))
			switch {
			case key == "name":
				cur.Name = val
			case key == "nodeid":
				if n, err := strconv.Atoi(val); err == nil {
					cur.NodeID = n
				}
			case strings.HasPrefix(key, "ring") && strings.HasSuffix(key, "_addr"):
				cur.RingAddrs = append(cur.RingAddrs, val)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("host: scanning corosync.conf: %w", err)
	}
	return cfg, nil
}

// ReadCorosyncConf reads and parses corosync.conf from path (typically
// DefaultCorosyncConfPath). The returned error wraps the underlying
// os.PathError (checkable with errors.Is(err, os.ErrNotExist) — wrapping
// via %w preserves Unwrap, but the *os.PathError is no longer the error's
// dynamic type, so os.IsNotExist itself would not recognize it) so callers
// that tolerate corosync not being configured at all (a single, not-yet-
// clustered node has no /etc/pve/corosync.conf) can distinguish "not
// clustered" from a real read failure.
func ReadCorosyncConf(path string) (*CorosyncConfig, error) {
	if path == "" {
		path = DefaultCorosyncConfPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("host: reading corosync.conf %s: %w", path, err)
	}
	return ParseCorosyncConf(data)
}

// RingStatus is one corosync ring/link's live status as reported by
// `corosync-cfgtool -s` (T-803) — distinct from CorosyncNode's static
// configured ring *addresses* above: this is corosync's own knet/totem
// layer's current observation of that ring, which can go faulty without any
// change to corosync.conf.
//
// Two real, differently-shaped `corosync-cfgtool -s` outputs feed this same
// struct (planning/reports/blocked-validation.md §2.1, found against a real
// PVE 9.2.10 two-node cluster — this is not a hypothetical): an older flat
// "RING ID n" / "id\t=" / "status\t=" shape, and knet's real shape (knet has
// been PVE's default transport since 6.x, so this is the common case on any
// real deployment today):
//
//	LINK ID 0 udp
//		addr	= 192.168.1.9
//		status:
//			nodeid:          1:	localhost
//			nodeid:          2:	connected
//
// For a knet "LINK ID" block, RingID is the link's number and Addr is its
// "addr\t=" value, same as the old shape's "id\t=" — but there is no single
// "status\t=" line to read verbatim; instead each cluster member's
// connection state as seen from this link is reported on its own nested
// "nodeid: N: <state>" line. StatusText/Faulty are synthesized from those
// lines (see ParseCorosyncStatus) rather than copied from one source line.
type RingStatus struct {
	// Addr is the ring/link's local interface address, as printed after
	// "id\t=" (older shape) or "addr\t=" (knet) in cfgtool's output.
	Addr string
	// StatusText is a human-readable status summary: the raw, unparsed
	// status line verbatim for the older shape (e.g. "ring 0 active with
	// no faults", or a "Marking ringid N interface X FAULTY ..." variant —
	// kept verbatim since the exact wording is not stable across corosync
	// versions/transports, see planning/reports/needs-hardware-validation.md),
	// or a synthesized "nodeid N: <state>; nodeid M: <state>; ..." summary
	// for a knet link, joining every peer connection state this link
	// reported.
	StatusText string
	RingID     int
	// Faulty is derived from StatusText for the older shape: false iff the
	// status text contains "no faults" (case-insensitive) — the one
	// substring every observed corosync-cfgtool build uses for a healthy
	// ring — true for every other wording (FAULTY, down, degraded, ...).
	//
	// For a knet link, Faulty is false iff every "nodeid: N: <state>" line
	// this link reported has state "localhost" (this node itself) or
	// "connected" (a healthy peer, case-insensitive) — any other wording is
	// treated as faulty. The real wording knet uses for a genuinely
	// disconnected/faulty peer was not captured on hardware as of this
	// parser's introduction (only the healthy 2/2 case was observed —
	// planning/reports/needs-hardware-validation.md); this permissive
	// default follows the same "not textually confirmed healthy = worth
	// flagging" philosophy the older shape's Faulty already establishes,
	// rather than hard-coding an unconfirmed FAULTY string.
	Faulty bool
}

// ParseCorosyncStatus parses `corosync-cfgtool -s` output (see this file's
// doc comment on RingStatus for the exact-wording caveat, and for both real
// shapes this parses) into one RingStatus per "RING ID n" (older shape) or
// "LINK ID n <transport>" (knet, PVE's default since 6.x) block. This is a
// deliberately tolerant, line-oriented parser (mirroring ParseCorosyncConf's
// own "narrow and tolerant, not a full grammar" stance): any line it doesn't
// recognize (header lines like "Printing ring status."/"Local node ID n",
// blank lines, or a future field this package doesn't know about) is
// silently skipped rather than failing the whole parse. Malformed/
// adversarial input never panics: any unexpected internal panic is
// recovered and returned as an error, matching host.ParseBGPSummary/
// ParseEVPNVNI's convention. Empty input returns (nil, nil) — "no output"
// is not itself an error; ErrCorosyncUnavailable is a distinct,
// sentinel-carrying condition callers detect from the *exec* failure, not
// from parsing empty output.
func ParseCorosyncStatus(raw []byte) (rings []RingStatus, err error) {
	defer func() {
		if r := recover(); r != nil {
			rings, err = nil, fmt.Errorf("host: corosync: status parser panic recovered: %v", r)
		}
	}()

	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}

	var cur *RingStatus
	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if id, ok := parseRingIDHeader(line); ok {
			rings = append(rings, RingStatus{RingID: id})
			cur = &rings[len(rings)-1]
			continue
		}
		if id, ok := parseLinkIDHeader(line); ok {
			rings = append(rings, RingStatus{RingID: id})
			cur = &rings[len(rings)-1]
			continue
		}
		if cur == nil {
			continue // header/preamble line before any "RING ID"/"LINK ID" block
		}

		// knet's nested per-peer connection-state line — has its own
		// "key: n: value" shape distinct from every other line this parser
		// recognizes, so it is checked before the generic "key = value"
		// cut below (a bare "status:" section-marker line, knet's other
		// addition, has no "=" at all and simply falls through unmatched).
		if nodeID, state, ok := parseNodeIDLine(line); ok {
			if cur.StatusText != "" {
				cur.StatusText += "; "
			}
			cur.StatusText += fmt.Sprintf("nodeid %d: %s", nodeID, state)
			if !isHealthyKnetNodeState(state) {
				cur.Faulty = true
			}
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "id", "addr":
			cur.Addr = val
		case "status":
			cur.StatusText = val
			cur.Faulty = !strings.Contains(strings.ToLower(val), "no faults")
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("host: corosync: scanning status output: %w", scanErr)
	}
	return rings, nil
}

// parseRingIDHeader recognizes a "RING ID n" header line (cfgtool's older,
// non-knet per-ring block delimiter), case-insensitively (observed corosync
// builds are consistent about "RING ID", but this parser stays tolerant per
// its doc comment).
func parseRingIDHeader(line string) (id int, ok bool) {
	const prefix = "ring id"
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, prefix) {
		return 0, false
	}
	rest := strings.TrimSpace(line[len(prefix):])
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseLinkIDHeader recognizes a "LINK ID n <transport>" header line
// (cfgtool's knet per-link block delimiter — e.g. "LINK ID 0 udp"),
// case-insensitively. Unlike parseRingIDHeader's "RING ID n", knet's header
// carries a trailing transport name after the number, so only the first
// whitespace-separated token after the prefix is parsed as the id; anything
// after it is ignored rather than rejecting the whole line.
func parseLinkIDHeader(line string) (id int, ok bool) {
	const prefix = "link id"
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, prefix) {
		return 0, false
	}
	fields := strings.Fields(line[len(prefix):])
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseNodeIDLine recognizes one knet "nodeid: N: <state>" line (e.g.
// "nodeid:          1:	localhost", "nodeid:          2:	connected"),
// returning the cluster member's corosync node id and its raw connection
// state word verbatim (never normalized here — isHealthyKnetNodeState does
// the case-insensitive comparison, so the original wording survives into
// RingStatus.StatusText for a finding's detail text).
func parseNodeIDLine(line string) (nodeID int, state string, ok bool) {
	const prefix = "nodeid:"
	if !strings.HasPrefix(line, prefix) {
		return 0, "", false
	}
	rest := strings.TrimSpace(line[len(prefix):])
	idPart, statePart, found := strings.Cut(rest, ":")
	if !found {
		return 0, "", false
	}
	n, err := strconv.Atoi(strings.TrimSpace(idPart))
	if err != nil {
		return 0, "", false
	}
	state = strings.TrimSpace(statePart)
	if state == "" {
		return 0, "", false
	}
	return n, state, true
}

// isHealthyKnetNodeState reports whether state (one knet "nodeid: N: ..."
// line's connection-state word) is a textually-confirmed-healthy state:
// "localhost" (this node's own entry) or "connected" (a healthy peer),
// case-insensitive — see RingStatus.Faulty's doc comment for why every
// other wording is treated as faulty rather than assumed healthy.
func isHealthyKnetNodeState(state string) bool {
	switch strings.ToLower(state) {
	case "localhost", "connected":
		return true
	default:
		return false
	}
}
