package fwlog

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Entry is one successfully parsed pve-firewall log line (see doc.go for
// the format this decodes). Node is the cluster node this line was read
// from (set by the caller, not derived from the line itself — a
// cross-cluster fan-out reader stamps each source's own node name).
type Entry struct {
	Timestamp time.Time

	Fields map[string]string // every "KEY=VALUE" token found in the message tail, keyed verbatim (e.g. "SRC", "DPT")

	Node      string // cluster node this line was read from
	Raw       string // the original line, trimmed of its trailing newline
	Chain     string
	Direction string // "in" | "out" | "" (unknown)
	Action    string // ACCEPT | DROP | REJECT | ... (upper-cased), "" if undeterminable

	// The following are convenience copies of well-known Fields entries,
	// populated whenever present (empty string otherwise).
	Proto  string
	Source string
	Dest   string
	Sport  string
	Dport  string
	In     string
	Out    string

	VMID     int
	LogLevel int
	NicIndex int

	Guest             bool // Chain matched a recognized guest tap/veth chain
	HasTimestamp      bool
	PolicyFallthrough bool // message was "policy $POLICY: ..." (default-policy hit, not a specific rule)
}

// Result is the outcome of parsing a whole log stream (ParseAll):
// successfully parsed entries plus the AC1-required "garbage lines skipped
// with a counter, never crash" accounting.
type Result struct {
	Entries []Entry
	Total   int // every non-empty line seen
	Parsed  int // len(Entries)
	Skipped int // garbage/unparsable lines (Total - Parsed)
}

// guestChainRe matches pve-firewall's documented guest chain naming
// (pvefw-logger.c's own example: "tap117i0-IN"; containers use "veth"
// instead of "tap" — same "<vmid>i<nic>-<DIR>" suffix convention).
var guestChainRe = regexp.MustCompile(`^(?:tap|veth)(\d+)i(\d+)-(IN|OUT)$`)

// clfTimestampLayout is the two-token timestamp pvefw-logger.c's example
// line uses: "14/Mar/2014:12:47:07" + " +0100" (Apache/CLF-style date,
// space-separated from its own timezone offset token).
const clfTimestampLayout = "02/Jan/2006:15:04:05 -0700"

// ParseLine parses one raw pve-firewall log line (no trailing newline
// required — trimmed defensively either way). ok is false iff the line is
// garbage: fewer than 3 whitespace-separated fields, a non-integer/negative
// leading VMID field, or a log-level field outside pve-firewall's
// documented 0-7 range (PVE::Firewall.pm's get_log_rule_base loglevel
// check). Any other malformed content (bad timestamp, unrecognized chain,
// unparsable message) still yields a best-effort Entry — never a crash,
// never silently discarded data beyond what's genuinely unrecoverable.
func ParseLine(node, raw string) (Entry, bool) {
	line := strings.TrimRight(raw, "\r\n")
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return Entry{}, false
	}

	vmid, err := strconv.Atoi(fields[0])
	if err != nil || vmid < 0 {
		return Entry{}, false
	}
	level, err := strconv.Atoi(fields[1])
	if err != nil || level < 0 || level > 7 {
		return Entry{}, false
	}
	chain := fields[2]

	e := Entry{
		Node: node, Raw: line, VMID: vmid, LogLevel: level, Chain: chain,
		Fields: map[string]string{},
	}

	msgStart := 3
	if ts, consumed, ok := parseTimestamp(fields, 3); ok {
		e.Timestamp = ts
		e.HasTimestamp = true
		msgStart = 3 + consumed
	}

	if m := guestChainRe.FindStringSubmatch(chain); m != nil {
		e.Guest = true
		if gv, err := strconv.Atoi(m[1]); err == nil {
			// The chain name is pvefw-logger's own authoritative encoding
			// of which guest/nic/direction a line belongs to (see doc.go);
			// prefer it over the leading VMID field on the rare/malformed
			// case they disagree (VMID is 0 for host-scope rules, per
			// PVE::Firewall.pm's get_log_rule_base default, so a guest
			// chain's own vmid is always the more specific truth).
			e.VMID = gv
		}
		if nic, err := strconv.Atoi(m[2]); err == nil {
			e.NicIndex = nic
		}
		if m[3] == "IN" {
			e.Direction = "in"
		} else {
			e.Direction = "out"
		}
	} else {
		e.Direction = directionFromChainSuffix(chain)
	}

	policy, action, kv := splitMessage(fields[msgStart:])
	e.PolicyFallthrough = policy
	e.Action = strings.ToUpper(action)
	for _, tok := range kv {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || k == "" {
			continue // bare flag token (e.g. "SYN") or malformed; skip, never error
		}
		e.Fields[k] = v
	}
	e.Proto = e.Fields["PROTO"]
	e.Source = e.Fields["SRC"]
	e.Dest = e.Fields["DST"]
	e.Sport = e.Fields["SPT"]
	e.Dport = e.Fields["DPT"]
	e.In = e.Fields["IN"]
	e.Out = e.Fields["OUT"]

	return e, true
}

// directionFromChainSuffix is the fallback direction guess for a chain
// name that didn't match guestChainRe (a node/cluster/host-scope chain, or
// one this build doesn't recognize) — plain "-IN"/"-OUT" suffix matching,
// deliberately weaker than the guest case: it does not set Entry.Guest, so
// Correlate never claims rule-level correlation for it (see doc.go's
// "Scope: guest chains only").
func directionFromChainSuffix(chain string) string {
	switch {
	case strings.HasSuffix(chain, "-IN"):
		return "in"
	case strings.HasSuffix(chain, "-OUT"):
		return "out"
	default:
		return ""
	}
}

// parseTimestamp tries, in order: a single RFC3339 token (a documented
// format variant this build also accepts — e.g. a journald-forwarded line
// re-timestamped in ISO8601), then pvefw-logger's own two-token CLF-style
// timestamp (date+time, then a separate timezone-offset token). ok is
// false if neither matches; callers must treat that as "timestamp
// unavailable", not "garbage line" (see ParseLine's doc comment).
func parseTimestamp(fields []string, idx int) (t time.Time, consumed int, ok bool) {
	if idx >= len(fields) {
		return time.Time{}, 0, false
	}
	if strings.Contains(fields[idx], "T") {
		if ts, err := time.Parse(time.RFC3339, fields[idx]); err == nil {
			return ts, 1, true
		}
	}
	if idx+1 < len(fields) {
		if ts, err := time.Parse(clfTimestampLayout, fields[idx]+" "+fields[idx+1]); err == nil {
			return ts, 2, true
		}
	}
	return time.Time{}, 0, false
}

// splitMessage separates a log line's message tail into: whether it opens
// with "policy" (a default-policy fallthrough, per PVE::Firewall.pm's
// ruleset_add_chain_policy), the matched action word (colon stripped), and
// the remaining KV-token slice. A leading token containing '=' is treated
// as already being a KV field (no action word present in this variant of
// the message) rather than misparsed as the action.
func splitMessage(tokens []string) (policyFallthrough bool, action string, kv []string) {
	idx := 0
	if len(tokens) > 0 && strings.EqualFold(tokens[0], "policy") {
		policyFallthrough = true
		idx = 1
	}
	if idx < len(tokens) && !strings.Contains(tokens[idx], "=") {
		action = strings.TrimSuffix(tokens[idx], ":")
		idx++
	}
	return policyFallthrough, action, tokens[idx:]
}

// ParseAll parses every line of r (a whole node's log, or a tail/follow
// increment of it), tagging each successfully parsed Entry with node.
// Never returns an error: an unreadable stream simply stops early with
// whatever was parsed so far, since a log tailer's "the file vanished
// mid-read" case is routine, not exceptional (AC1: "never crash").
func ParseAll(node string, r io.Reader) Result {
	br := bufio.NewReaderSize(r, 64*1024)
	var res Result
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			res.Total++
			if trimmed == "" {
				res.Skipped++
			} else if e, ok := ParseLine(node, trimmed); ok {
				res.Entries = append(res.Entries, e)
				res.Parsed++
			} else {
				res.Skipped++
			}
		}
		if err != nil {
			return res // io.EOF (normal) or a read error — either way, stop
		}
	}
}
